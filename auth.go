package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Identity is the authenticated identity carried by an OIDC token.
type Identity struct {
	Subject   string
	Email     string
	ExpiresAt time.Time
}

// Authenticator verifies a caller-supplied OIDC JWT.
type Authenticator interface {
	Authenticate(context.Context, string) (Identity, error)
}

const (
	oidcHTTPTimeout      = 10 * time.Second
	maxOIDCResponseBytes = 1 << 20
	maxOIDCRedirects     = 5
)

var errOIDCResponseTooLarge = errors.New("OIDC response exceeds size limit")

type boundedOIDCBody struct {
	body      io.ReadCloser
	remaining int64
}

func (b *boundedOIDCBody) Read(payload []byte) (int, error) {
	if len(payload) == 0 {
		return 0, nil
	}
	if b.remaining == 0 {
		var probe [1]byte
		count, err := b.body.Read(probe[:])
		if count > 0 {
			return 0, errOIDCResponseTooLarge
		}
		return 0, err
	}
	if int64(len(payload)) > b.remaining {
		payload = payload[:b.remaining]
	}
	count, err := b.body.Read(payload)
	b.remaining -= int64(count)
	return count, err
}

func (b *boundedOIDCBody) Close() error { return b.body.Close() }

type boundedOIDCTransport struct {
	base http.RoundTripper
}

func (t *boundedOIDCTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL == nil || request.URL.Scheme != "https" {
		return nil, errors.New("OIDC endpoint must use HTTPS")
	}
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if response.ContentLength > maxOIDCResponseBytes {
		_ = response.Body.Close()
		return nil, errOIDCResponseTooLarge
	}
	response.Body = &boundedOIDCBody{body: response.Body, remaining: maxOIDCResponseBytes}
	return response, nil
}

func hardenedOIDCClient(ctx context.Context) *http.Client {
	base, _ := ctx.Value(oauth2.HTTPClient).(*http.Client)
	client := &http.Client{}
	if base != nil {
		*client = *base
	}
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = &boundedOIDCTransport{base: transport}
	if client.Timeout <= 0 || client.Timeout > oidcHTTPTimeout {
		client.Timeout = oidcHTTPTimeout
	}
	previousRedirectPolicy := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL == nil || request.URL.Scheme != "https" || request.URL.Host == "" || request.URL.User != nil {
			return errors.New("OIDC redirect target must be an absolute HTTPS URL without userinfo")
		}
		if len(via) >= maxOIDCRedirects {
			return errors.New("OIDC redirect limit exceeded")
		}
		if previousRedirectPolicy != nil {
			return previousRedirectPolicy(request, via)
		}
		return nil
	}
	return client
}

type oidcAuthenticator struct {
	verifier *oidc.IDTokenVerifier
}

func newOIDCAuthenticator(ctx context.Context, issuer, audience, jwksURL string) (Authenticator, error) {
	if ctx == nil {
		return nil, fmt.Errorf("OIDC auth: nil context")
	}
	if err := validateOIDCIssuer(issuer); err != nil {
		return nil, fmt.Errorf("OIDC auth: %w", err)
	}
	if audience == "" || strings.TrimSpace(audience) != audience {
		return nil, fmt.Errorf("OIDC auth: audience must be nonempty and have no surrounding whitespace")
	}
	if jwksURL != "" {
		if err := validateJWKSURL(jwksURL); err != nil {
			return nil, fmt.Errorf("OIDC auth: %w", err)
		}
	}

	keySetContext := oidc.ClientContext(ctx, hardenedOIDCClient(ctx))
	verifierConfig := &oidc.Config{
		ClientID:             audience,
		SupportedSigningAlgs: []string{"RS256"},
	}
	if jwksURL != "" {
		keySet := oidc.NewRemoteKeySet(keySetContext, jwksURL)
		return &oidcAuthenticator{
			verifier: oidc.NewVerifier(issuer, keySet, verifierConfig),
		}, nil
	}

	provider, err := oidc.NewProvider(keySetContext, issuer)
	if err != nil {
		return nil, fmt.Errorf("OIDC auth: discover provider metadata: %w", err)
	}
	var metadata struct {
		JWKSURL string `json:"jwks_uri"`
	}
	if err := provider.Claims(&metadata); err != nil {
		return nil, fmt.Errorf("OIDC auth: read provider metadata: %w", err)
	}
	if err := validateJWKSURL(metadata.JWKSURL); err != nil {
		return nil, fmt.Errorf("OIDC auth: discovered %w", err)
	}
	keySet := oidc.NewRemoteKeySet(keySetContext, metadata.JWKSURL)
	return &oidcAuthenticator{verifier: oidc.NewVerifier(issuer, keySet, verifierConfig)}, nil
}

func (a *oidcAuthenticator) Authenticate(ctx context.Context, rawToken string) (Identity, error) {
	if a == nil || a.verifier == nil {
		return Identity{}, fmt.Errorf("OIDC auth: authenticator is not initialized")
	}
	if ctx == nil {
		return Identity{}, fmt.Errorf("OIDC auth: nil context")
	}
	if strings.TrimSpace(rawToken) == "" {
		return Identity{}, fmt.Errorf("OIDC auth: empty token")
	}

	token, err := a.verifier.Verify(ctx, rawToken)
	if err != nil {
		return Identity{}, fmt.Errorf("OIDC auth: verify token: %w", err)
	}
	if strings.TrimSpace(token.Subject) == "" {
		return Identity{}, fmt.Errorf("OIDC auth: token subject is empty")
	}
	if token.Expiry.IsZero() {
		return Identity{}, fmt.Errorf("OIDC auth: token expiry is missing")
	}

	var claims struct {
		Email string `json:"email"`
	}
	if err := token.Claims(&claims); err != nil {
		return Identity{}, fmt.Errorf("OIDC auth: read token claims: %w", err)
	}

	return Identity{
		Subject:   token.Subject,
		Email:     strings.ToLower(strings.TrimSpace(claims.Email)),
		ExpiresAt: token.Expiry,
	}, nil
}
