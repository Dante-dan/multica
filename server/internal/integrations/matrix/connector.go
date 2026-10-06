package matrix

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/integrations/channel"
)

const TypeMatrix channel.Type = "matrix"

// Config is the per-installation channel config. Stable user/device IDs are
// verified with /account/whoami before receiving messages. The token is stored
// encrypted using the same secretbox boundary as existing adapters.
type Config struct {
	AppID          string `json:"app_id"`
	Homeserver     string `json:"homeserver"`
	UserID         string `json:"user_id"`
	DeviceID       string `json:"device_id"`
	TokenEncrypted string `json:"access_token_encrypted"`
	// RequireMention defaults to true; false opts every plaintext room message
	// into the router. Direct rooms listed in the bot account's m.direct data
	// do not require a mention.
	RequireMention *bool `json:"require_mention,omitempty"`
}

// CursorStore persists the opaque sync token per installation. Handler success
// must mean its deduplicated downstream enqueue is durable before Save returns.
// A failed save keeps the old cursor so the next attempt safely replays events.
type CursorStore interface {
	Load(context.Context) (string, error)
	Save(context.Context, string) error
}

type Dependencies struct {
	Decrypt     func([]byte) ([]byte, error)
	HTTPClient  *http.Client
	CursorStore func(channel.Config) CursorStore
}

// Factory builds an adapter for the existing supervisor/registry contract.
// No production registration is performed by this transport-only slice.
func Factory(deps Dependencies) channel.Factory {
	return func(cfg channel.Config) (channel.Channel, error) {
		var config Config
		if err := json.Unmarshal(cfg.Raw, &config); err != nil {
			return nil, errors.New("matrix: invalid installation config")
		}
		if deps.Decrypt == nil {
			return nil, errors.New("matrix: token decrypter required")
		}
		encrypted, err := base64.StdEncoding.DecodeString(config.TokenEncrypted)
		if err != nil || len(encrypted) == 0 {
			return nil, errors.New("matrix: invalid encrypted access token")
		}
		token, err := deps.Decrypt(encrypted)
		if err != nil {
			return nil, errors.New("matrix: access token decryption failed")
		}
		connector, err := NewConnector(config, string(token), cfg.Handler, deps.HTTPClient)
		if err != nil {
			return nil, err
		}
		if cfg.Handler != nil {
			if deps.CursorStore == nil {
				return nil, errors.New("matrix: inbound requires durable sync cursor storage")
			}
			connector.cursors = deps.CursorStore(cfg)
			if connector.cursors == nil {
				return nil, errors.New("matrix: sync cursor store required")
			}
		}
		return connector, nil
	}
}

// Connector owns one sync cursor for a bot installation. A reconnect retains
// the last fully handled batch; a fresh process baselines without replaying
// old room history. Persisting sync cursors belongs to installation wiring.
type Connector struct {
	config         Config
	client         *Client
	handler        channel.InboundHandler
	cursors        CursorStore
	requireMention bool
	mu             sync.Mutex
	running        bool
	cancel         context.CancelFunc
	since          string
	direct         map[string]bool
	encrypted      map[string]bool
}

var _ channel.Channel = (*Connector)(nil)

// NewConnector constructs a transport from in-memory credentials. Persistent
// callers should use Factory so raw tokens cannot enter installation config.
func NewConnector(config Config, token string, handler channel.InboundHandler, httpClient *http.Client) (*Connector, error) {
	if config.UserID == "" || config.DeviceID == "" {
		return nil, errors.New("matrix: stable user and device IDs required")
	}
	client, err := newClient(config.Homeserver, token, httpClient)
	if err != nil {
		return nil, err
	}
	requireMention := true
	if config.RequireMention != nil {
		requireMention = *config.RequireMention
	}
	return &Connector{config: config, client: client, handler: handler, requireMention: requireMention, direct: map[string]bool{}, encrypted: map[string]bool{}}, nil
}

