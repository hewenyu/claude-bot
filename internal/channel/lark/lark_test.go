package lark

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"github.com/hewenyu/claude-bot/internal/channel"
)

const botOpenID = "ou_bot_open_id"

func loadEvent(t *testing.T, name string) *larkim.P2MessageReceiveV1 {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}

	var ev larkim.P2MessageReceiveV1
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatalf("unmarshal %s: %v", name, err)
	}

	return &ev
}

func TestFromEvent_groupMentioningBot(t *testing.T) {
	t.Parallel()

	got, ok := fromEvent(loadEvent(t, "group_at_bot.json"), botOpenID)
	if !ok {
		t.Fatal("group message that @s the bot was dropped")
	}

	want := channel.Message{
		ID:        "lark:om_5ce6d572455d361153b7cb51da133945",
		Channel:   "lark",
		ChatID:    "oc_5ce6d572455d361153b7xx51da133945",
		ChatType:  "group",
		ThreadID:  "omt_d4be107c616",
		ReplyTo:   "lark:om_5ce6d572455d361153b7cb5xxfsdfsdfdsf",
		Sender:    channel.User{ID: "ou_84aad35d084aa403a838cf73ee18467", Mention: `<at user_id="ou_84aad35d084aa403a838cf73ee18467"></at>`},
		Text:      "登录页报 500 了，@张三 也遇到了\n复现：点两次登录",
		Mentions:  []channel.User{{ID: "ou_zhangsan", Name: "张三", Mention: `<at user_id="ou_zhangsan"></at>`}},
		CreatedAt: time.Date(2020, 12, 27, 12, 45, 51, 345e6, time.UTC),
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("fromEvent()\n got  %+v\n want %+v", got, want)
	}
}

func TestFromEvent_filters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		fixture string
		botID   string
		want    bool
	}{
		{name: "group chatter without mention is ignored", fixture: "group_no_mention.json", botID: botOpenID, want: false},
		{name: "messages from other bots are ignored", fixture: "from_bot.json", botID: botOpenID, want: false},
		{name: "direct message is delivered", fixture: "p2p_post.json", botID: botOpenID, want: true},
		{name: "mentioning someone else is not mentioning the bot", fixture: "group_at_bot.json", botID: "ou_another_bot", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, ok := fromEvent(loadEvent(t, tt.fixture), tt.botID); ok != tt.want {
				t.Errorf("delivered = %v, want %v", ok, tt.want)
			}
		})
	}
}

func TestFromEvent_postWithImageAndCode(t *testing.T) {
	t.Parallel()

	got, ok := fromEvent(loadEvent(t, "p2p_post.json"), botOpenID)
	if !ok {
		t.Fatal("dropped")
	}

	wantText := "崩溃日志\n启动就崩，见 issue (https://github.com/o/r/issues/1)\n\n```GO\npanic: nil map\n```\n\n[image]"
	if got.Text != wantText {
		t.Errorf("text =\n%q\nwant\n%q", got.Text, wantText)
	}

	wantAtts := []channel.Attachment{{Kind: channel.AttachmentImage, Key: "img_v3_abc"}}
	if !reflect.DeepEqual(got.Attachments, wantAtts) {
		t.Errorf("attachments = %+v, want %+v", got.Attachments, wantAtts)
	}

	if got.ChatType != channel.ChatDirect {
		t.Errorf("chat type = %q", got.ChatType)
	}
}

func TestParseContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, msgType, content, text string
		atts                         []channel.Attachment
	}{
		{
			name: "image", msgType: "image", content: `{"image_key":"img_1"}`, text: "[image]",
			atts: []channel.Attachment{{Kind: channel.AttachmentImage, Key: "img_1"}},
		},
		{
			name: "file", msgType: "file", content: `{"file_key":"file_1","file_name":"crash.log"}`, text: "[file]",
			atts: []channel.Attachment{{Kind: channel.AttachmentFile, Key: "file_1", Name: "crash.log"}},
		},
		{name: "locale wrapped post", msgType: "post", content: `{"zh_cn":{"title":"","content":[[{"tag":"text","text":"hello"}]]}}`, text: "hello"},
		{name: "unknown type", msgType: "sticker", content: `{}`, text: "[sticker]"},
		{name: "malformed text falls back to raw", msgType: "text", content: `not json`, text: "not json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			text, atts := parseContent(tt.msgType, tt.content, nil)
			if text != tt.text || !reflect.DeepEqual(atts, tt.atts) {
				t.Errorf("parseContent() = %q %+v, want %q %+v", text, atts, tt.text, tt.atts)
			}
		})
	}
}

