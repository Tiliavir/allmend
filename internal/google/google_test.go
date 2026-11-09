package google

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fake struct {
	key     *rsa.PrivateKey
	srv     *httptest.Server
	claims  map[string]any
	gotPKCE string
}

func newFake(t *testing.T) *fake {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fake{key: k}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /certs", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.gotPKCE = r.PostForm.Get("code_verifier")
		if r.PostForm.Get("code") != "good-code" {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": f.sign(t, "k1", f.claims)})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fake) sign(t *testing.T, kid string, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"})
	p, _ := json.Marshal(claims)
	in := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p)
	sum := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return in + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (f *fake) provider() *Provider {
	p := New("client-1", "secret", "https://allmend.example/login/google/callback")
	p.TokenURL, p.JWKSURL, p.HTTP = f.srv.URL+"/token", f.srv.URL+"/certs", f.srv.Client()
	return p
}

func goodClaims() map[string]any {
	now := time.Now().Unix()
	return map[string]any{"iss": "https://accounts.google.com", "aud": "client-1", "sub": "1234",
		"email": "a@example.org", "email_verified": true, "exp": now + 3600, "iat": now, "nonce": "n1"}
}

func TestLogin(t *testing.T) {
	f := newFake(t)
	f.claims = goodClaims()
	c, err := f.provider().Login(context.Background(), "good-code", "ver", "n1")
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject != "1234" || c.Email != "a@example.org" || !c.EmailVerified {
		t.Fatalf("claims %+v", c)
	}
	if f.gotPKCE != "ver" {
		t.Fatalf("code_verifier not sent")
	}
}

func TestRejects(t *testing.T) {
	f := newFake(t)
	p := f.provider()
	mod := func(k string, v any) map[string]any { c := goodClaims(); c[k] = v; return c }
	cases := map[string]string{
		"issuer":   f.sign(t, "k1", mod("iss", "https://evil.example")),
		"audience": f.sign(t, "k1", mod("aud", "other")),
		"expired":  f.sign(t, "k1", mod("exp", time.Now().Add(-time.Hour).Unix())),
		"nonce":    f.sign(t, "k1", mod("nonce", "other")),
		"kid":      f.sign(t, "unknown", goodClaims()),
	}
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	forged := &fake{key: other}
	cases["signature"] = forged.sign(t, "k1", goodClaims())
	for name, tok := range cases {
		if _, err := p.Verify(context.Background(), tok, "n1"); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
	if _, err := p.Login(context.Background(), "bad-code", "v", "n1"); err == nil {
		t.Error("bad code accepted")
	}
}
