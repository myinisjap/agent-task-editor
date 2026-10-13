package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
)

var ok = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })

func do(h http.Handler, method, target, host string, hdr map[string]string, body string) *httptest.ResponseRecorder {
	var rd *strings.Reader
	req := httptest.NewRequest(method, target, nil)
	if body != "" {
		rd = strings.NewReader(body)
		req = httptest.NewRequest(method, target, rd)
	}
	if host != "" {
		req.Host = host
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestRequireTrustedOrigin(t *testing.T) {
	const cors = "http://localhost:5173,https://ate.example.com"
	h := middleware.RequireTrustedOrigin(cors)(ok)
	cases := []struct {
		name, method, host, origin string
		want                       int
	}{
		{"no origin passes", "POST", "localhost:8080", "", 204},
		{"allowed origin", "POST", "localhost:8080", "http://localhost:5173", 204},
		{"allowed origin case-insensitive", "POST", "localhost:8080", "HTTP://LOCALHOST:5173", 204},
		{"same origin explicit port", "PUT", "myhost:9000", "http://myhost:9000", 204},
		{"same origin default port http", "PATCH", "myhost", "http://myhost", 204},
		{"same origin default port https", "DELETE", "myhost", "https://myhost:443", 204},
		{"same host different port", "POST", "myhost:9000", "http://myhost:9001", 403},
		{"evil origin", "POST", "localhost:8080", "https://evil.example", 403},
		{"rebinding-style same origin passes (host check covers it)", "POST", "evil.example:8080", "http://evil.example:8080", 204},
		{"null origin", "POST", "localhost:8080", "null", 403},
		{"garbage origin", "POST", "localhost:8080", "::::", 403},
		{"safe method GET not checked", "GET", "localhost:8080", "https://evil.example", 204},
		{"preflight OPTIONS not checked", "OPTIONS", "localhost:8080", "https://evil.example", 204},
		{"X-Forwarded-Host is not trusted", "POST", "localhost:8080", "https://fwd.example", 403},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hdr := map[string]string{}
			if c.origin != "" {
				hdr["Origin"] = c.origin
			}
			hdr["X-Forwarded-Host"] = "fwd.example"
			w := do(h, c.method, "/api/v1/tasks", c.host, hdr, "")
			if w.Code != c.want {
				t.Fatalf("got %d, want %d (%s)", w.Code, c.want, w.Body.String())
			}
			if c.want == 403 && !strings.Contains(w.Body.String(), "CORS_ORIGINS") {
				t.Errorf("error should mention CORS_ORIGINS: %s", w.Body.String())
			}
		})
	}
}

func TestRequireTrustedOrigin_Wildcard(t *testing.T) {
	h := middleware.RequireTrustedOrigin("*")(ok)
	w := do(h, "POST", "/x", "localhost:8080", map[string]string{"Origin": "https://evil.example"}, "")
	if w.Code != 204 {
		t.Fatalf("wildcard must stay permissive, got %d", w.Code)
	}
}

