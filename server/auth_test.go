package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	gosync "sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const (
	testResource = "https://mcp.example.test/mcp"
	testSubject  = "owner-7f3c"
	testClient   = "claude-client"
)

var testRSAKeys = gosync.OnceValue(func() [2]*rsa.PrivateKey {
	var keys [2]*rsa.PrivateKey
	for i := range keys {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		keys[i] = k
	}
	return keys
})

type issuerKey struct {
	alg       jose.SignatureAlgorithm
	key       crypto.Signer
	published bool
}

// testIssuer is a local authorization server: RFC 8414 / OIDC discovery and
// a JWKS on a TLS httptest server. Nothing leaves the process.
type testIssuer struct {
	srv   *httptest.Server
	clock *fakeClock

	mu               gosync.Mutex
	keys             map[string]issuerKey
	down             bool
	oidcOnly         bool
	metadataIssuer   string
	jwksURI          string // overrides the advertised jwks_uri
	redirectMetadata bool
	keyUse           string // "use" of published keys, default "sig"
	noAlg            bool   // publish keys without "alg"
	hold             chan struct{}
	entered          chan struct{}

	jwksHits atomic.Int32
}

func newTestIssuer(t *testing.T, clock *fakeClock) *testIssuer {
	t.Helper()
	is := &testIssuer{clock: clock, keys: map[string]issuerKey{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		is.mu.Lock()
		oidcOnly, redirect := is.oidcOnly, is.redirectMetadata
		is.mu.Unlock()
		switch {
		case oidcOnly:
			http.NotFound(w, r)
		case redirect:
			http.Redirect(w, r, "/metadata-elsewhere", http.StatusFound)
		default:
			is.serveMetadata(w)
		}
	})
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		is.mu.Lock()
		redirect := is.redirectMetadata
		is.mu.Unlock()
		if redirect {
			http.Redirect(w, r, "/metadata-elsewhere", http.StatusFound)
			return
		}
		is.serveMetadata(w)
	})
	mux.HandleFunc("/metadata-elsewhere", func(w http.ResponseWriter, r *http.Request) {
		is.serveMetadata(w)
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		is.mu.Lock()
		hold := is.hold
		if hold != nil && is.entered != nil {
			close(is.entered)
			is.entered = nil
		}
		is.mu.Unlock()
		if hold != nil {
			select {
			case <-hold:
			case <-r.Context().Done():
			}
		}

		is.mu.Lock()
		defer is.mu.Unlock()
		if is.down {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		is.jwksHits.Add(1)
		use := is.keyUse
		if use == "" {
			use = "sig"
		}
		var set jose.JSONWebKeySet
		for kid, k := range is.keys {
			if k.published {
				jwk := jose.JSONWebKey{Key: k.key.Public(), KeyID: kid, Algorithm: string(k.alg), Use: use}
				if is.noAlg {
					jwk.Algorithm = ""
				}
				set.Keys = append(set.Keys, jwk)
			}
		}
		json.NewEncoder(w).Encode(set)
	})
	is.srv = httptest.NewTLSServer(mux)
	t.Cleanup(is.srv.Close)
	is.addKey("k1", jose.RS256, testRSAKeys()[0], true)
	return is
}

func (is *testIssuer) serveMetadata(w http.ResponseWriter) {
	is.mu.Lock()
	defer is.mu.Unlock()
	if is.down {
		http.Error(w, "down", http.StatusInternalServerError)
		return
	}
	issuer := is.srv.URL
	if is.metadataIssuer != "" {
		issuer = is.metadataIssuer
	}
	jwksURI := is.srv.URL + "/jwks"
	if is.jwksURI != "" {
		jwksURI = is.jwksURI
	}
	json.NewEncoder(w).Encode(map[string]any{
		"issuer":                 issuer,
		"authorization_endpoint": is.srv.URL + "/authorize",
		"token_endpoint":         is.srv.URL + "/api/oidc/token",
		"jwks_uri":               jwksURI,
	})
}

func (is *testIssuer) addKey(kid string, alg jose.SignatureAlgorithm, key crypto.Signer, published bool) {
	is.mu.Lock()
	defer is.mu.Unlock()
	is.keys[kid] = issuerKey{alg: alg, key: key, published: published}
}

func (is *testIssuer) setDown(down bool) {
	is.mu.Lock()
	defer is.mu.Unlock()
	is.down = down
}

func (is *testIssuer) configure(f func(is *testIssuer)) {
	is.mu.Lock()
	defer is.mu.Unlock()
	f(is)
}

// holdJWKS makes the next JWKS request wait until release is called;
// entered is closed once that request has arrived.
func (is *testIssuer) holdJWKS(t *testing.T) (entered <-chan struct{}, release func()) {
	t.Helper()
	is.mu.Lock()
	defer is.mu.Unlock()
	hold, in := make(chan struct{}), make(chan struct{})
	is.hold, is.entered = hold, in
	var once gosync.Once
	release = func() {
		once.Do(func() {
			is.mu.Lock()
			is.hold = nil
			is.mu.Unlock()
			close(hold)
		})
	}
	t.Cleanup(release)
	return in, release
}

