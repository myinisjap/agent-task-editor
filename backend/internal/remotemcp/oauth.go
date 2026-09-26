package remotemcp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	accessTTL  = time.Hour
	refreshTTL = 30 * 24 * time.Hour
	codeTTL    = 5 * time.Minute
	pendingTTL = 10 * time.Minute

	// maxPending caps in-flight sign-ins so unauthenticated /authorize calls
	// can't grow memory without bound.
	maxPending = 1000
	maxState   = 1024

	tokenAccess  = "access"
	tokenRefresh = "refresh"
	tokenClient  = "client"
)

// claims is the payload of every token this server signs: access and refresh
// tokens, and the client ids issued by dynamic registration (which carry the
// client's registered redirect URIs so no client table is needed).
type claims struct {
	Typ          string   `json:"typ"`
	Sub          string   `json:"sub,omitempty"`
	Aud          string   `json:"aud,omitempty"`
	ClientID     string   `json:"cid,omitempty"`
	RedirectURIs []string `json:"ru,omitempty"`
	ClientName   string   `json:"cn,omitempty"`
	IssuedAt     int64    `json:"iat"`
	ExpiresAt    int64    `json:"exp,omitempty"`
}

// pendingAuth is an /authorize request waiting for Google to redirect back.
type pendingAuth struct {
	// BrowserNonce is also set as a cookie on the browser that called
	// /authorize; the callback must present it, so a Google link lifted from
	// someone else's sign-in can't complete in a victim's browser.
	BrowserNonce  string
	ClientID      string
	RedirectURI   string
	State         string
	CodeChallenge string
	Expires       time.Time
}

// authCode is an issued, not yet redeemed authorization code.
type authCode struct {
	ClientID      string
	RedirectURI   string
	CodeChallenge string
	Email         string
	Expires       time.Time
}

var b64 = base64.RawURLEncoding

func (s *Server) sign(c claims) string {
	payload, _ := json.Marshal(c)
	p := b64.EncodeToString(payload)
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(p))
	return "ate1." + p + "." + b64.EncodeToString(mac.Sum(nil))
}

func (s *Server) verify(tok, typ string) (claims, error) {
	var c claims
	parts := strings.Split(tok, ".")
	if len(parts) != 3 || parts[0] != "ate1" {
		return c, errors.New("malformed token")
	}
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(parts[1]))
	sig, err := b64.DecodeString(parts[2])
	if err != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		return c, errors.New("bad signature")
	}
	payload, err := b64.DecodeString(parts[1])
	if err != nil || json.Unmarshal(payload, &c) != nil {
		return c, errors.New("malformed token")
	}
	if c.Typ != typ {
		return c, errors.New("wrong token type")
	}
	if c.ExpiresAt != 0 && s.now().Unix() >= c.ExpiresAt {
		return c, errors.New("token expired")
	}
	return c, nil
}

func randomString() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return b64.EncodeToString(b)
}

// --- metadata ---

func (s *Server) handleResourceMetadata(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 s.resourceURL(),
		"authorization_servers":    []string{s.cfg.PublicURL},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "Agent Task Editor",
	})
}

func (s *Server) handleServerMetadata(w http.ResponseWriter, r *http.Request) {
	base := s.cfg.PublicURL
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + "/oauth/authorize",
		"token_endpoint":                        base + "/oauth/token",
		"registration_endpoint":                 base + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{"board"},
	})
}

// --- dynamic client registration (RFC 7591) ---

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "invalid JSON body")
		return
	}
	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > 10 {
		oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "between 1 and 10 redirect_uris are required")
		return
	}
	for _, ru := range req.RedirectURIs {
		if !s.redirectAllowed(ru) {
			oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect URI not allowed: "+ru)
			return
		}
	}
	if len(req.ClientName) > 100 {
		req.ClientName = req.ClientName[:100]
	}
	now := s.now()
	clientID := s.sign(claims{Typ: tokenClient, RedirectURIs: req.RedirectURIs, ClientName: req.ClientName, IssuedAt: now.Unix()})
	slog.Info("remote MCP: registered OAuth client", "name", req.ClientName, "redirect_uris", req.RedirectURIs)
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  clientID,
		"client_id_issued_at":        now.Unix(),
		"client_name":                req.ClientName,
		"redirect_uris":              req.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
}

