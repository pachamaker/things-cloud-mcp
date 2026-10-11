package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	gosync "sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// OAuth 2.1 resource server for a single owner (KI-07, ADR-0002). The
// authorization server (Pocket ID) issues RFC 9068 JWT access tokens; this
// file validates them and maps their scopes to tools.

const (
	scopeRead  = "things.read"
	scopeWrite = "things.write"

	protectedResourcePath = "/.well-known/oauth-protected-resource"

	tokenLeeway      = time.Minute
	jwksMinRefresh   = time.Minute
	jwksMaxAge       = time.Hour
	authFetchTimeout = 10 * time.Second
	maxTokenBytes    = 16 << 10
	maxAuthDocBytes  = 1 << 20
)

// Asymmetric algorithms only: HS* would let anyone who knows a shared secret
// mint tokens, and go-jose never accepts "none".
var tokenAlgorithms = []jose.SignatureAlgorithm{
	jose.RS256, jose.RS384, jose.RS512,
	jose.PS256, jose.PS384, jose.PS512,
	jose.ES256, jose.ES384, jose.ES512,
	jose.EdDSA,
}

var (
	errInvalidToken    = errors.New("invalid access token")
	errKeysUnavailable = errors.New("authorization server signing keys unavailable")
)

// access is what the current request may do. The HTTP gate stores it in the
// request context; the MCP layer denies everything to a request without it.
type access struct {
	write bool
}

type accessKey struct{}

func withAccess(ctx context.Context, a access) context.Context {
	return context.WithValue(ctx, accessKey{}, a)
}

func accessFrom(ctx context.Context) (access, bool) {
	a, ok := ctx.Value(accessKey{}).(access)
	return a, ok
}

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

type oauthConfig struct {
	Issuer   string   // OAUTH_ISSUER, compared with "iss" exactly
	Resource string   // MCP_PUBLIC_URL: the RFC 9728 resource and the required "aud"
	Subject  string   // OAUTH_ALLOWED_SUBJECT: the owner's "sub"
	Clients  []string // OAUTH_ALLOWED_CLIENTS: optional "client_id" allowlist
}

type authConfig struct {
	LocalOnly bool
	OAuth     oauthConfig
}

// parseSwitch parses an on/off environment variable; unset means off.
func parseSwitch(name, raw string) (bool, error) {
	if raw == "" {
		return false, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false, got %q", name, raw)
	}
	return v, nil
}

// loadAuthConfig decides how requests are authenticated. A server without
// OAuth settings refuses to start unless it is explicitly local-only, so a
// forgotten variable never exposes the task database (AGENTS.md rule 5).
func loadAuthConfig(getenv func(string) string) (authConfig, error) {
	if getenv("API_KEY") != "" {
		return authConfig{}, errors.New("API_KEY is no longer supported: /api/* takes the same OAuth access tokens as /mcp (ADR-0002); unset it")
	}
	localOnly, err := parseSwitch("INSECURE_NO_AUTH_LOCAL_ONLY", strings.TrimSpace(getenv("INSECURE_NO_AUTH_LOCAL_ONLY")))
	if err != nil {
		return authConfig{}, err
	}
	o := oauthConfig{
		Issuer:   strings.TrimSpace(getenv("OAUTH_ISSUER")),
		Resource: strings.TrimSpace(getenv("MCP_PUBLIC_URL")),
		Subject:  strings.TrimSpace(getenv("OAUTH_ALLOWED_SUBJECT")),
	}
	for _, c := range strings.Split(getenv("OAUTH_ALLOWED_CLIENTS"), ",") {
		if c = strings.TrimSpace(c); c != "" {
			o.Clients = append(o.Clients, c)
		}
	}
	anyOAuth := o.Issuer != "" || o.Resource != "" || o.Subject != "" || len(o.Clients) > 0

	switch {
	case localOnly && anyOAuth:
		return authConfig{}, errors.New("INSECURE_NO_AUTH_LOCAL_ONLY=true cannot be combined with OAUTH_* or MCP_PUBLIC_URL settings")
	case localOnly:
		return authConfig{LocalOnly: true}, nil
	case !anyOAuth:
		return authConfig{}, errors.New("authentication is not configured: set OAUTH_ISSUER, MCP_PUBLIC_URL and OAUTH_ALLOWED_SUBJECT " +
			"(docs/deployment/vps.md), or INSECURE_NO_AUTH_LOCAL_ONLY=true to serve without authentication on 127.0.0.1 only")
	}

	var missing []string
	for _, v := range []struct{ name, value string }{
		{"OAUTH_ISSUER", o.Issuer},
		{"MCP_PUBLIC_URL", o.Resource},
		{"OAUTH_ALLOWED_SUBJECT", o.Subject},
	} {
		if v.value == "" {
			missing = append(missing, v.name)
		}
	}
	if len(missing) > 0 {
		return authConfig{}, fmt.Errorf("incomplete OAuth configuration: set %s", strings.Join(missing, ", "))
	}
	if _, err := parseHTTPSURL("OAUTH_ISSUER", o.Issuer); err != nil {
		return authConfig{}, err
	}
	u, err := parseHTTPSURL("MCP_PUBLIC_URL", o.Resource)
	if err != nil {
		return authConfig{}, err
	}
	if u.Path != "/mcp" {
		return authConfig{}, fmt.Errorf("MCP_PUBLIC_URL must end in /mcp, the path this server serves, got %q", o.Resource)
	}
	return authConfig{OAuth: o}, nil
}

