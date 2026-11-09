// Package google implements "Sign in with Google" (OpenID Connect,
// authorization code flow with PKCE) using only the standard library.
package google

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// Google's endpoints (https://accounts.google.com/.well-known/openid-configuration).
const (
	AuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	TokenURL = "https://oauth2.googleapis.com/token" //nolint:gosec // a URL, not a credential
	JWKSURL  = "https://www.googleapis.com/oauth2/v3/certs"
)

// Issuers accepted in the "iss" claim.
var Issuers = []string{"https://accounts.google.com", "accounts.google.com"}

// Provider talks to Google. Endpoint fields default to Google's; tests set them.
type Provider struct {
	ClientID, ClientSecret, RedirectURL string
	AuthURL, TokenURL, JWKSURL          string
	Issuers                             []string
	HTTP                                *http.Client
	Now                                 func() time.Time

	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	fetched time.Time
}

// New returns a Provider for Google with the given client.
func New(clientID, secret, redirect string) *Provider {
	return &Provider{
		ClientID: clientID, ClientSecret: secret, RedirectURL: redirect,
		AuthURL: AuthURL, TokenURL: TokenURL, JWKSURL: JWKSURL, Issuers: Issuers,
		HTTP: &http.Client{Timeout: 10 * time.Second},
	}
}

func (p *Provider) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// AuthCodeURL is where the browser is sent to log in.
func (p *Provider) AuthCodeURL(state, nonce, verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"client_id":             {p.ClientID},
		"redirect_uri":          {p.RedirectURL},
		"response_type":         {"code"},
		"scope":                 {"openid email profile"},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		"prompt":                {"select_account"},
	}
	return p.AuthURL + "?" + q.Encode()
}

// Claims are the ID token fields Allmend uses.
type Claims struct {
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	Issuer        string `json:"iss"`
	Audience      string `json:"aud"`
	Expiry        int64  `json:"exp"`
	IssuedAt      int64  `json:"iat"`
	Nonce         string `json:"nonce"`
}

// ErrInvalid wraps every reason an ID token is rejected.
var ErrInvalid = errors.New("google: invalid id token")

// Login exchanges the code and returns the verified claims.
func (p *Provider) Login(ctx context.Context, code, verifier, nonce string) (Claims, error) {
	raw, err := p.exchange(ctx, code, verifier)
	if err != nil {
		return Claims{}, err
	}
	return p.Verify(ctx, raw, nonce)
}

func (p *Provider) exchange(ctx context.Context, code, verifier string) (string, error) {
	form := url.Values{
		"code": {code}, "client_id": {p.ClientID}, "client_secret": {p.ClientSecret},
		"redirect_uri": {p.RedirectURL}, "grant_type": {"authorization_code"}, "code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("google: token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("google: token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("google: token endpoint answered %d", resp.StatusCode)
	}
	var tr struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tr); err != nil || tr.IDToken == "" {
		return "", errors.New("google: token response without id_token")
	}
	return tr.IDToken, nil
}

// Verify checks signature (RS256), issuer, audience, expiry and nonce.
func (p *Provider) Verify(ctx context.Context, raw, nonce string) (Claims, error) {
	var c Claims
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return c, fmt.Errorf("%w: not a JWS", ErrInvalid)
	}
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return c, fmt.Errorf("%w: header", ErrInvalid)
	}
	var h struct{ Alg, Kid, Typ string }
	if err := json.Unmarshal(hb, &h); err != nil || h.Alg != "RS256" || h.Kid == "" {
		return c, fmt.Errorf("%w: header must be RS256 with kid", ErrInvalid)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return c, fmt.Errorf("%w: signature encoding", ErrInvalid)
	}
	key, err := p.key(ctx, h.Kid)
	if err != nil {
		return c, err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig); err != nil {
		return c, fmt.Errorf("%w: signature", ErrInvalid)
	}
	pb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return c, fmt.Errorf("%w: payload", ErrInvalid)
	}
	if err := json.Unmarshal(pb, &c); err != nil {
		return c, fmt.Errorf("%w: payload json (aud must be a single string)", ErrInvalid)
	}
	now := p.now().Unix()
	const skew = 60
	switch {
	case !slices.Contains(p.Issuers, c.Issuer):
		return c, fmt.Errorf("%w: issuer", ErrInvalid)
	case c.Audience != p.ClientID:
		return c, fmt.Errorf("%w: audience", ErrInvalid)
	case c.Expiry+skew < now:
		return c, fmt.Errorf("%w: expired", ErrInvalid)
	case c.IssuedAt-skew > now:
		return c, fmt.Errorf("%w: issued in the future", ErrInvalid)
	case nonce == "" || c.Nonce != nonce:
		return c, fmt.Errorf("%w: nonce", ErrInvalid)
	case c.Subject == "":
		return c, fmt.Errorf("%w: subject", ErrInvalid)
	}
	return c, nil
}

// key returns the signing key for kid, refetching the JWKS when it is
// unknown (key rotation) but at most once a minute.
func (p *Provider) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if k, ok := p.keys[kid]; ok {
		return k, nil
	}
	if p.now().Sub(p.fetched) < time.Minute && p.keys != nil {
		return nil, fmt.Errorf("%w: unknown key", ErrInvalid)
	}
	keys, err := p.fetchKeys(ctx)
	if err != nil {
		return nil, err
	}
	p.keys, p.fetched = keys, p.now()
	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("%w: unknown key", ErrInvalid)
}

func (p *Provider) fetchKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.JWKSURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google: jwks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google: jwks answered %d", resp.StatusCode)
	}
	var set struct {
		Keys []struct{ Kty, Kid, Alg, Use, N, E string }
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return nil, fmt.Errorf("google: jwks json: %w", err)
	}
	out := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" || (k.Use != "" && k.Use != "sig") {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil || len(e) > 4 {
			return nil, fmt.Errorf("google: jwks key %q malformed", k.Kid)
		}
		out[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	if len(out) == 0 {
		return nil, errors.New("google: jwks without RSA keys")
	}
	return out, nil
}
