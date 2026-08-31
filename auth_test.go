package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

type testOIDCIssuer struct {
	server        *httptest.Server
	key           *rsa.PrivateKey
	kid           string
	discoveryHits atomic.Int32
}

func newTestOIDCIssuer(t *testing.T) *testOIDCIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}

	issuer := &testOIDCIssuer{key: key, kid: "test-key"}
	mux := http.NewServeMux()
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{
				"kid": issuer.kid,
				"kty": "RSA",
				"alg": "RS256",
				"use": "sig",
				"n":   base64.RawURLEncoding.EncodeToString(issuer.key.N.Bytes()),
				"e":   "AQAB",
			}},
		})
	})
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		issuer.discoveryHits.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":   issuer.server.URL,
			"jwks_uri": issuer.server.URL + "/keys",
		})
	})
	issuer.server = httptest.NewTLSServer(mux)
	t.Cleanup(issuer.server.Close)
	return issuer
}

func (s *testOIDCIssuer) context() context.Context {
	return oidc.ClientContext(context.Background(), s.server.Client())
}

func (s *testOIDCIssuer) authenticate(t *testing.T, jwksURL string) Authenticator {
	t.Helper()
	authenticator, err := newOIDCAuthenticator(s.context(), s.server.URL, "audience-123", jwksURL)
	if err != nil {
		t.Fatalf("construct authenticator: %v", err)
	}
	return authenticator
}

func (s *testOIDCIssuer) sign(t *testing.T, claims map[string]any) string {
	t.Helper()
	return signOIDCToken(t, s.key, s.kid, "RS256", claims)
}

func signOIDCToken(t *testing.T, key *rsa.PrivateKey, kid, algorithm string, claims map[string]any) string {
	t.Helper()
	encode := func(value any) string {
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal JWT part: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(payload)
	}

	unsigned := encode(map[string]string{
		"alg": algorithm,
		"kid": kid,
		"typ": "JWT",
	}) + "." + encode(claims)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func oidcClaims(issuer, audience string, expiry time.Time) map[string]any {
	return map[string]any{
		"iss":   issuer,
		"sub":   "subject-123",
		"aud":   audience,
		"exp":   expiry.Unix(),
		"email": " User@Example.COM ",
	}
}

func forgeOIDCToken(t *testing.T, token string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT has %d parts", len(parts))
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode JWT signature: %v", err)
	}
	signature[0] ^= 0xff
	parts[2] = base64.RawURLEncoding.EncodeToString(signature)
	return strings.Join(parts, ".")
}

func TestOIDCAuthenticatorUsesExplicitJWKS(t *testing.T) {
	issuer := newTestOIDCIssuer(t)
	authenticator := issuer.authenticate(t, issuer.server.URL+"/keys")
	if issuer.discoveryHits.Load() != 0 {
		t.Fatal("explicit JWKS construction performed discovery")
	}

	expiry := time.Now().Add(5 * time.Minute).Truncate(time.Second)
	identity, err := authenticator.Authenticate(issuer.context(), issuer.sign(t, oidcClaims(issuer.server.URL, "audience-123", expiry)))
	if err != nil {
		t.Fatalf("authenticate signed token: %v", err)
	}
	if identity.Subject != "subject-123" || identity.Email != "user@example.com" || !identity.ExpiresAt.Equal(expiry) {
		t.Fatalf("unexpected identity: %+v", identity)
	}
}

func TestOIDCAuthenticatorUsesDiscoveryWhenJWKSUnset(t *testing.T) {
	issuer := newTestOIDCIssuer(t)
	authenticator := issuer.authenticate(t, "")
	if issuer.discoveryHits.Load() != 1 {
		t.Fatalf("discovery requests = %d, want 1", issuer.discoveryHits.Load())
	}
	token := issuer.sign(t, oidcClaims(issuer.server.URL, "audience-123", time.Now().Add(5*time.Minute)))
	if _, err := authenticator.Authenticate(issuer.context(), token); err != nil {
		t.Fatalf("authenticate discovered token: %v", err)
	}
}

func TestOIDCAuthenticatorRejectsInvalidTokens(t *testing.T) {
	issuer := newTestOIDCIssuer(t)
	authenticator := issuer.authenticate(t, issuer.server.URL+"/keys")
	validClaims := oidcClaims(issuer.server.URL, "audience-123", time.Now().Add(5*time.Minute))
	validToken := issuer.sign(t, validClaims)
	tests := []struct {
		name  string
		token func() string
	}{
		{name: "forged", token: func() string { return forgeOIDCToken(t, validToken) }},
		{name: "wrong issuer", token: func() string {
			return issuer.sign(t, oidcClaims("https://other.example", "audience-123", time.Now().Add(5*time.Minute)))
		}},
		{name: "wrong audience", token: func() string {
			return issuer.sign(t, oidcClaims(issuer.server.URL, "other-audience", time.Now().Add(5*time.Minute)))
		}},
		{name: "expired", token: func() string {
			return issuer.sign(t, oidcClaims(issuer.server.URL, "audience-123", time.Now().Add(-time.Minute)))
		}},
		{name: "unsupported algorithm", token: func() string {
			return signOIDCToken(t, issuer.key, issuer.kid, "HS256", validClaims)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := authenticator.Authenticate(issuer.context(), test.token()); err == nil {
				t.Fatalf("Authenticate accepted %s token", test.name)
			}
		})
	}
}

