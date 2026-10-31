package lark

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"

	"github.com/hewenyu/claude-bot/internal/channel"
)

// fakeContact serves the tenant token and the contact user API.
type fakeContact struct {
	mu    sync.Mutex
	users []string // paths asked for, with query
	auth  []string // Authorization headers of user lookups
}

func (f *fakeContact) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.URL.Path {
	case "/open-apis/auth/v3/tenant_access_token/internal":
		_, _ = io.WriteString(w, `{"code":0,"msg":"ok","tenant_access_token":"t-test","expire":7200}`)
	case "/open-apis/contact/v3/users/ou_ann":
		f.record(r)

		_, _ = io.WriteString(w, `{"code":0,"msg":"success","data":{"user":{"open_id":"ou_ann","name":"张三","en_name":"Ann"}}}`)
	case "/open-apis/contact/v3/users/ou_noname": // name field not granted
		f.record(r)

		_, _ = io.WriteString(w, `{"code":0,"msg":"success","data":{"user":{"open_id":"ou_noname"}}}`)
	case "/open-apis/contact/v3/users/ou_en":
		f.record(r)

		_, _ = io.WriteString(w, `{"code":0,"msg":"success","data":{"user":{"open_id":"ou_en","en_name":"Bob"}}}`)
	default: // outside the contact range, or no contact permission
		f.record(r)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"code":41050,"msg":"no user authority error"}`)
	}
}

func (f *fakeContact) record(r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.users = append(f.users, r.URL.RequestURI())
	f.auth = append(f.auth, r.Header.Get("Authorization"))
}

func newContactChannel(t *testing.T) (*fakeContact, *Channel) {
	t.Helper()

	f := &fakeContact{}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	c := &Channel{
		client: lark.NewClient("cli_test", "secret",
			lark.WithOpenBaseUrl(srv.URL), lark.WithLogLevel(larkcore.LogLevelError), lark.WithHttpClient(srv.Client())),
		log: slog.New(slog.DiscardHandler),
	}
	c.names.Lookup = c.userName

	return f, c
}

func TestNameSender(t *testing.T) {
	t.Parallel()

	f, c := newContactChannel(t)

	tests := []struct {
		id, want string
	}{
		{id: "ou_ann", want: "张三"},
		{id: "ou_ann", want: "张三"},  // cached
		{id: "ou_en", want: "Bob"},  // no Chinese name: the English one
		{id: "ou_hidden", want: ""}, // lookup refused: the message keeps only the id
		{id: "ou_hidden", want: ""}, // refusal cached
		{id: "ou_noname", want: ""}, // name field not granted
	}

	for _, tt := range tests {
		msg := channel.Message{Sender: channel.User{ID: tt.id, Mention: mentionSyntax(tt.id)}}
		c.nameSender(t.Context(), &msg)

		if msg.Sender.Name != tt.want || msg.Sender.ID != tt.id {
			t.Errorf("sender of %s = %+v, want name %q", tt.id, msg.Sender, tt.want)
		}
	}

	want := []string{
		"/open-apis/contact/v3/users/ou_ann?user_id_type=open_id",
		"/open-apis/contact/v3/users/ou_en?user_id_type=open_id",
		"/open-apis/contact/v3/users/ou_hidden?user_id_type=open_id",
		"/open-apis/contact/v3/users/ou_noname?user_id_type=open_id",
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.users) != len(want) {
		t.Fatalf("user lookups = %v, want %v", f.users, want)
	}

	for i := range want {
		if f.users[i] != want[i] || f.auth[i] != "Bearer t-test" {
			t.Errorf("lookup %d = %s (%s), want %s with the tenant token", i, f.users[i], f.auth[i], want[i])
		}
	}
}

// A name already in the message (from the message API) is kept as is.
func TestNameSender_keepsKnownName(t *testing.T) {
	t.Parallel()

	f, c := newContactChannel(t)

	msg := channel.Message{Sender: channel.User{ID: "ou_ann", Name: "Already"}}
	c.nameSender(t.Context(), &msg)

	if msg.Sender.Name != "Already" || len(f.users) != 0 {
		t.Errorf("sender = %+v after %d lookups", msg.Sender, len(f.users))
	}
}

func TestUserName_withoutNameField(t *testing.T) {
	t.Parallel()

	_, c := newContactChannel(t)

	if name, err := c.userName(t.Context(), "ou_noname"); name != "" || !errors.Is(err, errNoName) {
		t.Errorf("userName() = %q, %v; want errNoName", name, err)
	}
}
