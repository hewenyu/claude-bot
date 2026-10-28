package lark

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Scopes the bot needs: receive DMs and group @-mentions, read and send
// messages, download attachments, react with an acknowledgement emoji.
var Scopes = []string{
	"im:message",
	"im:message.p2p_msg:readonly",
	"im:message.group_at_msg:readonly",
	"im:message:send_as_bot",
	"im:resource",
	"im:message.reactions:write_only",
}

// Device-flow timing (RFC 8628), in seconds where the server sends seconds.
const (
	defaultExpiresIn = 600 // when the server omits expires_in
	defaultInterval  = 5   // when the server omits interval
	slowDownStep     = 5   // added to the interval on every slow_down
	maxPollFailures  = 3   // failed polls in a row that end the registration
)

// Device-flow error codes.
const (
	codePending  = "authorization_pending"
	codeSlowDown = "slow_down"
	codeDenied   = "access_denied"
	codeExpired  = "expired_token"
)

// Events the bot subscribes to.
var Events = []string{"im.message.receive_v1"}

// Registration errors the caller may want to tell apart.
var (
	ErrDenied   = errors.New("authorization was declined")
	ErrExpired  = errors.New("authorization link expired, run init again")
	ErrRefused  = errors.New("registration refused by the platform")
	ErrResponse = errors.New("unexpected registration response")
)

// Registered is the outcome of a successful app registration.
type Registered struct {
	AppID     string
	AppSecret string
	OwnerID   string // open_id of the person who confirmed
	Domain    string // "feishu" or "lark"
}

// Registrar runs the official app registration device flow: the user opens a
// link, logs in and confirms; the app is created with the requested
// permissions and events, and its credentials come back to us.
type Registrar struct {
	HTTP *http.Client
	// Hosts are the accounts endpoints for feishu and lark (overridable in tests).
	FeishuHost, LarkHost string
	// LinkHosts are hosts the authorization link may point to.
	LinkHosts []string
	// Name and Desc label the app in the Lark admin console.
	Name, Desc string
	// AppID requests more permissions for an existing app instead of creating one.
	AppID string
	// OnLink is called once with the URL the user must open.
	OnLink func(link string, expires time.Time)
	// OnStatus reports polling progress ("pending", "slow_down", "lark").
	OnStatus func(status string)
	// IntervalUnit scales the server-provided poll interval (seconds); tests shrink it.
	IntervalUnit time.Duration
}

