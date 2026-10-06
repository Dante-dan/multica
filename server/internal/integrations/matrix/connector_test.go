package matrix

import (
 "context"
 "encoding/base64"
 "encoding/json"
 "errors"
 "fmt"
 "net/http"
 "net/http/httptest"
 "strings"
 "sync"
 "testing"
 "time"

 "github.com/multica-ai/multica/server/internal/integrations/channel"
)

const bot = "@bot:example.test"
func newTestConnector(t *testing.T, server *httptest.Server, handler channel.InboundHandler) *Connector {
 t.Helper()
 c, err := NewConnector(Config{Homeserver:server.URL, UserID:bot, DeviceID:"DEVICE"}, "secret-token", handler, server.Client())
 if err != nil { t.Fatal(err) }
 return c
}
func writeJSON(w http.ResponseWriter, value any) { w.Header().Set("Content-Type", "application/json"); _ = json.NewEncoder(w).Encode(value) }
func whoami(w http.ResponseWriter) { writeJSON(w,map[string]string{"user_id":bot,"device_id":"DEVICE"}) }
func testEvent(id, sender, text string, mentioned bool) roomEvent {
 ids:=[]string{}
 if mentioned { ids=append(ids,bot) }
 content,_:=json.Marshal(map[string]any{"msgtype":"m.text","body":text,"m.mentions":map[string]any{"user_ids":ids}})
 return roomEvent{Type:"m.room.message",EventID:id,Sender:sender,Content:content}
}
func batch(token string, events ...roomEvent) syncResponse {
 var b syncResponse
 b.NextBatch=token
 b.Rooms.Join=map[string]struct {State eventList `json:"state"`;Timeline eventList `json:"timeline"`}{
 "!room:example.test":{Timeline:eventList{Events:events}},
 }
 return b
}

func TestSyncBaselinesHistoryAndConsumesIncrementalRoomMessages(t *testing.T) {
 ctx,cancel:=context.WithCancel(context.Background());defer cancel()
 var queries []string
 s:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if r.Header.Get("Authorization")!="Bearer secret-token"{t.Errorf("missing bearer auth")}
  if r.URL.Path=="/_matrix/client/v3/account/whoami"{whoami(w);return}
  since:=r.URL.Query().Get("since");queries=append(queries,since)
  if since==""{writeJSON(w,batch("baseline",testEvent("$old","@person:example.test","do not replay",true)));return}
  if since!="baseline"{t.Errorf("since=%q",since)}
  writeJSON(w,batch("next",testEvent("$ignored","@person:example.test","ambient",false),testEvent("$new","@person:example.test","new input",true)))
 }))
 defer s.Close()
 var received []channel.InboundMessage
 c:=newTestConnector(t,s,func(_ context.Context,msg channel.InboundMessage)error{received=append(received,msg);cancel();return nil})
 if err:=c.Connect(ctx);err!=nil{t.Fatal(err)}
 if len(received)!=1 || received[0].MessageID!="$new" || received[0].Text!="new input"{t.Fatalf("received=%+v",received)}
 if received[0].Source.ThreadID!="" || received[0].Source.ChatID!="!room:example.test"{t.Fatalf("room scope: %+v",received[0].Source)}
 if len(queries)!=2 || c.since!="next"{t.Fatalf("queries=%v cursor=%q",queries,c.since)}
}

func TestHandlerFailureDoesNotAdvanceCursorAndReconnectReplays(t *testing.T) {
 var sinceQueries []string
 s:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if strings.HasSuffix(r.URL.Path,"whoami"){whoami(w);return}
  since:=r.URL.Query().Get("since");sinceQueries=append(sinceQueries,since)
  if since==""{writeJSON(w,batch("baseline"));return}
  writeJSON(w,batch("after",testEvent("$event","@alice:test","work",true)))
 }));defer s.Close()
 failed:=true
 ctx,cancel:=context.WithCancel(context.Background());defer cancel()
 c:=newTestConnector(t,s,func(context.Context,channel.InboundMessage)error{
  if failed {failed=false;return errors.New("database unavailable")};cancel();return nil
 })
 if err:=c.Connect(context.Background());err==nil{t.Fatal("handler failure swallowed")}
 if c.since!="baseline"{t.Fatalf("advanced failed cursor: %q",c.since)}
 if err:=c.Connect(ctx);err!=nil{t.Fatal(err)}
 if fmt.Sprint(sinceQueries)!="[ baseline baseline]"{t.Fatalf("sync queries: %v",sinceQueries)}
}