func parseHTTPSURL(name, raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s is not a URL: %w", name, err)
	}
	if u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, fmt.Errorf("%s must be an https URL without credentials, query or fragment, got %q", name, raw)
	}
	return u, nil
}

// listenAddr keeps an unauthenticated server off every interface but loopback.
func listenAddr(cfg authConfig, port string) string {
	if cfg.LocalOnly {
		return "127.0.0.1:" + port
	}
	return ":" + port
}

// ---------------------------------------------------------------------------
// Signing keys
// ---------------------------------------------------------------------------

// jwksCache holds the issuer's signing keys. It refetches them when they are
// older than jwksMaxAge or a token names an unknown kid, at most once per
// jwksMinRefresh, so forged kids cannot turn the server into a request
// amplifier. A failed refresh keeps the last good keys. One fetch runs at a
// time and outside the lock: requests whose key is cached never wait for it.
type jwksCache struct {
	issuer string
	client *http.Client
	now    func() time.Time

	mu          gosync.Mutex
	keys        []jose.JSONWebKey
	fetchedAt   time.Time
	attemptedAt time.Time
	refreshing  chan struct{} // closed when the running fetch ends; nil when idle
	lastErr     error
}

func (c *jwksCache) lookup(ctx context.Context, kid string) (jose.JSONWebKey, error) {
	c.mu.Lock()
	now := c.now()
	key, found := c.find(kid)
	if found && now.Sub(c.fetchedAt) < jwksMaxAge {
		c.mu.Unlock()
		return key, nil
	}
	var wait chan struct{}
	switch {
	case c.refreshing != nil && found:
		c.mu.Unlock()
		return key, nil
	case c.refreshing != nil:
		wait = c.refreshing
	case c.attemptedAt.IsZero() || now.Sub(c.attemptedAt) >= jwksMinRefresh:
		wait = c.startRefreshLocked()
	}
	c.mu.Unlock()

	if wait != nil {
		select {
		case <-wait:
		case <-ctx.Done():
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if key, found := c.find(kid); found {
		return key, nil
	}
	if c.fetchedAt.IsZero() {
		return jose.JSONWebKey{}, errKeysUnavailable
	}
	return jose.JSONWebKey{}, fmt.Errorf("%w: unknown signing key", errInvalidToken)
}

// refresh fetches the keys now (or waits for the running fetch); main calls
// it once at startup to surface configuration mistakes in the log.
func (c *jwksCache) refresh(ctx context.Context) (int, error) {
	c.mu.Lock()
	wait := c.refreshing
	if wait == nil {
		wait = c.startRefreshLocked()
	}
	c.mu.Unlock()
	select {
	case <-wait:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.keys), c.lastErr
}

// startRefreshLocked starts a fetch that no request owns, so a client that
// hangs up does not cancel it.
func (c *jwksCache) startRefreshLocked() chan struct{} {
	done := make(chan struct{})
	c.refreshing = done
	c.attemptedAt = c.now()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), authFetchTimeout)
		defer cancel()
		keys, err := c.fetch(ctx)
		c.mu.Lock()
		if err != nil {
			log.Printf("auth: cannot refresh signing keys from %s: %v", c.issuer, err)
		} else {
			c.keys, c.fetchedAt = keys, c.now()
		}
		c.lastErr = err
		c.refreshing = nil
		c.mu.Unlock()
		close(done)
	}()
	return done
}