func TestRequireJSON(t *testing.T) {
	h := middleware.RequireJSON(ok)
	cases := []struct {
		name, method, path, ct, body string
		want                         int
	}{
		{"json", "POST", "/api/v1/repos", "application/json", `{}`, 204},
		{"json charset", "POST", "/api/v1/repos", "application/json; charset=utf-8", `{}`, 204},
		{"json uppercase", "PUT", "/api/v1/repos/1", "Application/JSON", `{}`, 204},
		{"text/plain blocked", "POST", "/api/v1/repos", "text/plain", `{}`, 415},
		{"text/plain charset blocked", "POST", "/api/v1/repos", "text/plain;charset=UTF-8", `{}`, 415},
		{"form blocked", "PATCH", "/api/v1/repos/1", "application/x-www-form-urlencoded", `a=b`, 415},
		{"missing content-type blocked", "POST", "/api/v1/repos", "", `{}`, 415},
		{"empty body passes without type", "POST", "/api/v1/tasks/1/rerun", "", "", 204},
		{"GET untouched", "GET", "/api/v1/repos", "text/plain", "", 204},
		{"DELETE untouched", "DELETE", "/api/v1/repos/1", "text/plain", "x", 204},
		{"multipart task create exempt", "POST", "/api/v1/tasks", "multipart/form-data; boundary=x", `--x--`, 204},
		{"multipart elsewhere blocked", "POST", "/api/v1/repos", "multipart/form-data; boundary=x", `--x--`, 415},
		{"text/plain task create blocked", "POST", "/api/v1/tasks", "text/plain", `{}`, 415},
		{"yaml import exempt", "POST", "/api/v1/workflows/import", "application/yaml", `a: 1`, 204},
		{"yaml update exempt", "PUT", "/api/v1/workflows/abc/yaml", "application/yaml", `a: 1`, 204},
		{"text/plain yaml import blocked", "POST", "/api/v1/workflows/import", "text/plain", `a: 1`, 415},
		{"yaml type elsewhere blocked", "POST", "/api/v1/repos", "application/yaml", `a: 1`, 415},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hdr := map[string]string{}
			if c.ct != "" {
				hdr["Content-Type"] = c.ct
			}
			w := do(h, c.method, c.path, "", hdr, c.body)
			if w.Code != c.want {
				t.Fatalf("got %d, want %d", w.Code, c.want)
			}
		})
	}
}

func TestHostPolicy(t *testing.T) {
	const cors = "http://localhost:5173,https://ate.example.com"
	h := middleware.NewHostPolicy("", cors, false).Middleware()(ok)
	for host, want := range map[string]int{
		"localhost:8080":     204,
		"localhost":          204,
		"127.0.0.1:8080":     204,
		"[::1]:8080":         204,
		"backend:8080":       204,
		"ate.example.com":    204, // hostname from CORS_ORIGINS
		"192.168.1.5:5173":   204, // IP literals are never rebinding
		"evil.example:8080":  403,
		"evil.example":       403,
		"localhost.evil.com": 403,
	} {
		w := do(h, "GET", "/healthz", host, nil, "")
		if w.Code != want {
			t.Errorf("host %q: got %d, want %d", host, w.Code, want)
		}
		if want == 403 && !strings.Contains(w.Body.String(), "ALLOWED_HOSTS") {
			t.Errorf("host %q: error should name ALLOWED_HOSTS: %s", host, w.Body.String())
		}
	}
	// Preflight OPTIONS is covered by the Host check too (it is a Host problem).
	if w := do(h, "OPTIONS", "/api/v1/tasks", "evil.example", nil, ""); w.Code != 403 {
		t.Errorf("OPTIONS with bad host: got %d", w.Code)
	}
}

func TestHostPolicy_AllowedHostsEnv(t *testing.T) {
	p := middleware.NewHostPolicy("ate.lan, other.example:9000", "http://localhost:5173", false)
	h := p.Middleware()(ok)
	for host, want := range map[string]int{
		"ate.lan:8080":       204,
		"other.example:9000": 204,
		"other.example:9001": 403,
		"other.example":      403,
		"evil.example":       403,
	} {
		if w := do(h, "GET", "/x", host, nil, ""); w.Code != want {
			t.Errorf("host %q: got %d, want %d", host, w.Code, want)
		}
	}
}

func TestHostPolicy_WildcardDisables(t *testing.T) {
	p := middleware.NewHostPolicy("*", "", false)
	if p.Enforced {
		t.Fatal("ALLOWED_HOSTS=* must disable enforcement")
	}
	if w := do(p.Middleware()(ok), "GET", "/x", "evil.example", nil, ""); w.Code != 204 {
		t.Fatalf("got %d", w.Code)
	}
}

func TestHostPolicy_NotEnforcedWhenAuthEnabled(t *testing.T) {
	p := middleware.NewHostPolicy("", "http://localhost:5173", true)
	if p.Enforced {
		t.Fatal("Host check must be off when a token is configured")
	}
	if w := do(p.Middleware()(ok), "GET", "/x", "ate.public.example", nil, ""); w.Code != 204 {
		t.Fatalf("got %d", w.Code)
	}
}