func TestInboundPolicyRejectsSelfEditsMediaAndUnmentionedRooms(t *testing.T) {
 c:=&Connector{config:Config{UserID:bot},requireMention:true,direct:map[string]bool{}}
 for _,tc:=range []struct{name string;event roomEvent;want bool}{
  {"mention",testEvent("$e","@a:t","hi",true),true},
  {"ambient",testEvent("$e","@a:t","hi",false),false},
  {"body-only mention",testEvent("$e","@a:t",bot,false),false},
  {"self",testEvent("$e",bot,"hi",true),false},
  {"encrypted",roomEvent{Type:"m.room.encrypted",EventID:"$e",Sender:"@a:t",Content:json.RawMessage(`{"ciphertext":"secret"}`)},false},
  {"edit",roomEvent{Type:"m.room.message",EventID:"$e",Sender:"@a:t",Content:json.RawMessage(`{"msgtype":"m.text","body":"edited","m.relates_to":{"rel_type":"m.replace"},"m.mentions":{"user_ids":["@bot:example.test"]}}`)},false},
  {"media",roomEvent{Type:"m.room.message",EventID:"$e",Sender:"@a:t",Content:json.RawMessage(`{"msgtype":"m.image","body":"photo"}`)},false},
 }{t.Run(tc.name,func(t *testing.T){_,ok:=c.inbound("!room:t",tc.event);if ok!=tc.want{t.Fatalf("ok=%v want=%v",ok,tc.want)}})}
 c.direct["!dm:t"]=true
 msg,ok:=c.inbound("!dm:t",testEvent("$dm","@a:t","hello",false))
 if !ok || msg.Source.ChatType!=channel.ChatTypeP2P{t.Fatalf("DM: %+v %v",msg,ok)}
 c.requireMention=false
 if _,ok:=c.inbound("!room:t",testEvent("$e","@a:t","opted in",false));!ok{t.Fatal("require mention false ignored")}
}

func TestEncryptedRoomNeverDispatchesPlaintextOrSendsDowngradedReply(t *testing.T) {
 ctx,cancel:=context.WithCancel(context.Background());defer cancel()
 syncs:=0;sent:=false
 s:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  switch {
  case strings.HasSuffix(r.URL.Path,"whoami"):whoami(w)
  case strings.HasSuffix(r.URL.Path,"/state/m.room.encryption"):writeJSON(w,map[string]string{"algorithm":"m.megolm.v1.aes-sha2"})
  case strings.Contains(r.URL.Path,"/send/"):sent=true;writeJSON(w,map[string]string{"event_id":"$sent"})
  default:
   syncs++
   b:=batch(fmt.Sprint(syncs),testEvent("$plaintext","@a:t","unsafe downgrade",true))
   if syncs==1 { room:=b.Rooms.Join["!room:example.test"];room.State.Events=[]roomEvent{{Type:"m.room.encryption"}};b.Rooms.Join["!room:example.test"]=room }
   if syncs==2 {cancel()}
   writeJSON(w,b)
  }
 }));defer s.Close()
 c:=newTestConnector(t,s,func(context.Context,channel.InboundMessage)error{t.Error("encrypted room dispatched");return nil})
 if err:=c.Connect(ctx);err!=nil{t.Fatal(err)}
 if _,err:=c.Send(context.Background(),channel.OutboundMessage{ChatID:"!room:example.test",Text:"reply"});err==nil{t.Fatal("encrypted send accepted")}
 if sent{t.Fatal("plaintext posted to encrypted room")}
}