// NewRegistrar returns a Registrar for the official endpoints. proxy follows
// the same rules as Config.Proxy.
func NewRegistrar(proxy func(*http.Request) (*url.URL, error)) *Registrar {
	return &Registrar{
		HTTP: &http.Client{
			Timeout:   20 * time.Second,
			Transport: newTransport(proxy),
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		IntervalUnit: time.Second,
		FeishuHost:   "accounts.feishu.cn",
		LarkHost:     "accounts.larksuite.com",
		LinkHosts: []string{
			"accounts.feishu.cn", "accounts.larksuite.com", "open.feishu.cn",
			"open.larksuite.com", "passport.feishu.cn", "passport.larksuite.com",
		},
	}
}

type regResponse struct {
	DeviceCode      string `json:"device_code"`
	VerificationURI string `json:"verification_uri_complete"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	ClientID        string `json:"client_id"`
	ClientSecret    string `json:"client_secret"`
	Error           string `json:"error"`
	UserInfo        struct {
		OpenID      string `json:"open_id"`
		TenantBrand string `json:"tenant_brand"`
	} `json:"user_info"`
}

// Register blocks until the user confirms, declines, or the link expires.
func (r *Registrar) Register(ctx context.Context) (Registered, error) {
	begin, err := r.post(ctx, r.FeishuHost, url.Values{
		"action":            {"begin"},
		"archetype":         {"PersonalAgent"},
		"auth_method":       {"client_secret"},
		"request_user_info": {"open_id"},
	})
	if err != nil {
		return Registered{}, err
	}

	if begin.DeviceCode == "" {
		return Registered{}, fmt.Errorf("%w: no device code", ErrResponse)
	}

	link, err := r.link(begin.VerificationURI)
	if err != nil {
		return Registered{}, fmt.Errorf("%w: %w", ErrResponse, err)
	}

	expires := time.Now().Add(time.Duration(positive(begin.ExpiresIn, defaultExpiresIn)) * time.Second)

	if r.OnLink != nil {
		r.OnLink(link, expires)
	}

	pollCtx, cancel := context.WithDeadline(ctx, expires)
	defer cancel()

	reg, err := r.poll(pollCtx, begin)
	if err != nil && pollCtx.Err() != nil {
		if ctx.Err() != nil {
			return Registered{}, fmt.Errorf("registration cancelled: %w", ctx.Err())
		}

		return Registered{}, ErrExpired
	}

	return reg, err
}

// poll waits for the user's decision on the device code from begin.
func (r *Registrar) poll(ctx context.Context, begin regResponse) (Registered, error) {
	host, domain := r.FeishuHost, DomainFeishu
	interval := time.Duration(positive(begin.Interval, defaultInterval)) * r.IntervalUnit
	failures := 0

	for {
		res, err := r.post(ctx, host, url.Values{"action": {"poll"}, "device_code": {begin.DeviceCode}})
		if err != nil {
			// The user may already have confirmed in the browser, so ride out a
			// network blip as if the answer were still pending.
			if failures++; failures == maxPollFailures {
				return Registered{}, err
			}

			res = regResponse{Error: codePending}
		} else {
			failures = 0
		}

		// Lark (international) tenants are served by a different host.
		if res.UserInfo.TenantBrand == DomainLark && domain != DomainLark {
			host, domain = r.LarkHost, DomainLark
			r.status(DomainLark)

			continue
		}

		if res.ClientID != "" && res.ClientSecret != "" {
			return r.registered(res, domain)
		}

		if err := pollError(res.Error); err != nil {
			return Registered{}, err
		}

		status := "pending"
		if res.Error == codeSlowDown {
			status = "slow_down"
			interval += slowDownStep * r.IntervalUnit
		}

		r.status(status)

		select {
		case <-ctx.Done():
			return Registered{}, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// registered checks the app the user authorized against the one requested.
func (r *Registrar) registered(res regResponse, domain string) (Registered, error) {
	if r.AppID != "" && res.ClientID != r.AppID {
		return Registered{}, fmt.Errorf("%w: authorized app %s, expected %s", ErrResponse, res.ClientID, r.AppID)
	}

	return Registered{AppID: res.ClientID, AppSecret: res.ClientSecret, OwnerID: res.UserInfo.OpenID, Domain: domain}, nil
}

// pollError maps a terminal device-flow error; nil means keep waiting.
func pollError(code string) error {
	switch code {
	case "", codePending, codeSlowDown:
		return nil
	case codeDenied:
		return ErrDenied
	case codeExpired:
		return ErrExpired
	default:
		return fmt.Errorf("%w: %s", ErrRefused, code)
	}
}

// link validates the authorization URL and attaches the app's name,
// description and requested permissions/events.
func (r *Registrar) link(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse link: %w", err)
	}

	if u.Scheme != "https" || !slices.Contains(r.LinkHosts, u.Hostname()) {
		return "", fmt.Errorf("link host %q is not an official Lark host", u.Hostname())
	}

	addons, err := encodeAddons()
	if err != nil {
		return "", err
	}

	q := u.Query()
	q.Set("from", "sdk")
	q.Set("tp", "sdk")
	q.Set("source", "go-sdk/claude-bot")
	q.Set("name", r.Name)
	q.Set("desc", r.Desc)
	q.Set("addons", addons)

	if r.AppID != "" {
		q.Set("clientID", r.AppID)
	}

	u.RawQuery = q.Encode()

	return u.String(), nil
}

// encodeAddons packs the permission and event requests as the registration
// page expects them: base64url(gzip(json)).
func encodeAddons() (string, error) {
	raw, err := json.Marshal(map[string]any{
		"scopes": map[string]any{scopeTenant: Scopes},
		"events": map[string]any{"items": map[string]any{scopeTenant: Events}},
	})
	if err != nil {
		return "", fmt.Errorf("encode addons: %w", err)
	}

	var buf bytes.Buffer

	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		return "", fmt.Errorf("compress addons: %w", err)
	}

	if err := zw.Close(); err != nil {
		return "", fmt.Errorf("compress addons: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(buf.Bytes()), nil
}

func (r *Registrar) post(ctx context.Context, host string, form url.Values) (regResponse, error) {
	var res regResponse

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+host+"/oauth/v1/app/registration", strings.NewReader(form.Encode()))
	if err != nil {
		return res, fmt.Errorf("build registration request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := r.HTTP.Do(req)
	if err != nil {
		return res, fmt.Errorf("registration request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body

	// Pending / slow_down come back as HTTP 400 with a JSON body.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		return res, fmt.Errorf("%w: HTTP %d", ErrResponse, resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return res, fmt.Errorf("%w: %w", ErrResponse, err)
	}

	return res, nil
}

func (r *Registrar) status(s string) {
	if r.OnStatus != nil {
		r.OnStatus(s)
	}
}

func positive(v, fallback int) int {
	if v > 0 {
		return v
	}

	return fallback
}