type mintOpts struct {
	kid    string                  // default "k1"
	alg    jose.SignatureAlgorithm // default: the key's algorithm
	typ    string                  // default "at+jwt"; "-" omits the header
	claims func(map[string]any)
}

// mint signs an access token shaped like Pocket ID's RFC 9068 tokens.
func (is *testIssuer) mint(t *testing.T, o mintOpts) string {
	t.Helper()
	if o.kid == "" {
		o.kid = "k1"
	}
	is.mu.Lock()
	k, ok := is.keys[o.kid]
	is.mu.Unlock()
	if !ok {
		t.Fatalf("no test key %q", o.kid)
	}
	if o.alg == "" {
		o.alg = k.alg
	}
	opts := &jose.SignerOptions{}
	switch o.typ {
	case "":
		opts = opts.WithType("at+jwt")
	case "-":
	default:
		opts = opts.WithType(jose.ContentType(o.typ))
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: o.alg, Key: jose.JSONWebKey{Key: k.key, KeyID: o.kid}}, opts)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	now := is.clock.Now()
	claims := map[string]any{
		"iss":       is.srv.URL,
		"aud":       testResource,
		"sub":       testSubject,
		"client_id": testClient,
		"scope":     "things.read things.write",
		"iat":       now.Unix(),
		"exp":       now.Add(time.Hour).Unix(),
		"jti":       "token-1",
	}
	if o.claims != nil {
		o.claims(claims)
	}
	raw, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return raw
}

func testOAuthConfig(is *testIssuer) oauthConfig {
	return oauthConfig{Issuer: is.srv.URL, Resource: testResource, Subject: testSubject}
}

func newTestVerifier(t *testing.T, is *testIssuer, cfg oauthConfig) *tokenVerifier {
	t.Helper()
	v, err := newTokenVerifier(cfg, is.srv.Client(), is.clock.Now)
	if err != nil {
		t.Fatalf("newTokenVerifier: %v", err)
	}
	return v
}