func TestSend429ReusesTransactionAndEditUsesReplacement(t *testing.T) {
 var paths []string;var content map[string]any
 s:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if strings.HasSuffix(r.URL.Path,"/state/m.room.encryption"){w.WriteHeader(404);writeJSON(w,map[string]string{"errcode":"M_NOT_FOUND"});return}
  paths=append(paths,r.URL.EscapedPath())
  if len(paths)==1{w.Header().Set("Retry-After","0");w.WriteHeader(429);writeJSON(w,map[string]any{"errcode":"M_LIMIT_EXCEEDED","retry_after_ms":120000});return}
  _=json.NewDecoder(r.Body).Decode(&content)
  writeJSON(w,map[string]string{"event_id":"$sent"})
 }));defer s.Close()
 c:=newTestConnector(t,s,nil)
 result,err:=c.Send(context.Background(),channel.OutboundMessage{ChatID:"!room:example.test",Text:"hello"})
 if err!=nil || result.MessageID!="$sent"{t.Fatalf("send=%+v err=%v",result,err)}
 if len(paths)!=2 || paths[0]!=paths[1]{t.Fatalf("retry changed transaction: %v",paths)}
 if content["body"]!="hello" || content["msgtype"]!="m.notice"{t.Fatalf("content=%v",content)}
 _,err=c.Edit(context.Background(),"!room:example.test","$original","updated")
 if err!=nil{t.Fatal(err)}
 relation:=content["m.relates_to"].(map[string]any)
 if relation["rel_type"]!="m.replace" || relation["event_id"]!="$original" || content["m.new_content"].(map[string]any)["body"]!="updated"{t.Fatalf("edit=%v",content)}
}

func TestDisconnectCancelsSyncAndIdentityMismatchStopsBeforeSync(t *testing.T) {
 var connector *Connector;var once sync.Once
 s:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if strings.HasSuffix(r.URL.Path,"whoami"){whoami(w);return}
  once.Do(func(){go connector.Disconnect(context.Background())})
  <-r.Context().Done()
 }));defer s.Close()
 connector=newTestConnector(t,s,func(context.Context,channel.InboundMessage)error{return nil})
 if err:=connector.Connect(context.Background());err!=nil{t.Fatal(err)}
 if err:=connector.Disconnect(context.Background());err!=nil{t.Fatal(err)}
 bad:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if !strings.HasSuffix(r.URL.Path,"whoami"){t.Error("sync reached on wrong bot identity")}
  writeJSON(w,map[string]string{"user_id":"@other:test","device_id":"OTHER"})
 }));defer bad.Close()
 c:=newTestConnector(t,bad,func(context.Context,channel.InboundMessage)error{return nil})
 if err:=c.Connect(context.Background());err==nil{t.Fatal("identity mismatch accepted")}
}

func TestFactoryRequiresEncryptedTokenAndFailsClosedOnRedirect(t *testing.T) {
 var reached bool
 target:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){reached=true;whoami(w)}));defer target.Close()
 redirect:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){http.Redirect(w,r,target.URL,307)}));defer redirect.Close()
 cfg:=Config{Homeserver:redirect.URL,UserID:bot,DeviceID:"DEVICE",TokenEncrypted:base64.StdEncoding.EncodeToString([]byte("ciphertext"))}
 raw,_:=json.Marshal(cfg)
 if _,err:=Factory(Dependencies{})(channel.Config{Raw:raw});err==nil{t.Fatal("missing decrypter accepted")}
 adapted,err:=Factory(Dependencies{Decrypt:func(b []byte)([]byte,error){if string(b)!="ciphertext"{t.Error("wrong ciphertext")};return []byte("secret-token"),nil},HTTPClient:redirect.Client(),CursorStore:func(channel.Config)CursorStore{return &memoryCursor{}}})(channel.Config{Raw:raw,Handler:func(context.Context,channel.InboundMessage)error{return nil}})
 if err!=nil{t.Fatal(err)}
 if err:=adapted.Connect(context.Background());err==nil{t.Fatal("redirect accepted")}
 if reached{t.Fatal("redirect forwarded access token")}
 if adapted.Capabilities().Has(channel.CapAttachment|channel.CapThreadReply){t.Fatal("unsupported capabilities advertised")}
}