func (*Connector) Type() channel.Type { return TypeMatrix }
func (*Connector) Capabilities() channel.Capability {
	return channel.CapText | channel.CapMessageEdit | channel.CapTypingIndicator
}

func (c *Connector) Disconnect(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel != nil {
		c.cancel()
	}
	return nil
}

// Connect deliberately does not join invitations or decrypt encrypted rooms.
// Operators must join the bot to plaintext rooms; E2EE requires a separate
// crypto/device-state design, not a silently downgraded session.
func (c *Connector) Connect(ctx context.Context) error {
	if c.handler == nil {
		return errors.New("matrix: inbound handler required")
	}
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return errors.New("matrix: already connected")
	}
	ctx, cancel := context.WithCancel(ctx)
	c.cancel, c.running = cancel, true
	c.mu.Unlock()
	defer func() { cancel(); c.mu.Lock(); c.running = false; c.cancel = nil; c.mu.Unlock() }()
	var who struct {
		UserID   string `json:"user_id"`
		DeviceID string `json:"device_id"`
	}
	if err := c.client.request(ctx, http.MethodGet, "account/whoami", nil, nil, &who); err != nil {
		return graceful(ctx, err)
	}
	if who.UserID != c.config.UserID || who.DeviceID != c.config.DeviceID {
		return errors.New("matrix: access token user/device does not match installation")
	}
	if c.cursors != nil {
		since, err := c.cursors.Load(ctx)
		if err != nil {
			return graceful(ctx, fmt.Errorf("matrix: load sync cursor: %w", err))
		}
		c.since = since
	}
	// m.direct may be omitted from an incremental /sync after a factory
	// rebuild. Bootstrap it independently; full_state bootstraps room state.
	var direct map[string][]string
	if err := c.client.request(ctx, http.MethodGet, "user/"+url.PathEscape(c.config.UserID)+"/account_data/m.direct", nil, nil, &direct); err != nil {
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound || apiErr.Code != "M_NOT_FOUND" {
			return graceful(ctx, err)
		}
	}
	c.direct = map[string]bool{}
	for _, rooms := range direct {
		for _, roomID := range rooms {
			c.direct[roomID] = true
		}
	}
	firstSync := true
	for {
		initial := c.since == ""
		q := url.Values{"timeout": {"30000"}}
		if firstSync {
			q.Set("full_state", "true")
		}
		if !initial {
			q.Set("since", c.since)
		}
		var batch syncResponse
		if err := c.client.request(ctx, http.MethodGet, "sync", q, nil, &batch); err != nil {
			return graceful(ctx, err)
		}
		if batch.NextBatch == "" {
			return errors.New("matrix: sync response missing next_batch")
		}
		c.updateState(batch)
		// The first sync includes historical timelines. Start from its next_batch,
		// never execute historical room messages as fresh coding instructions.
		if !initial {
			for roomID, room := range batch.Rooms.Join {
				if c.encrypted[roomID] {
					continue
				}
				events := room.Timeline.Events
				if room.Timeline.Limited {
					recovered, err := c.backfill(ctx, roomID, c.since, room.Timeline.PrevBatch)
					if err != nil {
						return graceful(ctx, err)
					}
					events = append(recovered, events...)
				}
				seen := map[string]bool{}
				for _, event := range events {
					if seen[event.EventID] {
						continue
					}
					seen[event.EventID] = true
					msg, ok := c.inbound(roomID, event)
					if !ok {
						continue
					}
					if err := c.handler(ctx, msg); err != nil {
						return graceful(ctx, fmt.Errorf("matrix: inbound handler: %w", err))
					}
				}
			}
		}
		// Advance only after every handler succeeds. Engine message-ID dedup owns
		// replayed events when a batch is retried after a partial failure.
		if c.cursors != nil {
			if err := c.cursors.Save(ctx, batch.NextBatch); err != nil {
				return graceful(ctx, fmt.Errorf("matrix: save sync cursor: %w", err))
			}
		}
		c.since = batch.NextBatch
		firstSync = false
	}
}