func TestTokenVerifier_KI07(t *testing.T) {
	t.Parallel()

	hmacToken := func(t *testing.T, is *testIssuer) string {
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: jose.JSONWebKey{Key: []byte("0123456789abcdef0123456789abcdef"), KeyID: "k1"}},
			(&jose.SignerOptions{}).WithType("at+jwt"))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := jwt.Signed(signer).Claims(map[string]any{
			"iss": is.srv.URL, "aud": testResource, "sub": testSubject, "scope": "things.read",
			"exp": is.clock.Now().Add(time.Hour).Unix(),
		}).Serialize()
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	set := func(k string, v any) func(map[string]any) {
		return func(c map[string]any) { c[k] = v }
	}
	drop := func(k string) func(map[string]any) {
		return func(c map[string]any) { delete(c, k) }
	}

	tests := []struct {
		name       string
		setup      func(is *testIssuer)
		cfg        func(*oauthConfig)
		token      func(t *testing.T, is *testIssuer) string
		wantErr    bool
		wantScopes []string
	}{
		{
			name:       "valid RS256 token",
			token:      func(t *testing.T, is *testIssuer) string { return is.mint(t, mintOpts{}) },
			wantScopes: []string{scopeRead, scopeWrite},
		},
		{
			name: "valid ES256 token",
			setup: func(is *testIssuer) {
				k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				is.addKey("ec", jose.ES256, k, true)
			},
			token:      func(t *testing.T, is *testIssuer) string { return is.mint(t, mintOpts{kid: "ec"}) },
			wantScopes: []string{scopeRead, scopeWrite},
		},
		{
			name: "valid EdDSA token",
			setup: func(is *testIssuer) {
				_, k, _ := ed25519.GenerateKey(rand.Reader)
				is.addKey("ed", jose.EdDSA, k, true)
			},
			token:      func(t *testing.T, is *testIssuer) string { return is.mint(t, mintOpts{kid: "ed"}) },
			wantScopes: []string{scopeRead, scopeWrite},
		},
		{
			name:       "typ application/at+jwt",
			token:      func(t *testing.T, is *testIssuer) string { return is.mint(t, mintOpts{typ: "application/at+jwt"}) },
			wantScopes: []string{scopeRead, scopeWrite},
		},
		{
			name: "audience array containing the resource",
			token: func(t *testing.T, is *testIssuer) string {
				return is.mint(t, mintOpts{claims: set("aud", []string{testClient, testResource})})
			},
			wantScopes: []string{scopeRead, scopeWrite},
		},
		{
			name: "read-only scope",
			token: func(t *testing.T, is *testIssuer) string {
				return is.mint(t, mintOpts{claims: set("scope", "openid things.read")})
			},
			wantScopes: []string{"openid", scopeRead},
		},
		{
			name: "scope as a JSON array",
			token: func(t *testing.T, is *testIssuer) string {
				return is.mint(t, mintOpts{claims: set("scope", []string{scopeRead})})
			},
			wantScopes: []string{scopeRead},
		},
		{
			name:       "no scope claim",
			token:      func(t *testing.T, is *testIssuer) string { return is.mint(t, mintOpts{claims: drop("scope")}) },
			wantScopes: nil,
		},
		{
			name: "expired within leeway",
			token: func(t *testing.T, is *testIssuer) string {
				return is.mint(t, mintOpts{claims: set("exp", is.clock.Now().Add(-30*time.Second).Unix())})
			},
			wantScopes: []string{scopeRead, scopeWrite},
		},
		{
			name: "allowed client",
			cfg:  func(c *oauthConfig) { c.Clients = []string{"chatgpt-client", testClient} },
			token: func(t *testing.T, is *testIssuer) string {
				return is.mint(t, mintOpts{})
			},
			wantScopes: []string{scopeRead, scopeWrite},
		},
		{
			name: "expired",
			token: func(t *testing.T, is *testIssuer) string {
				return is.mint(t, mintOpts{claims: set("exp", is.clock.Now().Add(-2*time.Minute).Unix())})
			},
			wantErr: true,
		},
		{
			name:    "no exp",
			token:   func(t *testing.T, is *testIssuer) string { return is.mint(t, mintOpts{claims: drop("exp")}) },
			wantErr: true,
		},
		{
			name: "not valid yet",
			token: func(t *testing.T, is *testIssuer) string {
				return is.mint(t, mintOpts{claims: set("nbf", is.clock.Now().Add(5*time.Minute).Unix())})
			},
			wantErr: true,
		},
		{
			name: "issued in the future",
			token: func(t *testing.T, is *testIssuer) string {
				return is.mint(t, mintOpts{claims: set("iat", is.clock.Now().Add(5*time.Minute).Unix())})
			},
			wantErr: true,
		},
		{
			name: "wrong issuer",
			token: func(t *testing.T, is *testIssuer) string {
				return is.mint(t, mintOpts{claims: set("iss", "https://evil.example")})
			},
			wantErr: true,
		},
		{
			name:    "audience is the client id",
			token:   func(t *testing.T, is *testIssuer) string { return is.mint(t, mintOpts{claims: set("aud", testClient)}) },
			wantErr: true,
		},
		{
			name: "audience is another resource",
			token: func(t *testing.T, is *testIssuer) string {
				return is.mint(t, mintOpts{claims: set("aud", "https://mcp.example.test/other")})
			},
			wantErr: true,
		},
		{
			name:    "no audience",
			token:   func(t *testing.T, is *testIssuer) string { return is.mint(t, mintOpts{claims: drop("aud")}) },
			wantErr: true,
		},
		{
			name: "wrong subject",
			token: func(t *testing.T, is *testIssuer) string {
				return is.mint(t, mintOpts{claims: set("sub", "someone-else")})
			},
			wantErr: true,
		},
		{
			name:    "no subject",
			token:   func(t *testing.T, is *testIssuer) string { return is.mint(t, mintOpts{claims: drop("sub")}) },
			wantErr: true,
		},
		{
			name:    "client not allowed",
			cfg:     func(c *oauthConfig) { c.Clients = []string{"chatgpt-client"} },
			token:   func(t *testing.T, is *testIssuer) string { return is.mint(t, mintOpts{}) },
			wantErr: true,
		},
		{
			name:    "typ JWT (an ID token, not an access token)",
			token:   func(t *testing.T, is *testIssuer) string { return is.mint(t, mintOpts{typ: "JWT"}) },
			wantErr: true,
		},
		{
			name:    "no typ",
			token:   func(t *testing.T, is *testIssuer) string { return is.mint(t, mintOpts{typ: "-"}) },
			wantErr: true,
		},
		{
			name:    "HS256 with a guessed secret",
			token:   hmacToken,
			wantErr: true,
		},
		{
			name: "algorithm differs from the published key's",
			token: func(t *testing.T, is *testIssuer) string {
				return is.mint(t, mintOpts{alg: jose.PS256})
			},
			wantErr: true,
		},
		{
			name: "signature does not match the published key",
			setup: func(is *testIssuer) {
				is.addKey("rogue", jose.RS256, testRSAKeys()[1], false)
			},
			token: func(t *testing.T, is *testIssuer) string {
				raw := is.mint(t, mintOpts{kid: "rogue"})
				genuine := is.mint(t, mintOpts{})
				// Header of the genuine token (kid k1), payload and signature of the rogue one.
				return strings.Split(genuine, ".")[0] + raw[strings.Index(raw, "."):]
			},
			wantErr: true,
		},
		{
			name: "unknown kid",
			setup: func(is *testIssuer) {
				is.addKey("k9", jose.RS256, testRSAKeys()[1], false)
			},
			token:   func(t *testing.T, is *testIssuer) string { return is.mint(t, mintOpts{kid: "k9"}) },
			wantErr: true,
		},
		{
			name:    "not a JWT",
			token:   func(t *testing.T, is *testIssuer) string { return "not-a-token" },
			wantErr: true,
		},
		{
			name: "oversized but otherwise valid token",
			token: func(t *testing.T, is *testIssuer) string {
				return is.mint(t, mintOpts{claims: set("pad", strings.Repeat("a", maxTokenBytes))})
			},
			wantErr: true,
		},
		{
			name: "key published without alg",
			setup: func(is *testIssuer) {
				is.configure(func(is *testIssuer) { is.noAlg = true })
			},
			token:      func(t *testing.T, is *testIssuer) string { return is.mint(t, mintOpts{}) },
			wantScopes: []string{scopeRead, scopeWrite},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			is := newTestIssuer(t, newFakeClock())
			if tt.setup != nil {
				tt.setup(is)
			}
			cfg := testOAuthConfig(is)
			if tt.cfg != nil {
				tt.cfg(&cfg)
			}
			v := newTestVerifier(t, is, cfg)

			info, err := v.verify(context.Background(), tt.token(t, is))
			if tt.wantErr {
				if err == nil {
					t.Fatal("verify accepted the token")
				}
				if !errors.Is(err, errInvalidToken) {
					t.Fatalf("error = %v, want errInvalidToken", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("verify: %v", err)
			}
			var got []string
			for s := range info.scopes {
				got = append(got, s)
			}
			slices.Sort(got)
			want := slices.Clone(tt.wantScopes)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("scopes = %v, want %v", got, want)
			}
		})
	}
}

