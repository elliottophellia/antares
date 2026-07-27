package gateway

import (
	"testing"

	"github.com/enowdev/antares/internal/config"
)

func TestParseSignalReceive(t *testing.T) {
	data := []byte(`[
	  {"envelope":{"source":"+15551234","dataMessage":{"message":"hi there"}}},
	  {"envelope":{"source":"+15559999","dataMessage":{"message":"grp","groupInfo":{"groupId":"G1"}}}},
	  {"envelope":{"source":"+15550000","dataMessage":null}}
	]`)
	msgs := parseSignalReceive(data)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 text messages, got %d", len(msgs))
	}
	if msgs[0].Body != "hi there" || msgs[1].Group != "G1" {
		t.Fatalf("bad parse: %+v", msgs)
	}
}

func TestParseWhatsAppWebhook(t *testing.T) {
	data := []byte(`{"entry":[{"changes":[{"value":{"messages":[
	  {"from":"15551234","type":"text","text":{"body":"hello"}},
	  {"from":"15551234","type":"image"}
	]}}]}]}`)
	msgs := parseWhatsAppWebhook(data)
	if len(msgs) != 1 || msgs[0].From != "15551234" || msgs[0].Body != "hello" {
		t.Fatalf("bad parse: %+v", msgs)
	}
}

func TestParseFeishuEvent(t *testing.T) {
	data := []byte(`{"header":{"event_type":"im.message.receive_v1"},"event":{
	  "sender":{"sender_id":{"open_id":"ou_1"}},
	  "message":{"chat_id":"oc_1","message_type":"text","content":"{\"text\":\"hi lark\"}"}
	}}`)
	m, ok := parseFeishuEvent(data)
	if !ok || m.ChatID != "oc_1" || m.Sender != "ou_1" || m.Body != "hi lark" {
		t.Fatalf("bad parse: %+v ok=%v", m, ok)
	}
	// A non-message event is ignored.
	if _, ok := parseFeishuEvent([]byte(`{"header":{"event_type":"other"}}`)); ok {
		t.Fatal("non-message event should be ignored")
	}
}

func TestNewGatewaysDefaults(t *testing.T) {
	if NewSignal(config.Signal{APIURL: "http://x:8080/"}, nil).base != "http://x:8080" {
		t.Fatal("signal base not trimmed")
	}
	w := NewWhatsApp(config.WhatsApp{}, nil)
	if w.cfg.Path != "/webhook" || w.cfg.ListenAddr != ":8090" {
		t.Fatal("whatsapp defaults wrong")
	}
	f := NewFeishu(config.Feishu{}, nil)
	if f.cfg.Path != "/webhook" || f.cfg.ListenAddr != ":8091" {
		t.Fatal("feishu defaults wrong")
	}
}