// redirectAllowed accepts https URIs on a configured host (e.g. claude.ai) and
// loopback URIs for local clients. Restricting hosts stops someone from
// registering their own redirect and phishing an allowed user into approving
// it.
func (s *Server) redirectAllowed(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Fragment != "" || u.Host == "" {
		return false
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); (ip != nil && ip.IsLoopback()) || host == "localhost" {
		return u.Scheme == "http" || u.Scheme == "https"
	}
	return u.Scheme == "https" && slices.Contains(s.cfg.RedirectHosts, strings.ToLower(host))
}

// --- authorization endpoint ---

func (s *Server) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	client, err := s.verify(q.Get("client_id"), tokenClient)
	if err != nil {
		errorPage(w, "Unknown client. Remove the connector and add it again.")
		return
	}
	redirectURI := q.Get("redirect_uri")
	if redirectURI == "" && len(client.RedirectURIs) == 1 {
		redirectURI = client.RedirectURIs[0]
	}
	if !slices.Contains(client.RedirectURIs, redirectURI) || !s.redirectAllowed(redirectURI) {
		errorPage(w, "The redirect URI doesn't match the one this client registered.")
		return
	}
	// From here on errors go back to the client, per RFC 6749 §4.1.2.1.
	state := q.Get("state")
	fail := func(code, desc string) {
		s.redirectWith(w, r, redirectURI, url.Values{"error": {code}, "error_description": {desc}, "state": {state}})
	}
	if q.Get("response_type") != "code" {
		fail("unsupported_response_type", "only response_type=code is supported")
		return
	}
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		fail("invalid_request", "PKCE with code_challenge_method=S256 is required")
		return
	}
	if res := q.Get("resource"); res != "" && strings.TrimRight(res, "/") != s.resourceURL() {
		fail("invalid_target", "unknown resource")
		return
	}

	if len(state) > maxState || !pkceChallenge.MatchString(q.Get("code_challenge")) {
		fail("invalid_request", "state too long or malformed code_challenge")
		return
	}

	key, nonce := randomString(), randomString()
	s.mu.Lock()
	s.prune()
	full := len(s.pending) >= maxPending
	if !full {
		s.pending[key] = pendingAuth{
			BrowserNonce:  nonce,
			ClientID:      q.Get("client_id"),
			RedirectURI:   redirectURI,
			State:         state,
			CodeChallenge: q.Get("code_challenge"),
			Expires:       s.now().Add(pendingTTL),
		}
	}
	s.mu.Unlock()
	if full {
		fail("temporarily_unavailable", "too many sign-ins in progress; try again shortly")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     nonceCookie(key),
		Value:    nonce,
		Path:     s.cookiePath(),
		MaxAge:   int(pendingTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.secureCookies(),
		SameSite: http.SameSiteLaxMode,
	})

	g := url.Values{
		"client_id":     {s.cfg.GoogleClientID},
		"redirect_uri":  {s.cfg.PublicURL + "/oauth/callback"},
		"response_type": {"code"},
		"scope":         {"openid email"},
		"state":         {key},
		"prompt":        {"select_account"},
	}
	name := client.ClientName
	if name == "" {
		name = "An MCP client"
	}
	ru, _ := url.Parse(redirectURI)
	consentPage(w, name, ru.Host, s.cfg.GoogleAuthURL+"?"+g.Encode())
}

// pkceChallenge matches an S256 challenge: base64url(SHA-256) is 43 chars.
var pkceChallenge = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// nonceCookie names the per-sign-in browser-binding cookie. Keying it by the
// pending state lets several sign-ins run side by side.
func nonceCookie(key string) string { return "ate_mcp_" + key[:16] }

// secureCookies is false only for a plain-http MCP_PUBLIC_URL (local testing),
// where browsers would drop a Secure cookie.
func (s *Server) secureCookies() bool { return strings.HasPrefix(s.cfg.PublicURL, "https://") }

// cookiePath scopes the binding cookie to the OAuth routes.
func (s *Server) cookiePath() string {
	u, _ := url.Parse(s.cfg.PublicURL)
	return strings.TrimRight(u.Path, "/") + "/oauth/"
}