func (c *jwksCache) find(kid string) (jose.JSONWebKey, bool) {
	for _, k := range c.keys {
		if k.KeyID == kid {
			return k, true
		}
	}
	return jose.JSONWebKey{}, false
}

func (c *jwksCache) fetch(ctx context.Context) ([]jose.JSONWebKey, error) {
	jwksURI, err := c.discoverJWKS(ctx)
	if err != nil {
		return nil, err
	}
	var set jose.JSONWebKeySet
	if err := c.getJSON(ctx, jwksURI, &set); err != nil {
		return nil, err
	}
	var keys []jose.JSONWebKey
	for _, k := range set.Keys {
		if k.KeyID == "" || (k.Use != "" && k.Use != "sig") {
			continue
		}
		if !k.IsPublic() {
			k = k.Public()
		}
		switch k.Key.(type) {
		case *rsa.PublicKey, *ecdsa.PublicKey, ed25519.PublicKey:
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%s has no usable signing keys", jwksURI)
	}
	return keys, nil
}

// discoverJWKS reads RFC 8414 metadata, falling back to OpenID Connect
// discovery, and insists that the document describes the configured issuer.
func (c *jwksCache) discoverJWKS(ctx context.Context) (string, error) {
	u, err := url.Parse(c.issuer)
	if err != nil {
		return "", err
	}
	candidates := []string{
		u.Scheme + "://" + u.Host + "/.well-known/oauth-authorization-server" + strings.TrimSuffix(u.Path, "/"),
		strings.TrimSuffix(c.issuer, "/") + "/.well-known/openid-configuration",
	}
	var errs []error
	for _, metadataURL := range candidates {
		var meta struct {
			Issuer  string `json:"issuer"`
			JWKSURI string `json:"jwks_uri"`
		}
		if err := c.getJSON(ctx, metadataURL, &meta); err != nil {
			errs = append(errs, err)
			continue
		}
		if meta.Issuer != c.issuer {
			return "", fmt.Errorf("%s describes issuer %q, want %q", metadataURL, meta.Issuer, c.issuer)
		}
		if _, err := parseHTTPSURL("jwks_uri", meta.JWKSURI); err != nil {
			return "", fmt.Errorf("%s: %w", metadataURL, err)
		}
		return meta.JWKSURI, nil
	}
	return "", errors.Join(errs...)
}

func (c *jwksCache) getJSON(ctx context.Context, target string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", target, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAuthDocBytes)).Decode(v); err != nil {
		return fmt.Errorf("GET %s: %w", target, err)
	}
	return nil
}