func TestNewTokenVerifier_KI07_RejectsIncompleteConfig(t *testing.T) {
	t.Parallel()

	is := newTestIssuer(t, newFakeClock())
	for _, cfg := range []oauthConfig{
		{Resource: testResource, Subject: testSubject},
		{Issuer: is.srv.URL, Subject: testSubject},
		{Issuer: is.srv.URL, Resource: testResource},
	} {
		if _, err := newTokenVerifier(cfg, is.srv.Client(), is.clock.Now); err == nil {
			t.Errorf("newTokenVerifier(%+v) accepted an incomplete config", cfg)
		}
	}
}

func TestJWKSCache_KI07(t *testing.T) {
	t.Parallel()

	t.Run("rotation: a new kid triggers a refetch", func(t *testing.T) {
		t.Parallel()
		is := newTestIssuer(t, newFakeClock())
		v := newTestVerifier(t, is, testOAuthConfig(is))
		if _, err := v.verify(context.Background(), is.mint(t, mintOpts{})); err != nil {
			t.Fatalf("k1: %v", err)
		}
		is.addKey("k2", jose.RS256, testRSAKeys()[1], true)
		is.clock.Advance(jwksMinRefresh)
		if _, err := v.verify(context.Background(), is.mint(t, mintOpts{kid: "k2"})); err != nil {
			t.Fatalf("k2 after rotation: %v", err)
		}
		if got := is.jwksHits.Load(); got != 2 {
			t.Errorf("JWKS fetched %d times, want 2", got)
		}
	})

	t.Run("unknown kids refetch at most once per interval", func(t *testing.T) {
		t.Parallel()
		is := newTestIssuer(t, newFakeClock())
		is.addKey("k9", jose.RS256, testRSAKeys()[1], false)
		v := newTestVerifier(t, is, testOAuthConfig(is))
		if _, err := v.verify(context.Background(), is.mint(t, mintOpts{})); err != nil {
			t.Fatalf("k1: %v", err)
		}
		is.clock.Advance(jwksMinRefresh)
		for range 5 {
			if _, err := v.verify(context.Background(), is.mint(t, mintOpts{kid: "k9"})); !errors.Is(err, errInvalidToken) {
				t.Fatalf("unknown kid: err = %v, want errInvalidToken", err)
			}
		}
		if got := is.jwksHits.Load(); got != 2 {
			t.Errorf("JWKS fetched %d times within one interval, want 2", got)
		}
		is.clock.Advance(jwksMinRefresh)
		v.verify(context.Background(), is.mint(t, mintOpts{kid: "k9"}))
		if got := is.jwksHits.Load(); got != 3 {
			t.Errorf("JWKS fetched %d times after the interval, want 3", got)
		}
	})

	t.Run("keys are refreshed after the maximum age", func(t *testing.T) {
		t.Parallel()
		is := newTestIssuer(t, newFakeClock())
		v := newTestVerifier(t, is, testOAuthConfig(is))
		v.verify(context.Background(), is.mint(t, mintOpts{}))
		is.clock.Advance(jwksMaxAge)
		if _, err := v.verify(context.Background(), is.mint(t, mintOpts{})); err != nil {
			t.Fatalf("verify: %v", err)
		}
		if got := is.jwksHits.Load(); got != 2 {
			t.Errorf("JWKS fetched %d times, want 2", got)
		}
	})

	t.Run("last good keys survive an outage", func(t *testing.T) {
		t.Parallel()
		is := newTestIssuer(t, newFakeClock())
		v := newTestVerifier(t, is, testOAuthConfig(is))
		if _, err := v.verify(context.Background(), is.mint(t, mintOpts{})); err != nil {
			t.Fatalf("verify: %v", err)
		}
		is.setDown(true)
		is.clock.Advance(jwksMaxAge + time.Minute)
		if _, err := v.verify(context.Background(), is.mint(t, mintOpts{})); err != nil {
			t.Fatalf("verify during the outage: %v", err)
		}
	})

	t.Run("issuer unreachable before the first fetch", func(t *testing.T) {
		t.Parallel()
		is := newTestIssuer(t, newFakeClock())
		is.setDown(true)
		v := newTestVerifier(t, is, testOAuthConfig(is))
		token := is.mint(t, mintOpts{})
		if _, err := v.verify(context.Background(), token); !errors.Is(err, errKeysUnavailable) {
			t.Fatalf("err = %v, want errKeysUnavailable", err)
		}
		is.setDown(false)
		is.clock.Advance(jwksMinRefresh)
		if _, err := v.verify(context.Background(), token); err != nil {
			t.Fatalf("verify after recovery: %v", err)
		}
	})

	t.Run("a slow refresh does not hold up requests with cached keys", func(t *testing.T) {
		t.Parallel()
		is := newTestIssuer(t, newFakeClock())
		is.addKey("k9", jose.RS256, testRSAKeys()[1], false)
		v := newTestVerifier(t, is, testOAuthConfig(is))
		cached, unknown := is.mint(t, mintOpts{}), is.mint(t, mintOpts{kid: "k9"})
		if _, err := v.verify(context.Background(), cached); err != nil {
			t.Fatalf("k1: %v", err)
		}
		is.clock.Advance(jwksMinRefresh)
		entered, release := is.holdJWKS(t)

		unknownDone := make(chan error, 1)
		go func() {
			_, err := v.verify(context.Background(), unknown)
			unknownDone <- err
		}()
		<-entered

		cachedDone := make(chan error, 1)
		go func() {
			_, err := v.verify(context.Background(), cached)
			cachedDone <- err
		}()
		select {
		case err := <-cachedDone:
			if err != nil {
				t.Fatalf("cached key during a refresh: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a request with a cached key waited for the JWKS refresh")
		}
		release()
		if err := <-unknownDone; !errors.Is(err, errInvalidToken) {
			t.Fatalf("unknown kid: err = %v, want errInvalidToken", err)
		}
	})

	t.Run("redirects are refused", func(t *testing.T) {
		t.Parallel()
		is := newTestIssuer(t, newFakeClock())
		is.configure(func(is *testIssuer) { is.redirectMetadata = true })
		client := newAuthHTTPClient()
		client.Transport = is.srv.Client().Transport
		v, err := newTokenVerifier(testOAuthConfig(is), client, is.clock.Now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := v.verify(context.Background(), is.mint(t, mintOpts{})); !errors.Is(err, errKeysUnavailable) {
			t.Fatalf("err = %v, want errKeysUnavailable", err)
		}
		if client.Timeout != authFetchTimeout {
			t.Errorf("client timeout = %v, want %v", client.Timeout, authFetchTimeout)
		}
	})

	t.Run("an http jwks_uri is rejected", func(t *testing.T) {
		t.Parallel()
		is := newTestIssuer(t, newFakeClock())
		plain := httptest.NewServer(is.srv.Config.Handler)
		t.Cleanup(plain.Close)
		is.configure(func(is *testIssuer) { is.jwksURI = plain.URL + "/jwks" })
		v := newTestVerifier(t, is, testOAuthConfig(is))
		if _, err := v.verify(context.Background(), is.mint(t, mintOpts{})); !errors.Is(err, errKeysUnavailable) {
			t.Fatalf("err = %v, want errKeysUnavailable", err)
		}
	})

	t.Run("encryption keys are not signing keys", func(t *testing.T) {
		t.Parallel()
		is := newTestIssuer(t, newFakeClock())
		is.configure(func(is *testIssuer) { is.keyUse = "enc" })
		v := newTestVerifier(t, is, testOAuthConfig(is))
		if _, err := v.verify(context.Background(), is.mint(t, mintOpts{})); !errors.Is(err, errKeysUnavailable) {
			t.Fatalf("err = %v, want errKeysUnavailable", err)
		}
	})

	t.Run("OIDC discovery fallback", func(t *testing.T) {
		t.Parallel()
		is := newTestIssuer(t, newFakeClock())
		is.mu.Lock()
		is.oidcOnly = true
		is.mu.Unlock()
		v := newTestVerifier(t, is, testOAuthConfig(is))
		if _, err := v.verify(context.Background(), is.mint(t, mintOpts{})); err != nil {
			t.Fatalf("verify: %v", err)
		}
	})

	t.Run("metadata for another issuer is rejected", func(t *testing.T) {
		t.Parallel()
		is := newTestIssuer(t, newFakeClock())
		is.mu.Lock()
		is.metadataIssuer = "https://evil.example"
		is.mu.Unlock()
		v := newTestVerifier(t, is, testOAuthConfig(is))
		if _, err := v.verify(context.Background(), is.mint(t, mintOpts{})); !errors.Is(err, errKeysUnavailable) {
			t.Fatalf("err = %v, want errKeysUnavailable", err)
		}
	})
}

func newTestGate(t *testing.T, is *testIssuer, writes bool) *oauthGate {
	t.Helper()
	g, err := newOAuthGate(testOAuthConfig(is), writes, is.srv.Client(), is.clock.Now)
	if err != nil {
		t.Fatalf("newOAuthGate: %v", err)
	}
	return g
}

type gateResult struct {
	code      int
	challenge string
	body      string
	reached   bool
	access    access
}

func callGate(t *testing.T, h http.Handler, authorization string) gateResult {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := gateResult{code: rec.Code, challenge: rec.Header().Get("WWW-Authenticate"), body: rec.Body.String()}
	if rec.Header().Get("X-Reached") != "" {
		res.reached = true
		res.access.write = rec.Header().Get("X-Reached") == "write"
	}
	return res
}

func reachedHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := accessFrom(r.Context())
		switch {
		case !ok:
			w.Header().Set("X-Reached", "no-access")
		case a.write:
			w.Header().Set("X-Reached", "write")
		default:
			w.Header().Set("X-Reached", "read")
		}
	})
}