func TestBuildPost(t *testing.T) {
	t.Parallel()

	got, err := buildPost("**fixed** in `abc123`", "ou_x")
	if err != nil {
		t.Fatal(err)
	}

	// The md tag must sit alone in its paragraph (Lark rejects mixing it with other tags).
	want := `{"zh_cn":{"content":[[{"tag":"at","user_id":"ou_x"}],[{"tag":"md","text":"**fixed** in ` + "`abc123`" + `"}]]}}`
	if got != want {
		t.Errorf("buildPost()\n got  %s\n want %s", got, want)
	}

	noMention, err := buildPost("hi", "")
	if err != nil {
		t.Fatal(err)
	}

	if want := `{"zh_cn":{"content":[[{"tag":"md","text":"hi"}]]}}`; noMention != want {
		t.Errorf("buildPost() without mention = %s", noMention)
	}
}

func TestBuildText(t *testing.T) {
	t.Parallel()

	got, err := buildText("done", "ou_x")
	if err != nil {
		t.Fatal(err)
	}

	if want := `{"text":"<at user_id=\"ou_x\"></at> done"}`; got != want {
		t.Errorf("buildText() = %s, want %s", got, want)
	}
}

func TestFromItem(t *testing.T) {
	t.Parallel()

	raw := `{"message_id":"om_parent","msg_type":"text","create_time":"1609073151345","chat_id":"oc_1",
		"sender":{"id":"ou_wang","id_type":"open_id","sender_type":"user","sender_name":"王五"},
		"body":{"content":"{\"text\":\"@_user_1 部署失败\"}"},
		"mentions":[{"key":"@_user_1","id":"ou_bot_open_id","id_type":"open_id","name":"ProjectBot"}]}`

	var it larkim.Message
	if err := json.Unmarshal([]byte(raw), &it); err != nil {
		t.Fatal(err)
	}

	got := fromItem(&it, botOpenID)
	if got.ID != "lark:om_parent" || got.Text != "部署失败" || got.Sender.Name != "王五" || got.Sender.ID != "ou_wang" {
		t.Errorf("fromItem() = %+v", got)
	}
}

func TestFileName(t *testing.T) {
	t.Parallel()

	png := []byte("\x89PNG\r\n\x1a\n0000")

	tests := []struct {
		name   string
		att    channel.Attachment
		served string
		want   string
	}{
		{name: "attachment name wins", att: channel.Attachment{Key: "k", Name: "crash.log"}, want: "crash.log"},
		{name: "path traversal stripped", att: channel.Attachment{Key: "k", Name: "../../etc/passwd"}, want: "passwd.png"},
		{name: "key plus sniffed extension", att: channel.Attachment{Key: "img_v3_1"}, want: "img_v3_1.png"},
		{name: "served name", att: channel.Attachment{Key: "k"}, served: "shot.jpeg", want: "shot.jpeg"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := fileName(tt.att, tt.served, png); got != tt.want {
				t.Errorf("fileName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConfig_ProxyFunc(t *testing.T) {
	t.Parallel()

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, FeishuURL, nil)

	tests := []struct {
		name    string
		cfg     Config
		want    string // "" = direct
		wantErr bool
	}{
		{name: "feishu defaults to direct", cfg: Config{Domain: "feishu"}},
		{name: "explicit url", cfg: Config{Proxy: "http://127.0.0.1:7897"}, want: "http://127.0.0.1:7897"},
		{name: "explicit direct", cfg: Config{Domain: "lark", Proxy: "direct"}},
		{name: "garbage", cfg: Config{Proxy: "not a url"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fn, err := tt.cfg.ProxyFunc()
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error")
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			got := ""

			if fn != nil {
				u, err := fn(req)
				if err != nil {
					t.Fatal(err)
				}

				got = u.String()
			}

			if got != tt.want {
				t.Errorf("proxy = %q, want %q", got, tt.want)
			}
		})
	}
}
