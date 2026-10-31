package lark

import (
	"cmp"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAccounts scripts the registration endpoint: begin, then one poll reply
// per call; once the script runs out, every poll is pending.
type fakeAccounts struct {
	mu        sync.Mutex
	link      string
	expiresIn int // seconds; 60 when zero
	polls     []map[string]any
	calls     []url.Values
}

func (f *fakeAccounts) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	_ = r.ParseForm()
	f.calls = append(f.calls, r.PostForm)

	if r.URL.Path != "/oauth/v1/app/registration" {
		http.NotFound(w, r)

		return
	}

	var body map[string]any

	switch r.PostForm.Get("action") {
	case "begin":
		body = map[string]any{"device_code": "dc_1", "verification_uri_complete": f.link, "expires_in": cmp.Or(f.expiresIn, 60), "interval": 1}
	case "poll":
		body = map[string]any{"error": "authorization_pending"}
		if len(f.polls) > 0 {
			body, f.polls = f.polls[0], f.polls[1:]
		}

		if status, ok := body["http_status"].(int); ok {
			w.WriteHeader(status)

			return
		}

		if _, pending := body["error"]; pending {
			w.WriteHeader(http.StatusBadRequest) // the real endpoint reports pending as 400
		}
	}

	_ = json.NewEncoder(w).Encode(body)
}

func newTestRegistrar(t *testing.T, f *fakeAccounts) *Registrar {
	t.Helper()

	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)

	host := strings.TrimPrefix(srv.URL, "https://")
	r := NewRegistrar(nil)
	r.http = srv.Client()
	r.feishuHost, r.larkHost = host, host
	r.intervalUnit = time.Millisecond
	r.Name, r.Desc = "demo Bot", "desc"

	if f.link == "" {
		f.link = "https://open.feishu.cn/page/openclaw?user_code=ABCD"
	}

	return r
}

func TestRegistrar_Register(t *testing.T) {
	t.Parallel()

	f := &fakeAccounts{polls: []map[string]any{
		{"error": "authorization_pending"},
		{"error": "slow_down"},
		{"client_id": "cli_new", "client_secret": "sec", "user_info": map[string]any{"open_id": "ou_owner", "tenant_brand": "feishu"}},
	}}
	r := newTestRegistrar(t, f)

	var link string

	r.OnLink = func(l string, _ time.Time) { link = l }

	got, err := r.Register(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	want := Registered{AppID: "cli_new", AppSecret: "sec", Domain: "feishu"}
	if got != want {
		t.Errorf("Register() = %+v, want %+v", got, want)
	}

	if polls := len(f.calls) - 1; polls != 3 {
		t.Errorf("polled %d times, want 3 (pending and slow_down keep waiting)", polls)
	}

	begin := f.calls[0]
	if begin.Get("archetype") != "PersonalAgent" || begin.Get("auth_method") != "client_secret" || begin.Get("request_user_info") != "open_id" {
		t.Errorf("begin form = %v", begin)
	}

	checkLink(t, link)
}

func checkLink(t *testing.T, link string) {
	t.Helper()

	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}

	q := u.Query()
	if u.Host != "open.feishu.cn" || q.Get("user_code") != "ABCD" || q.Get("name") != "demo Bot" || q.Get("from") != "sdk" {
		t.Fatalf("link = %s", link)
	}

	raw, err := base64.RawURLEncoding.DecodeString(q.Get("addons"))
	if err != nil {
		t.Fatalf("addons not base64url: %v", err)
	}

	zr, err := gzip.NewReader(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("addons not gzip: %v", err)
	}

	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}

	var addons struct {
		Scopes struct {
			Tenant []string `json:"tenant"`
		} `json:"scopes"`
		Events struct {
			Items struct {
				Tenant []string `json:"tenant"`
			} `json:"items"`
		} `json:"events"`
	}

	if err := json.Unmarshal(plain, &addons); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(addons.Scopes.Tenant, scopes) || !reflect.DeepEqual(addons.Events.Items.Tenant, events) {
		t.Errorf("addons = %s", plain)
	}
}