func TestTypingAndCanceledRateLimit(t *testing.T) {
 var typing map[string]any
 s:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if !strings.Contains(r.URL.EscapedPath(),"typing/%40bot:example.test"){t.Errorf("typing path=%s",r.URL.EscapedPath())}
  _=json.NewDecoder(r.Body).Decode(&typing)
  writeJSON(w,map[string]any{})
 }));defer s.Close()
 c:=newTestConnector(t,s,nil)
 if err:=c.SetTyping(context.Background(),"!r:t",true);err!=nil{t.Fatal(err)}
 if typing["typing"]!=true || typing["timeout"]!=float64(30000){t.Fatalf("typing=%v",typing)}
 ctx,cancel:=context.WithCancel(context.Background());cancel()
 if err:=waitContext(ctx,time.Hour);!errors.Is(err,context.Canceled){t.Fatalf("wait=%v",err)}
}

type memoryCursor struct { token string; fail bool }
func (s *memoryCursor) Load(context.Context)(string,error){return s.token,nil}
func (s *memoryCursor) Save(_ context.Context,token string)error{if s.fail{return errors.New("store unavailable")};s.token=token;return nil}

func TestDurableCursorLoadsAcrossFactoryRebuildAndBackfillsLimitedTimeline(t *testing.T) {
 ctx,cancel:=context.WithCancel(context.Background());defer cancel()
 store:=&memoryCursor{token:"persisted"}
 var delivered []string
 s:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  switch {
  case strings.HasSuffix(r.URL.Path,"whoami"):whoami(w)
  case strings.HasSuffix(r.URL.Path,"/messages"):
   if r.URL.Query().Get("from")!="persisted" || r.URL.Query().Get("to")!="boundary" || r.URL.Query().Get("dir")!="f"{t.Errorf("backfill query=%v",r.URL.Query())}
   writeJSON(w,map[string]any{"end":"boundary","chunk":[]roomEvent{testEvent("$gap","@a:t","missed",true)}})
  default:
   if r.URL.Query().Get("since")!="persisted"{t.Errorf("cursor not loaded: %v",r.URL.Query())}
   b:=batch("after",testEvent("$tail","@a:t","latest",true));room:=b.Rooms.Join["!room:example.test"];room.Timeline.Limited=true;room.Timeline.PrevBatch="boundary";b.Rooms.Join["!room:example.test"]=room;writeJSON(w,b)
  }
 }));defer s.Close()
 c:=newTestConnector(t,s,func(_ context.Context,msg channel.InboundMessage)error{delivered=append(delivered,msg.MessageID);if msg.MessageID=="$tail"{cancel()};return nil})
 c.cursors=store
 if err:=c.Connect(ctx);err!=nil{t.Fatal(err)}
 if fmt.Sprint(delivered)!="[$gap $tail]" || store.token!="after"{t.Fatalf("delivered=%v persisted=%q",delivered,store.token)}
}

func TestCursorSaveFailureAndGapFailureDoNotAdvance(t *testing.T) {
 for _,gap:=range []bool{false,true}{t.Run(fmt.Sprint(gap),func(t *testing.T){
  store:=&memoryCursor{token:"persisted",fail:!gap}
  s:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
   if strings.HasSuffix(r.URL.Path,"whoami"){whoami(w);return}
   b:=batch("after",testEvent("$tail","@a:t","latest",true))
   if gap{room:=b.Rooms.Join["!room:example.test"];room.Timeline.Limited=true;b.Rooms.Join["!room:example.test"]=room}
   writeJSON(w,b)
  }));defer s.Close()
  delivered:=0
  c:=newTestConnector(t,s,func(context.Context,channel.InboundMessage)error{delivered++;return nil});c.cursors=store
  if err:=c.Connect(context.Background());err==nil{t.Fatal("failure swallowed")}
  if c.since!="persisted" || store.token!="persisted"{t.Fatal("advanced failed cursor")}
  if gap && delivered!=0{t.Fatal("processed only limited tail")}
 })}
}