func graceful(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func (c *Connector) Send(ctx context.Context, out channel.OutboundMessage) (channel.SendResult, error) {
	if out.ThreadID != "" || out.ReplyTo != "" {
		return channel.SendResult{}, errors.New("matrix: first transport supports room-scoped messages only")
	}
	id, err := c.sendContent(ctx, out.ChatID, map[string]any{"msgtype": "m.notice", "body": out.Text})
	return channel.SendResult{MessageID: id}, err
}

func (c *Connector) sendContent(ctx context.Context, roomID string, content any) (string, error) {
	if roomID == "" {
		return "", errors.New("matrix: room ID required")
	}
	// Never downgrade an encrypted room to a plaintext outbound reply, even
	// when this Connector was constructed only for the Send path.
	var encryption json.RawMessage
	if err := c.client.request(ctx, http.MethodGet, roomPath(roomID, "state/m.room.encryption"), nil, nil, &encryption); err == nil {
		return "", errors.New("matrix: encrypted rooms are not supported by the plaintext transport")
	} else {
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound || apiErr.Code != "M_NOT_FOUND" {
			return "", err
		}
	}
	var result struct {
		EventID string `json:"event_id"`
	}
	// One transaction ID is reused through every 429 retry in request().
	path := roomPath(roomID, "send/m.room.message/"+uuid.NewString())
	if err := c.client.request(ctx, http.MethodPut, path, nil, content, &result); err != nil {
		return "", err
	}
	if result.EventID == "" {
		return "", errors.New("matrix: send response missing event_id")
	}
	return result.EventID, nil
}

// Edit posts a Matrix replacement event. Callers still own stream throttling;
// this method handles the homeserver's retry_after_ms on HTTP 429.
func (c *Connector) Edit(ctx context.Context, roomID, eventID, text string) (string, error) {
	if eventID == "" {
		return "", errors.New("matrix: original event ID required")
	}
	return c.sendContent(ctx, roomID, map[string]any{
		"msgtype": "m.notice", "body": "* " + text,
		"m.new_content": map[string]any{"msgtype": "m.notice", "body": text},
		"m.relates_to":  map[string]any{"rel_type": "m.replace", "event_id": eventID},
	})
}

func (c *Connector) SetTyping(ctx context.Context, roomID string, typing bool) error {
	if roomID == "" {
		return errors.New("matrix: room ID required")
	}
	return c.client.request(ctx, http.MethodPut, roomPath(roomID, "typing/"+url.PathEscape(c.config.UserID)), nil, map[string]any{"typing": typing, "timeout": 30000}, nil)
}

// backfill closes a limited incremental timeline gap before advancing /sync.
// A bounded gap failure is visible and leaves the cursor unchanged; it must
// never silently consume only the recent tail of a busy room.
func (c *Connector) backfill(ctx context.Context, roomID, from, to string) ([]roomEvent, error) {
	if to == "" {
		return nil, errors.New("matrix: limited timeline missing prev_batch")
	}
	events := []roomEvent{}
	for page := 0; page < 100 && from != to; page++ {
		var response struct {
			End   string      `json:"end"`
			Chunk []roomEvent `json:"chunk"`
		}
		query := url.Values{"from": {from}, "to": {to}, "dir": {"f"}, "limit": {"100"}}
		if err := c.client.request(ctx, http.MethodGet, roomPath(roomID, "messages"), query, nil, &response); err != nil {
			return nil, err
		}
		events = append(events, response.Chunk...)
		if response.End == to || response.End == "" {
			return events, nil
		}
		if response.End == from {
			return nil, errors.New("matrix: backfill did not reach sync timeline boundary")
		}
		from = response.End
	}
	if from != to {
		return nil, errors.New("matrix: sync gap exceeds 100 pages; cursor not advanced")
	}
	return events, nil
}