// newAuthHTTPClient fetches issuer metadata and keys; redirects are refused
// so the documents always come from the configured https origin.
func newAuthHTTPClient() *http.Client {
	return &http.Client{
		Timeout: authFetchTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// ---------------------------------------------------------------------------
// Token validation
// ---------------------------------------------------------------------------

type tokenVerifier struct {
	cfg  oauthConfig
	keys *jwksCache
	now  func() time.Time
}

type tokenInfo struct {
	scopes scopeSet
}

func newTokenVerifier(cfg oauthConfig, client *http.Client, now func() time.Time) (*tokenVerifier, error) {
	if cfg.Issuer == "" || cfg.Resource == "" || cfg.Subject == "" {
		return nil, errors.New("token verifier needs an issuer, a resource and a subject")
	}
	return &tokenVerifier{
		cfg:  cfg,
		keys: &jwksCache{issuer: cfg.Issuer, client: client, now: now},
		now:  now,
	}, nil
}

// verify checks an RFC 9068 access token: asymmetric signature from the
// issuer's JWKS, typ at+jwt, iss, aud = this resource, exp/nbf/iat, the
// owner's sub and, if configured, the client. Errors wrap errInvalidToken,
// or errKeysUnavailable when the keys cannot be fetched at all. They never
// include the token.
func (v *tokenVerifier) verify(ctx context.Context, raw string) (tokenInfo, error) {
	invalid := func(format string, args ...any) (tokenInfo, error) {
		return tokenInfo{}, fmt.Errorf("%w: %s", errInvalidToken, fmt.Sprintf(format, args...))
	}
	if len(raw) > maxTokenBytes {
		return invalid("token longer than %d bytes", maxTokenBytes)
	}
	tok, err := jwt.ParseSigned(raw, tokenAlgorithms)
	if err != nil {
		return invalid("not a signed JWT with an allowed algorithm")
	}
	if len(tok.Headers) != 1 {
		return invalid("expected one signature")
	}
	h := tok.Headers[0]
	typ, _ := h.ExtraHeaders[jose.HeaderType].(string)
	if !strings.EqualFold(typ, "at+jwt") && !strings.EqualFold(typ, "application/at+jwt") {
		return invalid("typ is not at+jwt (RFC 9068)")
	}
	if h.KeyID == "" {
		return invalid("no kid")
	}
	key, err := v.keys.lookup(ctx, h.KeyID)
	if err != nil {
		return tokenInfo{}, err
	}
	if key.Algorithm != "" && key.Algorithm != h.Algorithm {
		return invalid("algorithm %s does not match the key's %s", h.Algorithm, key.Algorithm)
	}

	var claims jwt.Claims
	var extra struct {
		Scope    scopeSet `json:"scope"`
		ClientID string   `json:"client_id"`
	}
	if err := tok.Claims(key.Key, &claims, &extra); err != nil {
		return invalid("bad signature or claims")
	}
	if claims.Expiry == nil {
		return invalid("no exp")
	}
	if err := claims.ValidateWithLeeway(jwt.Expected{
		Issuer:      v.cfg.Issuer,
		AnyAudience: jwt.Audience{v.cfg.Resource},
		Time:        v.now(),
	}, tokenLeeway); err != nil {
		return invalid("%v", err)
	}
	if claims.Subject != v.cfg.Subject {
		return invalid("subject is not the allowed owner")
	}
	if len(v.cfg.Clients) > 0 && !slices.Contains(v.cfg.Clients, extra.ClientID) {
		return invalid("client is not allowed")
	}
	return tokenInfo{scopes: extra.Scope}, nil
}

// scopeSet decodes the RFC 9068 "scope" claim: a space-separated string,
// or a JSON array as some servers send it.
type scopeSet map[string]bool

func (s *scopeSet) UnmarshalJSON(b []byte) error {
	var list []string
	var str string
	if err := json.Unmarshal(b, &str); err == nil {
		list = strings.Fields(str)
	} else if err := json.Unmarshal(b, &list); err != nil {
		return fmt.Errorf("scope: %w", err)
	}
	set := scopeSet{}
	for _, v := range list {
		set[v] = true
	}
	*s = set
	return nil
}

// ---------------------------------------------------------------------------
// HTTP gates
// ---------------------------------------------------------------------------

// gate authenticates data routes. protect wraps a handler; registerPublic
// adds the unauthenticated routes the gate itself needs.
type gate interface {
	protect(next http.Handler, needWrite bool) http.Handler
	registerPublic(mux *http.ServeMux)
}

// localGate is for INSECURE_NO_AUTH_LOCAL_ONLY: no token, full access, and
// the server listens on loopback only (listenAddr). Loopback alone does not
// stop DNS rebinding: a web page whose name resolves to 127.0.0.1 reaches the
// server with its own name in Host and Origin, and mcp-go v0.44 checks
// neither, so the gate does.
type localGate struct{}

func (localGate) protect(next http.Handler, _ bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHost(r.Host) || !isLoopbackOrigin(r.Header.Get("Origin")) {
			jsonError(w, "local-only server: requests must name localhost or a loopback address", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(withAccess(r.Context(), access{write: true})))
	})
}

func isLoopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// isLoopbackOrigin accepts requests without Origin (non-browser clients) and
// browser requests from pages on this machine.
func isLoopbackOrigin(origin string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && isLoopbackHost(u.Host)
}

func (localGate) registerPublic(*http.ServeMux) {}

type oauthGate struct {
	cfg         oauthConfig
	verifier    *tokenVerifier
	metadataURL string
	scopes      []string
}

func newOAuthGate(cfg oauthConfig, writes bool, client *http.Client, now func() time.Time) (*oauthGate, error) {
	v, err := newTokenVerifier(cfg, client, now)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(cfg.Resource)
	if err != nil {
		return nil, err
	}
	scopes := []string{scopeRead}
	if writes {
		scopes = append(scopes, scopeWrite)
	}
	return &oauthGate{
		cfg:         cfg,
		verifier:    v,
		metadataURL: u.Scheme + "://" + u.Host + protectedResourcePath + u.Path,
		scopes:      scopes,
	}, nil
}

// warmUp fetches the signing keys once so a wrong issuer shows up in the
// startup log instead of at the first sign-in.
func (g *oauthGate) warmUp(ctx context.Context) {
	n, err := g.verifier.keys.refresh(ctx)
	if err != nil {
		log.Printf("Auth: WARNING: cannot load signing keys from %s yet: %v", g.cfg.Issuer, err)
		return
	}
	log.Printf("Auth: loaded %d signing key(s) from %s", n, g.cfg.Issuer)
}

func (g *oauthGate) protect(next http.Handler, needWrite bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := bearerToken(r)
		if !ok {
			g.challenge(w, http.StatusUnauthorized, "", "", strings.Join(g.scopes, " "))
			return
		}
		info, err := g.verifier.verify(r.Context(), raw)
		if errors.Is(err, errKeysUnavailable) {
			log.Printf("auth: %s %s: %v", r.Method, r.URL.Path, err)
			w.Header().Set("Retry-After", "60")
			jsonError(w, "authorization server unavailable", http.StatusServiceUnavailable)
			return
		}
		if err != nil {
			log.Printf("auth: %s %s rejected: %v", r.Method, r.URL.Path, err)
			g.challenge(w, http.StatusUnauthorized, "invalid_token", "The access token is invalid or expired", strings.Join(g.scopes, " "))
			return
		}
		need := scopeRead
		if needWrite {
			need = scopeRead + " " + scopeWrite
		}
		if !info.scopes[scopeRead] || (needWrite && !info.scopes[scopeWrite]) {
			g.challenge(w, http.StatusForbidden, "insufficient_scope", "The access token lacks a required scope", need)
			return
		}
		next.ServeHTTP(w, r.WithContext(withAccess(r.Context(), access{write: info.scopes[scopeWrite]})))
	})
}

