package middleware

import (
	"encoding/json"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// This file holds three small request guards that close the "drive-by
// browser" holes CORS alone leaves open. CORS only decides which *response*
// headers a browser lets a page read; it never stops a request reaching the
// handler. So:
//
//   - RequireTrustedOrigin rejects cross-origin state-changing requests
//     (CSRF) whose Origin isn't allowed.
//   - RequireJSON rejects non-JSON bodies, which defeats the preflight-free
//     text/plain "simple request" trick.
//   - AllowedHosts rejects unexpected Host headers (DNS rebinding) while the
//     API is unauthenticated.

// writeGuardError writes a JSON {"error": msg} body. It deliberately avoids
// importing the handlers package (which imports this one).
func writeGuardError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func isUnsafeMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// defaultPort returns the implicit port for an Origin scheme.
func defaultPort(scheme string) string {
	if scheme == "https" {
		return "443"
	}
	return "80"
}

// splitHostPort splits a Host-header / URL host into a lowercased hostname
// (brackets stripped from IPv6 literals) and its port ("" when absent).
func splitHostPort(hostport string) (host, port string) {
	hostport = strings.ToLower(strings.TrimSpace(hostport))
	if h, p, err := net.SplitHostPort(hostport); err == nil {
		return strings.Trim(h, "[]"), p
	}
	return strings.Trim(hostport, "[]"), ""
}

// sameOrigin reports whether the Origin header value points at the same
// host:port the request was sent to (r.Host). Default ports are normalised
// according to the Origin's scheme, so "https://x" matches Host "x" and
// "x:443". X-Forwarded-Host is intentionally not consulted: the backend does
// not trust forwarded headers anywhere else.
func sameOrigin(origin, requestHost string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	oh, op := splitHostPort(u.Host)
	rh, rp := splitHostPort(requestHost)
	dp := defaultPort(u.Scheme)
	if op == "" {
		op = dp
	}
	if rp == "" {
		rp = dp
	}
	return oh != "" && oh == rh && op == rp
}

// parseOrigins splits a CORS_ORIGINS style comma-separated list.
func parseOrigins(origins string) []string {
	var out []string
	for _, o := range strings.Split(origins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}

// RequireTrustedOrigin rejects (403) POST/PUT/PATCH/DELETE requests that carry
// an Origin header which is neither in the CORS allowed-origins list nor
// same-origin with the request's Host. Requests with no Origin header (curl,
// server-to-server, the MCP sidecars) and all safe methods (including CORS
// preflight OPTIONS) pass. corsOrigins == "*" keeps the permissive behaviour:
// every Origin is accepted (an empty list allows only same-origin, like CORS).
func RequireTrustedOrigin(corsOrigins string) func(http.Handler) http.Handler {
	permissive := strings.TrimSpace(corsOrigins) == "*"
	allowed := parseOrigins(corsOrigins)
	return func(next http.Handler) http.Handler {
		if permissive {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" || !isUnsafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			for _, o := range allowed {
				if strings.EqualFold(o, origin) {
					next.ServeHTTP(w, r)
					return
				}
			}
			if sameOrigin(origin, r.Host) {
				next.ServeHTTP(w, r)
				return
			}
			writeGuardError(w, http.StatusForbidden,
				"cross-origin request rejected: Origin "+origin+" is not allowed; add it to CORS_ORIGINS")
		})
	}
}

// jsonExemption describes a route that legitimately takes a non-JSON body.
type jsonExemption struct {
	method string
	// match reports whether the request path is covered.
	match func(path string) bool
	// accept reports whether the (parsed, lowercased) media type is acceptable
	// on this route in addition to JSON.
	accept func(mediaType string) bool
}

func isYAMLType(mt string) bool {
	switch mt {
	case "application/yaml", "application/x-yaml", "text/yaml", "text/x-yaml":
		return true
	}
	return false
}

var jsonExemptions = []jsonExemption{
	// Task create accepts multipart/form-data (image attachments).
	{
		method: http.MethodPost,
		match:  func(p string) bool { return p == "/api/v1/tasks" },
		accept: func(mt string) bool { return mt == "multipart/form-data" },
	},
	// Workflow import / in-place YAML update take a YAML document.
	{
		method: http.MethodPost,
		match:  func(p string) bool { return p == "/api/v1/workflows/import" },
		accept: isYAMLType,
	},
	{
		method: http.MethodPut,
		match: func(p string) bool {
			return strings.HasPrefix(p, "/api/v1/workflows/") && strings.HasSuffix(p, "/yaml")
		},
		accept: isYAMLType,
	},
}

func isJSONType(mt string) bool {
	return mt == "application/json" || (strings.HasPrefix(mt, "application/") && strings.HasSuffix(mt, "+json"))
}

