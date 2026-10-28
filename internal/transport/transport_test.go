package transport

import (
	"net/http"
	"testing"
)

func TestProxy(t *testing.T) {
	t.Parallel()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}

	if p, err := Proxy(ProxyDirect); err != nil || p != nil {
		t.Errorf("Proxy(direct) set = %v, %v; want no proxy", p != nil, err)
	}

	if p, err := Proxy(ProxyEnv); err != nil || p == nil {
		t.Errorf("Proxy(env) set = %v, %v; want the environment's", p != nil, err)
	}

	p, err := Proxy("http://127.0.0.1:7890")
	if err != nil {
		t.Fatal(err)
	}

	if u, err := p(req); err != nil || u.Host != "127.0.0.1:7890" {
		t.Errorf("Proxy(url) picks %v, %v", u, err)
	}

	for _, bad := range []string{"", "not a url", "127.0.0.1:7890"} {
		if _, err := Proxy(bad); err == nil {
			t.Errorf("Proxy(%q) accepted", bad)
		}
	}
}