// challenge writes an RFC 6750 error with the RFC 9728 metadata pointer that
// starts the client's OAuth discovery. A request without a token gets no
// error code (RFC 6750 §3.1).
func (g *oauthGate) challenge(w http.ResponseWriter, status int, code, description, scope string) {
	params := []string{}
	if code != "" {
		params = append(params, fmt.Sprintf(`error=%q`, code), fmt.Sprintf(`error_description=%q`, description))
	}
	params = append(params, fmt.Sprintf(`resource_metadata=%q`, g.metadataURL), fmt.Sprintf(`scope=%q`, scope))
	w.Header().Set("WWW-Authenticate", "Bearer "+strings.Join(params, ", "))
	msg := "authentication required"
	if code != "" {
		msg = code
	}
	jsonError(w, msg, status)
}

func bearerToken(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

// registerPublic serves the RFC 9728 protected-resource metadata at the
// path-specific location for /mcp and at the root location.
func (g *oauthGate) registerPublic(mux *http.ServeMux) {
	doc := map[string]any{
		"resource":                 g.cfg.Resource,
		"authorization_servers":    []string{g.cfg.Issuer},
		"scopes_supported":         g.scopes,
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "Things Cloud MCP",
	}
	h := func(w http.ResponseWriter, r *http.Request) { jsonResponse(w, doc) }
	u, _ := url.Parse(g.cfg.Resource)
	mux.HandleFunc("GET "+protectedResourcePath+u.Path, h)
	mux.HandleFunc("GET "+protectedResourcePath, h)
}

// ---------------------------------------------------------------------------
// MCP tool access
// ---------------------------------------------------------------------------

// toolFilter hides write tools from tokens without things.write and every
// tool from requests that did not pass a gate.
func toolFilter(writeTools map[string]bool) server.ToolFilterFunc {
	return func(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
		a, ok := accessFrom(ctx)
		if !ok {
			return nil
		}
		if a.write {
			return tools
		}
		return slices.DeleteFunc(slices.Clone(tools), func(t mcp.Tool) bool { return writeTools[t.Name] })
	}
}

// toolAccess enforces the same rule on calls, which the filter alone does
// not cover: a client can call a tool it was never shown.
func toolAccess(writeTools map[string]bool) server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			a, ok := accessFrom(ctx)
			if !ok {
				return mcp.NewToolResultError("request is not authenticated"), nil
			}
			if writeTools[req.Params.Name] && !a.write {
				return mcp.NewToolResultError(fmt.Sprintf(
					"%s changes data, but this connection is read-only: its access token lacks the %s scope. Reconnect with write access to use it.",
					req.Params.Name, scopeWrite)), nil
			}
			return next(ctx, req)
		}
	}
}
