package password

import (
	"errors"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	h := Hash("correct horse battery")
	if err := Verify("correct horse battery", h); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := Verify("wrong", h); !errors.Is(err, ErrMismatch) {
		t.Fatalf("want mismatch, got %v", err)
	}
}

func TestMalformed(t *testing.T) {
	for _, h := range []string{"", "plain", "$argon2i$v=19$m=1,t=1,p=1$c2FsdHNhbHQ$a2V5a2V5a2V5a2V5a2V5", "$argon2id$v=19$m=1,t=1$c2FsdHNhbHQ$a2V5a2V5a2V5a2V5a2V5", "$argon2id$v=19$m=1,t=1,p=1,x=2$c2FsdHNhbHQ$a2V5a2V5a2V5a2V5a2V5"} {
		if err := Valid(h); err == nil {
			t.Errorf("Valid(%q) = nil, want error", h)
		}
	}
}