func TestOIDCAuthenticatorRequiresSubjectAndExpiry(t *testing.T) {
	issuer := newTestOIDCIssuer(t)
	authenticator := issuer.authenticate(t, issuer.server.URL+"/keys")
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "missing subject", mutate: func(claims map[string]any) { delete(claims, "sub") }},
		{name: "blank subject", mutate: func(claims map[string]any) { claims["sub"] = "  " }},
		{name: "missing expiry", mutate: func(claims map[string]any) { delete(claims, "exp") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			claims := oidcClaims(issuer.server.URL, "audience-123", time.Now().Add(5*time.Minute))
			test.mutate(claims)
			if _, err := authenticator.Authenticate(issuer.context(), issuer.sign(t, claims)); err == nil {
				t.Fatalf("Authenticate accepted token with %s", test.name)
			}
		})
	}
}

func TestOIDCAuthenticatorAllowsMissingEmail(t *testing.T) {
	issuer := newTestOIDCIssuer(t)
	authenticator := issuer.authenticate(t, issuer.server.URL+"/keys")
	claims := oidcClaims(issuer.server.URL, "audience-123", time.Now().Add(5*time.Minute))
	delete(claims, "email")
	identity, err := authenticator.Authenticate(issuer.context(), issuer.sign(t, claims))
	if err != nil {
		t.Fatalf("authenticate token without email: %v", err)
	}
	if identity.Email != "" {
		t.Fatalf("email = %q, want empty", identity.Email)
	}
}

func TestOIDCAuthenticatorRejectsInsecureDiscoveredJWKS(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":   server.URL,
			"jwks_uri": "http://keys.example.test/jwks",
		})
	}))
	t.Cleanup(server.Close)

	ctx := oidc.ClientContext(context.Background(), server.Client())
	authenticator, err := newOIDCAuthenticator(ctx, server.URL, "audience", "")
	if err == nil || authenticator != nil || !strings.Contains(err.Error(), "discovered OIDC JWKS URL") {
		t.Fatalf("newOIDCAuthenticator() = (%T, %v), want insecure discovered JWKS error", authenticator, err)
	}
}

func TestOIDCAuthenticatorRejectsHTTPSDowngradeRedirect(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://keys.example.test/insecure", http.StatusFound)
	}))
	t.Cleanup(server.Close)

	ctx := oidc.ClientContext(context.Background(), server.Client())
	authenticator, err := newOIDCAuthenticator(ctx, server.URL, "audience", "")
	if err == nil || authenticator != nil || !strings.Contains(err.Error(), "absolute HTTPS URL") {
		t.Fatalf("newOIDCAuthenticator() = (%T, %v), want HTTPS redirect error", authenticator, err)
	}
}

func TestOIDCAuthenticatorBoundsDiscoveryResponse(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxOIDCResponseBytes+1)))
	}))
	t.Cleanup(server.Close)

	ctx := oidc.ClientContext(context.Background(), server.Client())
	authenticator, err := newOIDCAuthenticator(ctx, server.URL, "audience", "")
	if err == nil || authenticator != nil || !strings.Contains(err.Error(), errOIDCResponseTooLarge.Error()) {
		t.Fatalf("newOIDCAuthenticator() = (%T, %v), want bounded response error", authenticator, err)
	}
}

func TestNewOIDCAuthenticatorRejectsMalformedConfig(t *testing.T) {
	for _, test := range []struct {
		name    string
		ctx     context.Context
		issuer  string
		aud     string
		jwksURL string
	}{
		{name: "nil context", ctx: nil, issuer: "https://issuer.example", aud: "audience"},
		{name: "empty issuer", ctx: context.Background(), issuer: "", aud: "audience"},
		{name: "relative issuer", ctx: context.Background(), issuer: "issuer.example", aud: "audience"},
		{name: "insecure issuer", ctx: context.Background(), issuer: "http://issuer.example", aud: "audience"},
		{name: "issuer query", ctx: context.Background(), issuer: "https://issuer.example?tenant=one", aud: "audience"},
		{name: "empty audience", ctx: context.Background(), issuer: "https://issuer.example", aud: ""},
		{name: "audience whitespace", ctx: context.Background(), issuer: "https://issuer.example", aud: " audience"},
		{name: "insecure JWKS", ctx: context.Background(), issuer: "https://issuer.example", aud: "audience", jwksURL: "http://keys.example/jwks"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if authenticator, err := newOIDCAuthenticator(test.ctx, test.issuer, test.aud, test.jwksURL); err == nil || authenticator != nil {
				t.Fatalf("newOIDCAuthenticator() = (%T, %v), want (nil, error)", authenticator, err)
			}
		})
	}
}

func TestOIDCAuthenticatorRejectsEmptyToken(t *testing.T) {
	issuer := newTestOIDCIssuer(t)
	authenticator := issuer.authenticate(t, issuer.server.URL+"/keys")
	if _, err := authenticator.Authenticate(issuer.context(), "   "); err == nil {
		t.Fatal("Authenticate accepted empty token")
	}
}