// handleCallback finishes the Google sign-in, checks the email against the
// allowlist and sends the client its authorization code.
func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	key := q.Get("state")
	s.mu.Lock()
	p, ok := s.pending[key]
	delete(s.pending, key)
	s.mu.Unlock()
	if !ok || s.now().After(p.Expires) {
		errorPage(w, "This sign-in link expired. Start connecting again from your client.")
		return
	}
	cookie, err := r.Cookie(nonceCookie(key))
	if err != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(p.BrowserNonce)) != 1 {
		errorPage(w, "This sign-in was started in a different browser. Start connecting again from your client.")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookie.Name, Path: s.cookiePath(), MaxAge: -1, HttpOnly: true, Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode})
	fail := func(code, desc string) {
		s.redirectWith(w, r, p.RedirectURI, url.Values{"error": {code}, "error_description": {desc}, "state": {p.State}})
	}
	if e := q.Get("error"); e != "" {
		fail("access_denied", "Google sign-in failed: "+e)
		return
	}

	email, err := s.googleEmail(r, q.Get("code"))
	if err != nil {
		slog.Warn("remote MCP: Google sign-in failed", "err", err)
		fail("server_error", "Google sign-in failed")
		return
	}
	if !s.allowed[email] {
		slog.Warn("remote MCP: sign-in by an email not in MCP_ALLOWED_EMAILS", "email", email)
		fail("access_denied", "this Google account is not allowed")
		return
	}

	code := randomString()
	s.mu.Lock()
	s.codes[code] = authCode{
		ClientID:      p.ClientID,
		RedirectURI:   p.RedirectURI,
		CodeChallenge: p.CodeChallenge,
		Email:         email,
		Expires:       s.now().Add(codeTTL),
	}
	s.mu.Unlock()
	slog.Info("remote MCP: authorized", "email", email)
	s.redirectWith(w, r, p.RedirectURI, url.Values{"code": {code}, "state": {p.State}, "iss": {s.cfg.PublicURL}})
}