func TestRegistrar_Register_failures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		polls []map[string]any
		appID string
		link  string
		want  error
	}{
		{name: "declined", polls: []map[string]any{{"error": "access_denied"}}, want: ErrDenied},
		{name: "expired", polls: []map[string]any{{"error": "expired_token"}}, want: ErrExpired},
		{name: "unknown error", polls: []map[string]any{{"error": "invalid_client"}}, want: ErrRefused},
		{
			name:  "permission upgrade answered by another app",
			appID: "cli_mine",
			polls: []map[string]any{{"client_id": "cli_other", "client_secret": "s"}},
			want:  ErrResponse,
		},
		{name: "phishing link host", link: "https://evil.example.com/authorize", want: ErrResponse},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newTestRegistrar(t, &fakeAccounts{polls: tt.polls, link: tt.link})
			r.AppID = tt.appID

			if _, err := r.Register(t.Context()); !errors.Is(err, tt.want) {
				t.Errorf("Register() err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestRegistrar_Register_larkTenant(t *testing.T) {
	t.Parallel()

	f := &fakeAccounts{polls: []map[string]any{
		{"error": "authorization_pending", "user_info": map[string]any{"tenant_brand": "lark"}},
		{"client_id": "cli_intl", "client_secret": "s", "user_info": map[string]any{"tenant_brand": "lark"}},
	}}
	r := newTestRegistrar(t, f)

	var switched string

	r.OnTenantSwitch = func(domain string) { switched = domain }

	got, err := r.Register(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if got.Domain != DomainLark || switched != DomainLark {
		t.Errorf("Register() = %+v, switched to %q; want lark domain", got, switched)
	}
}

func TestRegistrar_Register_upgradeKeepsClientID(t *testing.T) {
	t.Parallel()

	f := &fakeAccounts{polls: []map[string]any{{"client_id": "cli_mine", "client_secret": "s"}}}
	r := newTestRegistrar(t, f)
	r.AppID = "cli_mine"

	var link string

	r.OnLink = func(l string, _ time.Time) { link = l }

	if _, err := r.Register(t.Context()); err != nil {
		t.Fatal(err)
	}

	if u, _ := url.Parse(link); u.Query().Get("clientID") != "cli_mine" {
		t.Errorf("upgrade link lacks clientID: %s", link)
	}
}

func TestRegistrar_Register_ridesOutPollBlip(t *testing.T) {
	t.Parallel()

	f := &fakeAccounts{polls: []map[string]any{
		{"http_status": http.StatusBadGateway},
		{"client_id": "cli_new", "client_secret": "sec"},
	}}
	r := newTestRegistrar(t, f)

	if got, err := r.Register(t.Context()); err != nil || got.AppID != "cli_new" {
		t.Errorf("Register() = %+v, %v; want success after one failed poll", got, err)
	}
}

func TestRegistrar_Register_cancelled(t *testing.T) {
	t.Parallel()

	f := &fakeAccounts{polls: []map[string]any{{"error": "authorization_pending"}, {"error": "authorization_pending"}}}
	r := newTestRegistrar(t, f)
	r.intervalUnit = time.Second // a missed cancellation would show as a slow test

	ctx, cancel := context.WithCancel(t.Context())
	r.OnLink = func(string, time.Time) { cancel() }

	_, err := r.Register(ctx)
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrExpired) {
		t.Errorf("Register() after cancel err = %v, want context.Canceled (not ErrExpired)", err)
	}
}

// An app confirmed from a copy of the link without its user_code is never
// reported to the device code: the poll stays pending until the link
// expires. The wait must not be silent meanwhile.
func TestRegistrar_Register_neverConfirmed(t *testing.T) {
	t.Parallel()

	r := newTestRegistrar(t, &fakeAccounts{expiresIn: 1})
	r.waitNotice = 200 * time.Millisecond

	var (
		mu    sync.Mutex
		lefts []time.Duration
	)

	r.OnWaiting = func(left time.Duration) {
		mu.Lock()
		defer mu.Unlock()

		lefts = append(lefts, left)
	}

	start := time.Now()

	_, err := r.Register(t.Context())
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("Register() err = %v, want ErrExpired", err)
	}

	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("Register() took %v, want it to end when the link expires", took)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(lefts) < 2 || len(lefts) > 6 {
		t.Fatalf("OnWaiting called %d times over 1s every 200ms: %v", len(lefts), lefts)
	}

	for i, left := range lefts {
		if left <= 0 || left > time.Second || i > 0 && left >= lefts[i-1] {
			t.Errorf("OnWaiting left = %v, want a countdown within the link's second", lefts)

			break
		}
	}
}

func TestRegistrar_Register_noWaitingNoticeWhenAnswered(t *testing.T) {
	t.Parallel()

	f := &fakeAccounts{polls: []map[string]any{{"client_id": "cli_new", "client_secret": "sec"}}}
	r := newTestRegistrar(t, f)
	r.OnWaiting = func(left time.Duration) { t.Errorf("OnWaiting(%v) on an immediate answer", left) }

	if _, err := r.Register(t.Context()); err != nil {
		t.Fatal(err)
	}
}
