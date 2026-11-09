package env

import (
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	c, err := Load([]string{"ALLMEND_ADMIN_EMAIL=Me@Example.org", "ALLMEND_ADMIN_PASSWORD_HASH=x", "PATH=/bin"})
	if err != nil {
		t.Fatal(err)
	}
	if c.AdminEmail != "me@example.org" || c.Addr != ":8080" || !c.SecureCookie {
		t.Fatalf("%+v", c)
	}
}

func TestLoadErrors(t *testing.T) {
	cases := map[string][]string{
		"unknown":        {"ALLMEND_ADMIN_EMAIL=a@b.c", "ALLMEND_ADMIN_PASSWORD_HASH=x", "ALLMEND_TYPO=1"},
		"no admin":       {"ALLMEND_ADMIN_PASSWORD_HASH=x"},
		"no login":       {"ALLMEND_ADMIN_EMAIL=a@b.c"},
		"half google":    {"ALLMEND_ADMIN_EMAIL=a@b.c", "ALLMEND_GOOGLE_CLIENT_ID=x"},
		"google no tls":  {"ALLMEND_ADMIN_EMAIL=a@b.c", "ALLMEND_GOOGLE_CLIENT_ID=x", "ALLMEND_GOOGLE_CLIENT_SECRET=y", "ALLMEND_BASE_URL=http://nas.example"},
		"base with path": {"ALLMEND_ADMIN_EMAIL=a@b.c", "ALLMEND_ADMIN_PASSWORD_HASH=x", "ALLMEND_BASE_URL=https://x.example/app"},
		"bad bool":       {"ALLMEND_ADMIN_EMAIL=a@b.c", "ALLMEND_ADMIN_PASSWORD_HASH=x", "ALLMEND_SECURE_COOKIE=nope"},
	}
	for name, envs := range cases {
		if _, err := Load(envs); err == nil {
			t.Errorf("%s: want error", name)
		} else if !strings.HasPrefix(err.Error(), "env.Load") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
