// Package matrix provides the plaintext Matrix Client-Server transport for
// the channel engine. It is not registered in production until installation,
// identity binding and outbound event-bus wiring have been implemented.
package matrix

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxResponseBytes = 4 << 20

// Client authenticates as one bot device. Access tokens are carried only in
// Authorization headers, never URLs. The supplied homeserver must be trusted
// by the administrator installing the connector.
type Client struct {
	base  string
	token string
	http  *http.Client
}

func newClient(homeserver, token string, client *http.Client) (*Client, error) {
	u, err := url.Parse(homeserver)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("matrix: homeserver must be an HTTP(S) origin without credentials, query or fragment")
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("matrix: missing access token")
	}
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	// A homeserver redirect must not forward the bot's credential to a different
	// origin (including redirects to another port on the same hostname).
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{base: strings.TrimRight(homeserver, "/"), token: token, http: &copyClient}, nil
}

// APIError excludes server-supplied error text: a homeserver must not leak an
// echoed access token or response payload through logs.
type APIError struct {
	Status int
	Code   string
}

func (e *APIError) Error() string { return fmt.Sprintf("matrix: HTTP %d (%s)", e.Status, e.Code) }

func (c *Client) request(ctx context.Context, method, path string, query url.Values, payload, result any) error {
	var body []byte
	var err error
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return err
		}
	}
	endpoint := c.base + "/_matrix/client/v3/" + path
	if len(query) != 0 {
		endpoint += "?" + query.Encode()
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("matrix: request failed: %w", err)
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
		resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("matrix: read response: %w", readErr)
		}
		if len(data) > maxResponseBytes {
			return errors.New("matrix: response exceeds 4 MiB limit")
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if result == nil {
				return nil
			}
			if err := json.Unmarshal(data, result); err != nil {
				return errors.New("matrix: invalid JSON response")
			}
			return nil
		}
		var failure struct {
			Code    string `json:"errcode"`
			RetryMS int64  `json:"retry_after_ms"`
		}
		_ = json.Unmarshal(data, &failure)
		// Only accept the protocol's symbolic error codes, not arbitrary text.
		code := "unknown"
		if strings.HasPrefix(failure.Code, "M_") && len(failure.Code) <= 64 {
			valid := true
			for _, r := range failure.Code {
				if !(r >= 'A' && r <= 'Z' || r == '_') {
					valid = false
				}
			}
			if valid {
				code = failure.Code
			}
		}
		apiErr := &APIError{Status: resp.StatusCode, Code: code}
		if resp.StatusCode != http.StatusTooManyRequests || attempt >= 3 {
			return apiErr
		}
		wait := time.Second
		// Synapse supports the legacy field; the current spec prefers the header.
		if seconds, err := strconv.ParseInt(resp.Header.Get("Retry-After"), 10, 64); err == nil && seconds >= 0 {
			if seconds > 120 {
				return apiErr
			}
			wait = time.Duration(seconds) * time.Second
		} else if failure.RetryMS > 0 && failure.RetryMS <= 120000 {
			wait = time.Duration(failure.RetryMS) * time.Millisecond
		} else if failure.RetryMS > 120000 {
			return apiErr
		}
		if err := waitContext(ctx, wait); err != nil {
			return err
		}
	}
}

func waitContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func roomPath(roomID, suffix string) string { return "rooms/" + url.PathEscape(roomID) + "/" + suffix }