// RequireJSON rejects (415) POST/PUT/PATCH requests that have a non-empty
// body but a Content-Type other than application/json (parameters such as
// charset are allowed). Without this, a cross-site page can send a
// text/plain "simple" POST that skips CORS preflight yet is still decoded as
// JSON by the handlers. Bodyless requests (ContentLength == 0) pass. Routes
// that legitimately take other types are listed in jsonExemptions (multipart
// task create, YAML workflow import/update); each exemption accepts JSON as
// well as its own type, and nothing else.
func RequireJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch:
		default:
			next.ServeHTTP(w, r)
			return
		}
		if r.ContentLength == 0 || r.Body == nil || r.Body == http.NoBody {
			next.ServeHTTP(w, r)
			return
		}
		mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mt = strings.ToLower(mt)
		if err == nil && isJSONType(mt) {
			next.ServeHTTP(w, r)
			return
		}
		if err == nil {
			for _, ex := range jsonExemptions {
				if ex.method == r.Method && ex.match(r.URL.Path) && ex.accept(mt) {
					next.ServeHTTP(w, r)
					return
				}
			}
		}
		writeGuardError(w, http.StatusUnsupportedMediaType,
			"unsupported Content-Type: request bodies must be application/json")
	})
}

// HostPolicy is the compiled Host allowlist.
type HostPolicy struct {
	// Enforced is false when the check is disabled (auth configured, or
	// ALLOWED_HOSTS=*).
	Enforced bool
	// Reason explains, for the startup log, why the check is on or off.
	Reason string
	// Hosts is the effective allowlist (for the startup log).
	Hosts []string

	anyHost   bool
	hostnames map[string]bool // any port
	hostPorts map[string]bool // "host:port" exact
}

// builtinAllowedHosts are always accepted while the check is enforced:
// loopback names and the compose service name used by the frontend proxy.
var builtinAllowedHosts = []string{"localhost", "127.0.0.1", "::1", "backend"}

// NewHostPolicy builds the Host allowlist.
//
// The check is enforced only when authEnabled is false (no API_TOKEN /
// API_TOKENS): with a token configured, a DNS-rebinding page can do nothing
// without it, and skipping the check keeps deployments on public hostnames
// working with no extra config. When enforced, the allowlist is the built-in
// loopback/service names, the hostnames of CORS_ORIGINS, and allowedHosts
// (comma-separated "host" or "host:port"; "*" disables the check). Literal IP
// Host values are always accepted: a rebinding attack needs a DNS name, so an
// IP-literal Host means the browser navigated to that address directly.
func NewHostPolicy(allowedHosts, corsOrigins string, authEnabled bool) *HostPolicy {
	p := &HostPolicy{hostnames: map[string]bool{}, hostPorts: map[string]bool{}}
	if authEnabled {
		p.Reason = "API auth is enabled; Host header check skipped"
		return p
	}
	extra := parseOrigins(allowedHosts)
	for _, e := range extra {
		if e == "*" {
			p.anyHost = true
		}
	}
	if p.anyHost {
		p.Reason = "ALLOWED_HOSTS=*; Host header check disabled"
		return p
	}
	p.Enforced = true
	add := func(hostport string) {
		h, port := splitHostPort(hostport)
		if h == "" {
			return
		}
		if port != "" {
			p.hostPorts[net.JoinHostPort(h, port)] = true
			p.Hosts = append(p.Hosts, hostport)
		} else if !p.hostnames[h] {
			p.hostnames[h] = true
			p.Hosts = append(p.Hosts, h)
		}
	}
	for _, h := range builtinAllowedHosts {
		add(h)
	}
	for _, o := range parseOrigins(corsOrigins) {
		if u, err := url.Parse(o); err == nil && u.Hostname() != "" {
			add(u.Hostname())
		}
	}
	for _, e := range extra {
		add(e)
	}
	p.Reason = "API is unauthenticated; Host header check enforced (set ALLOWED_HOSTS to add hostnames, or '*' to disable)"
	return p
}

// allows reports whether the Host header value passes the policy.
func (p *HostPolicy) allows(hostport string) bool {
	if !p.Enforced {
		return true
	}
	h, port := splitHostPort(hostport)
	if h == "" {
		return false
	}
	if net.ParseIP(h) != nil {
		return true
	}
	if p.hostnames[h] {
		return true
	}
	return port != "" && p.hostPorts[net.JoinHostPort(h, port)]
}

// Middleware returns the Host-check middleware. Rejected requests get 403
// with a JSON error naming ALLOWED_HOSTS.
func (p *HostPolicy) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if !p.Enforced {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if p.allows(r.Host) {
				next.ServeHTTP(w, r)
				return
			}
			writeGuardError(w, http.StatusForbidden,
				"request rejected: Host "+r.Host+" is not allowed while the API is unauthenticated (DNS-rebinding protection); add it to the ALLOWED_HOSTS env var, or set API_TOKEN")
		})
	}
}