// googleEmail exchanges a Google authorization code and returns the verified,
// lower-cased email from the ID token. The ID token comes straight from
// Google's token endpoint over TLS, so per OIDC Core §3.1.3.7 its signature
// needn't be checked; the issuer, audience and expiry still are.
func (s *Server) googleEmail(r *http.Request, code string) (string, error) {
	if code == "" {
		return "", errors.New("missing code")
	}
	form := url.Values{
		"code":          {code},
		"client_id":     {s.cfg.GoogleClientID},
		"client_secret": {s.cfg.GoogleClientSecret},
		"redirect_uri":  {s.cfg.PublicURL + "/oauth/callback"},
		"grant_type":    {"authorization_code"},
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.cfg.GoogleTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.http.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tr struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tr); err != nil || tr.IDToken == "" {
		return "", errors.New("no id_token in response")
	}
	parts := strings.Split(tr.IDToken, ".")
	if len(parts) != 3 {
		return "", errors.New("malformed id_token")
	}
	payload, err := b64.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return "", errors.New("malformed id_token")
	}
	var idc struct {
		Iss           string `json:"iss"`
		Aud           string `json:"aud"`
		Exp           int64  `json:"exp"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := json.Unmarshal(payload, &idc); err != nil {
		return "", errors.New("malformed id_token")
	}
	if idc.Iss != "https://accounts.google.com" && idc.Iss != "accounts.google.com" {
		return "", fmt.Errorf("unexpected issuer %q", idc.Iss)
	}
	if idc.Aud != s.cfg.GoogleClientID {
		return "", errors.New("id_token audience mismatch")
	}
	if s.now().Unix() >= idc.Exp {
		return "", errors.New("id_token expired")
	}
	if !idc.EmailVerified || idc.Email == "" {
		return "", errors.New("email not verified")
	}
	return strings.ToLower(idc.Email), nil
}

// --- token endpoint ---

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "invalid form body")
		return
	}
	clientID := r.PostForm.Get("client_id")
	if id, _, ok := r.BasicAuth(); ok && clientID == "" {
		clientID, _ = url.QueryUnescape(id)
	}
	if _, err := s.verify(clientID, tokenClient); err != nil {
		oauthError(w, http.StatusUnauthorized, "invalid_client", "unknown client")
		return
	}

	var email string
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		code := r.PostForm.Get("code")
		s.mu.Lock()
		ac, ok := s.codes[code]
		delete(s.codes, code) // single use, even on failure
		s.mu.Unlock()
		if !ok || s.now().After(ac.Expires) || ac.ClientID != clientID {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "invalid or expired code")
			return
		}
		if ru := r.PostForm.Get("redirect_uri"); ru != "" && ru != ac.RedirectURI {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri mismatch")
			return
		}
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if subtle.ConstantTimeCompare([]byte(b64.EncodeToString(sum[:])), []byte(ac.CodeChallenge)) != 1 {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
			return
		}
		email = ac.Email

	case "refresh_token":
		c, err := s.verify(r.PostForm.Get("refresh_token"), tokenRefresh)
		if err != nil || c.ClientID != clientID || c.Aud != s.resourceURL() {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "invalid refresh token")
			return
		}
		email = c.Sub

	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "")
		return
	}

	// Re-check on every issue so removing an email from MCP_ALLOWED_EMAILS
	// cuts it off within one access-token lifetime.
	if !s.allowed[email] {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "account is no longer allowed")
		return
	}
	now := s.now()
	access := s.sign(claims{Typ: tokenAccess, Sub: email, Aud: s.resourceURL(), ClientID: clientID, IssuedAt: now.Unix(), ExpiresAt: now.Add(accessTTL).Unix()})
	refresh := s.sign(claims{Typ: tokenRefresh, Sub: email, Aud: s.resourceURL(), ClientID: clientID, IssuedAt: now.Unix(), ExpiresAt: now.Add(refreshTTL).Unix()})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    int(accessTTL.Seconds()),
		"refresh_token": refresh,
		"scope":         "board",
	})
}

// prune drops expired pending sign-ins and codes. Caller holds s.mu.
func (s *Server) prune() {
	now := s.now()
	for k, p := range s.pending {
		if now.After(p.Expires) {
			delete(s.pending, k)
		}
	}
	for k, c := range s.codes {
		if now.After(c.Expires) {
			delete(s.codes, k)
		}
	}
}

func oauthError(w http.ResponseWriter, status int, code, desc string) {
	body := map[string]string{"error": code}
	if desc != "" {
		body["error_description"] = desc
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, body)
}

// redirectWith sends the browser back to a client's redirect URI with params
// added. The target was matched against the client's registered URIs, but it
// is re-checked against the allowed hosts here too, so narrowing
// MCP_REDIRECT_HOSTS also covers clients registered before the change.
func (s *Server) redirectWith(w http.ResponseWriter, r *http.Request, target string, params url.Values) {
	if !s.redirectAllowed(target) {
		errorPage(w, "The redirect URI is not allowed.")
		return
	}
	for k, v := range params {
		if len(v) == 0 || v[0] == "" {
			delete(params, k)
		}
	}
	u, _ := url.Parse(target)
	q := u.Query()
	for k, v := range params {
		q[k] = v
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// consentPage asks the signed-in browser to confirm before sending it to
// Google, naming the client and where the grant will be sent.
func consentPage(w http.ResponseWriter, client, redirectHost, googleURL string) {
	setPageHeaders(w)
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><title>Agent Task Editor</title>
<body style="font-family:system-ui,sans-serif;max-width:32rem;margin:3rem auto;padding:0 1rem;line-height:1.5">
<h1 style="font-size:1.25rem">Connect to Agent Task Editor?</h1>
<p><strong>%s</strong> wants to read and manage your board: list and create tasks, read agent runs, and approve, reject, move or reply to tasks. Access will be sent to <strong>%s</strong>.</p>
<p>Only continue if you just started connecting from that client.</p>
<p><a href="%s" style="display:inline-block;padding:.6rem 1rem;background:#1a73e8;color:#fff;border-radius:6px;text-decoration:none">Continue with Google</a></p>
</body>`, html.EscapeString(client), html.EscapeString(redirectHost), html.EscapeString(googleURL))
}

func setPageHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func errorPage(w http.ResponseWriter, msg string) {
	setPageHeaders(w)
	w.WriteHeader(http.StatusBadRequest)
	_, _ = fmt.Fprintf(w, "<!doctype html><title>Agent Task Editor</title><p>%s</p>", html.EscapeString(msg))
}