func TestOAuthGate_KI07(t *testing.T) {
	t.Parallel()

	const metadata = `resource_metadata="https://mcp.example.test/.well-known/oauth-protected-resource/mcp"`

	t.Run("no token: 401 challenge without an error code", func(t *testing.T) {
		t.Parallel()
		is := newTestIssuer(t, newFakeClock())
		h := newTestGate(t, is, true).protect(reachedHandler(), false)
		for _, authz := range []string{"", "Basic b3duZXI6cHc=", "Bearer ", "Bearer"} {
			res := callGate(t, h, authz)
			if res.code != http.StatusUnauthorized || res.reached {
				t.Fatalf("%q: status %d reached %v, want 401", authz, res.code, res.reached)
			}
			if !strings.HasPrefix(res.challenge, "Bearer ") || !strings.Contains(res.challenge, metadata) ||
				!strings.Contains(res.challenge, `scope="things.read things.write"`) || strings.Contains(res.challenge, "error=") {
				t.Errorf("%q: WWW-Authenticate = %q", authz, res.challenge)
			}
		}
	})

	t.Run("read-only server advertises things.read only", func(t *testing.T) {
		t.Parallel()
		is := newTestIssuer(t, newFakeClock())
		res := callGate(t, newTestGate(t, is, false).protect(reachedHandler(), false), "")
		if !strings.Contains(res.challenge, `scope="things.read"`) {
			t.Errorf("WWW-Authenticate = %q", res.challenge)
		}
	})

	t.Run("invalid token: 401 invalid_token, token never echoed", func(t *testing.T) {
		t.Parallel()
		is := newTestIssuer(t, newFakeClock())
		token := is.mint(t, mintOpts{claims: func(c map[string]any) { c["sub"] = "someone-else" }})
		res := callGate(t, newTestGate(t, is, true).protect(reachedHandler(), false), "Bearer "+token)
		if res.code != http.StatusUnauthorized || res.reached {
			t.Fatalf("status %d reached %v, want 401", res.code, res.reached)
		}
		if !strings.Contains(res.challenge, `error="invalid_token"`) || !strings.Contains(res.challenge, metadata) {
			t.Errorf("WWW-Authenticate = %q", res.challenge)
		}
		if strings.Contains(res.challenge+res.body, token) || strings.Contains(res.challenge+res.body, "someone-else") {
			t.Error("response echoes the token or its claims")
		}
	})

	t.Run("insufficient scope: 403", func(t *testing.T) {
		t.Parallel()
		is := newTestIssuer(t, newFakeClock())
		g := newTestGate(t, is, true)
		cases := []struct {
			scope     string
			needWrite bool
			want      string
		}{
			{"", false, `scope="things.read"`},
			{"openid profile", false, `scope="things.read"`},
			{"things.write", false, `scope="things.read"`},
			{"things.read", true, `scope="things.read things.write"`},
		}
		for _, c := range cases {
			token := is.mint(t, mintOpts{claims: func(m map[string]any) { m["scope"] = c.scope }})
			res := callGate(t, g.protect(reachedHandler(), c.needWrite), "Bearer "+token)
			if res.code != http.StatusForbidden || res.reached {
				t.Fatalf("scope %q needWrite %v: status %d reached %v, want 403", c.scope, c.needWrite, res.code, res.reached)
			}
			if !strings.Contains(res.challenge, `error="insufficient_scope"`) || !strings.Contains(res.challenge, c.want) {
				t.Errorf("scope %q: WWW-Authenticate = %q, want %s", c.scope, res.challenge, c.want)
			}
		}
	})

	t.Run("valid tokens reach the handler with their access", func(t *testing.T) {
		t.Parallel()
		is := newTestIssuer(t, newFakeClock())
		g := newTestGate(t, is, true)
		read := is.mint(t, mintOpts{claims: func(m map[string]any) { m["scope"] = "things.read" }})
		write := is.mint(t, mintOpts{})

		if res := callGate(t, g.protect(reachedHandler(), false), "Bearer "+read); res.code != http.StatusOK || !res.reached || res.access.write {
			t.Errorf("read token: %+v", res)
		}
		if res := callGate(t, g.protect(reachedHandler(), false), "bearer "+read); res.code != http.StatusOK || !res.reached {
			t.Errorf("lower-case scheme: %+v", res)
		}
		if res := callGate(t, g.protect(reachedHandler(), true), "Bearer "+write); res.code != http.StatusOK || !res.access.write {
			t.Errorf("write token on a write route: %+v", res)
		}
	})

	t.Run("issuer unavailable: 503, not 401", func(t *testing.T) {
		t.Parallel()
		is := newTestIssuer(t, newFakeClock())
		is.setDown(true)
		res := callGate(t, newTestGate(t, is, true).protect(reachedHandler(), false), "Bearer "+is.mint(t, mintOpts{}))
		if res.code != http.StatusServiceUnavailable || res.reached {
			t.Fatalf("status %d reached %v, want 503", res.code, res.reached)
		}
	})
}

