package matrix

import (
 "encoding/json"
 "strings"

 "github.com/multica-ai/multica/server/internal/integrations/channel"
)

type roomEvent struct {
 Type string `json:"type"`
 EventID string `json:"event_id"`
 Sender string `json:"sender"`
 Content json.RawMessage `json:"content"`
}

type eventList struct {
 Events []roomEvent `json:"events"`
 Limited bool `json:"limited,omitempty"`
 PrevBatch string `json:"prev_batch,omitempty"`
}
type syncResponse struct {
 NextBatch string `json:"next_batch"`
 AccountData eventList `json:"account_data"`
 Rooms struct {
  Join map[string]struct { State eventList `json:"state"`; Timeline eventList `json:"timeline"` } `json:"join"`
 } `json:"rooms"`
}

func (c *Connector) updateState(batch syncResponse) {
 for _, event := range batch.AccountData.Events {
  if event.Type != "m.direct" { continue }
  var direct map[string][]string
  if json.Unmarshal(event.Content, &direct) != nil { continue }
  c.direct = map[string]bool{}
  for _, rooms := range direct { for _, roomID := range rooms { c.direct[roomID] = true } }
 }
 for roomID, room := range batch.Rooms.Join {
  for _, events := range []eventList{room.State, room.Timeline} {
   for _, event := range events.Events { if event.Type == "m.room.encryption" || event.Type == "m.room.encrypted" { c.encrypted[roomID] = true } }
  }
 }
}

func (c *Connector) inbound(roomID string, event roomEvent) (channel.InboundMessage, bool) {
 if event.Type != "m.room.message" || event.Sender == "" || event.Sender == c.config.UserID || event.EventID == "" { return channel.InboundMessage{}, false }
 var content struct {
  MsgType string `json:"msgtype"`
  Body string `json:"body"`
  Mentions struct { UserIDs []string `json:"user_ids"` } `json:"m.mentions"`
  Relates struct { RelType string `json:"rel_type"` } `json:"m.relates_to"`
 }
 if json.Unmarshal(event.Content, &content) != nil || content.MsgType != "m.text" || strings.TrimSpace(content.Body) == "" || content.Relates.RelType == "m.replace" { return channel.InboundMessage{}, false }
 direct := c.direct[roomID]
 addressed := direct || !c.requireMention
 for _, id := range content.Mentions.UserIDs { if id == c.config.UserID { addressed = true } }
 // Structured Matrix mentions avoid fuzzy display-name matching. Unsupported
 // legacy HTML/display-name mentions stay silent rather than starting a turn.
 if !addressed { return channel.InboundMessage{}, false }
 chatType := channel.ChatTypeGroup
 if direct { chatType = channel.ChatTypeP2P }
 raw, _ := json.Marshal(struct { BotUserID string `json:"bot_user_id"`; Homeserver string `json:"homeserver"`; Event roomEvent `json:"event"` }{c.config.UserID, c.config.Homeserver, event})
 return channel.InboundMessage{
  EventID:event.EventID, MessageID:event.EventID,
  Source:channel.Source{ChannelType:TypeMatrix, ChatID:roomID, ChatType:chatType, SenderID:event.Sender},
  Type:channel.MsgTypeText, Text:content.Body, CommandText:content.Body,
  AddressedToBot:addressed, Raw:raw,
 }, true
}