func TestLocalGate_KI07(t *testing.T) {
	t.Parallel()

	tests := []struct {
		host, origin string
		want         int
	}{
		{"127.0.0.1:8080", "", http.StatusOK},
		{"localhost:8080", "", http.StatusOK},
		{"[::1]:8080", "", http.StatusOK},
		{"127.0.0.1:8080", "http://localhost:3000", http.StatusOK},
		// DNS rebinding: a page on evil.example whose name now resolves to 127.0.0.1.
		{"evil.example:8080", "http://evil.example:8080", http.StatusForbidden},
		{"evil.example:8080", "", http.StatusForbidden},
		{"127.0.0.1:8080", "http://evil.example", http.StatusForbidden},
		{"127.0.0.1:8080", "null", http.StatusForbidden},
	}
	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
		req.Host = tt.host
		if tt.origin != "" {
			req.Header.Set("Origin", tt.origin)
		}
		rec := httptest.NewRecorder()
		localGate{}.protect(reachedHandler(), true).ServeHTTP(rec, req)
		if rec.Code != tt.want {
			t.Errorf("Host %q Origin %q: status %d, want %d", tt.host, tt.origin, rec.Code, tt.want)
		}
		if tt.want == http.StatusOK && rec.Header().Get("X-Reached") != "write" {
			t.Errorf("Host %q: handler not reached with full access", tt.host)
		}
	}
}

func TestProtectedResourceMetadata_KI07(t *testing.T) {
	t.Parallel()

	is := newTestIssuer(t, newFakeClock())
	for _, writes := range []bool{true, false} {
		mux := http.NewServeMux()
		newTestGate(t, is, writes).registerPublic(mux)
		for _, path := range []string{"/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource"} {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s: status %d", path, rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("GET %s: Content-Type %q", path, ct)
			}
			var doc struct {
				Resource             string   `json:"resource"`
				AuthorizationServers []string `json:"authorization_servers"`
				ScopesSupported      []string `json:"scopes_supported"`
				BearerMethods        []string `json:"bearer_methods_supported"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
				t.Fatalf("decode: %v", err)
			}
			wantScopes := []string{scopeRead}
			if writes {
				wantScopes = append(wantScopes, scopeWrite)
			}
			if doc.Resource != testResource || !slices.Equal(doc.AuthorizationServers, []string{is.srv.URL}) ||
				!slices.Equal(doc.ScopesSupported, wantScopes) || !slices.Equal(doc.BearerMethods, []string{"header"}) {
				t.Errorf("writes=%v GET %s: %+v", writes, path, doc)
			}
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/.well-known/oauth-protected-resource/mcp", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST metadata: status %d, want 405", rec.Code)
		}
	}
}

func TestLoadAuthConfig_KI07(t *testing.T) {
	t.Parallel()

	full := map[string]string{
		"OAUTH_ISSUER":          "https://auth.example.com",
		"MCP_PUBLIC_URL":        "https://mcp.example.com/mcp",
		"OAUTH_ALLOWED_SUBJECT": "a1b2c3",
	}
	with := func(base map[string]string, kv ...string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}

	tests := []struct {
		name    string
		env     map[string]string
		want    authConfig
		wantErr string
	}{
		{
			name: "oauth",
			env:  full,
			want: authConfig{OAuth: oauthConfig{Issuer: "https://auth.example.com", Resource: "https://mcp.example.com/mcp", Subject: "a1b2c3"}},
		},
		{
			name: "oauth with allowed clients",
			env:  with(full, "OAUTH_ALLOWED_CLIENTS", " claude , ,https://chatgpt.example/client "),
			want: authConfig{OAuth: oauthConfig{Issuer: "https://auth.example.com", Resource: "https://mcp.example.com/mcp", Subject: "a1b2c3",
				Clients: []string{"claude", "https://chatgpt.example/client"}}},
		},
		{
			name: "local only",
			env:  map[string]string{"INSECURE_NO_AUTH_LOCAL_ONLY": "true"},
			want: authConfig{LocalOnly: true},
		},
		{name: "nothing configured", env: map[string]string{}, wantErr: "INSECURE_NO_AUTH_LOCAL_ONLY"},
		{name: "local only false", env: map[string]string{"INSECURE_NO_AUTH_LOCAL_ONLY": "false"}, wantErr: "OAUTH_ISSUER"},
		{name: "local only not a bool", env: map[string]string{"INSECURE_NO_AUTH_LOCAL_ONLY": "yes"}, wantErr: "INSECURE_NO_AUTH_LOCAL_ONLY"},
		{name: "local only with oauth", env: with(full, "INSECURE_NO_AUTH_LOCAL_ONLY", "true"), wantErr: "cannot be combined"},
		{name: "API_KEY is gone", env: with(full, "API_KEY", "s3cret"), wantErr: "API_KEY"},
		{name: "missing subject", env: with(full, "OAUTH_ALLOWED_SUBJECT", ""), wantErr: "OAUTH_ALLOWED_SUBJECT"},
		{name: "missing public URL", env: with(full, "MCP_PUBLIC_URL", ""), wantErr: "MCP_PUBLIC_URL"},
		{name: "clients only", env: map[string]string{"OAUTH_ALLOWED_CLIENTS": "claude"}, wantErr: "OAUTH_ISSUER"},
		{name: "http issuer", env: with(full, "OAUTH_ISSUER", "http://auth.example.com"), wantErr: "OAUTH_ISSUER"},
		{name: "issuer with query", env: with(full, "OAUTH_ISSUER", "https://auth.example.com?x=1"), wantErr: "OAUTH_ISSUER"},
		{name: "issuer with credentials", env: with(full, "OAUTH_ISSUER", "https://u:p@auth.example.com"), wantErr: "OAUTH_ISSUER"},
		{name: "http public URL", env: with(full, "MCP_PUBLIC_URL", "http://mcp.example.com/mcp"), wantErr: "MCP_PUBLIC_URL"},
		{name: "public URL without /mcp", env: with(full, "MCP_PUBLIC_URL", "https://mcp.example.com"), wantErr: "/mcp"},
		{name: "public URL with another path", env: with(full, "MCP_PUBLIC_URL", "https://mcp.example.com/things/mcp"), wantErr: "/mcp"},
		{name: "public URL with fragment", env: with(full, "MCP_PUBLIC_URL", "https://mcp.example.com/mcp#x"), wantErr: "MCP_PUBLIC_URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := loadAuthConfig(func(k string) string { return tt.env[k] })
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want one mentioning %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("loadAuthConfig: %v", err)
			}
			if got.LocalOnly != tt.want.LocalOnly || got.OAuth.Issuer != tt.want.OAuth.Issuer || got.OAuth.Resource != tt.want.OAuth.Resource ||
				got.OAuth.Subject != tt.want.OAuth.Subject || !slices.Equal(got.OAuth.Clients, tt.want.OAuth.Clients) {
				t.Errorf("loadAuthConfig = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestListenAddr_KI07_LocalOnlyBindsLoopback(t *testing.T) {
	t.Parallel()

	if got := listenAddr(authConfig{LocalOnly: true}, "8080"); got != "127.0.0.1:8080" {
		t.Errorf("local only: %q", got)
	}
	if got := listenAddr(authConfig{OAuth: oauthConfig{Issuer: "https://a"}}, "8080"); got != ":8080" {
		t.Errorf("oauth: %q", got)
	}
}

func TestParseSwitch(t *testing.T) {
	t.Parallel()

	if v, err := parseSwitch("ENABLE_REST_API", ""); v || err != nil {
		t.Errorf("empty: %v %v", v, err)
	}
	if v, err := parseSwitch("ENABLE_REST_API", "true"); !v || err != nil {
		t.Errorf("true: %v %v", v, err)
	}
	if _, err := parseSwitch("ENABLE_REST_API", "on"); err == nil || !strings.Contains(err.Error(), "ENABLE_REST_API") {
		t.Errorf("on: error %v should name the variable", err)
	}
}
