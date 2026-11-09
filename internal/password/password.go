// Package password hashes and verifies passwords with argon2id (PHC format,
// compatible with hashes made by the old almanach hashpw tool).
package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	time    uint32 = 3
	memory  uint32 = 64 * 1024
	threads uint8  = 4
	keyLen  uint32 = 32
	// MinLength is the minimum accepted password length.
	MinLength = 12
)

// ErrMismatch is returned when the password does not match.
var ErrMismatch = errors.New("password mismatch")

// Hash returns a PHC-encoded argon2id hash.
func Hash(pw string) string {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	key := argon2.IDKey([]byte(pw), salt, time, memory, threads, keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, memory, time, threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

// Verify checks pw against encoded. A malformed hash is an error, not a mismatch.
func Verify(pw, encoded string) error {
	p, err := parse(encoded)
	if err != nil {
		return err
	}
	got := argon2.IDKey([]byte(pw), p.salt, p.t, p.m, p.p, uint32(len(p.key))) //nolint:gosec // key length is 16..64 by parse
	if subtle.ConstantTimeCompare(got, p.key) != 1 {
		return ErrMismatch
	}
	return nil
}

// Valid reports whether encoded is a well-formed argon2id hash.
func Valid(encoded string) error {
	_, err := parse(encoded)
	return err
}

type params struct {
	m, t      uint32
	p         uint8
	salt, key []byte
}

func parse(encoded string) (params, error) {
	var out params
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return out, errors.New("password: not an argon2id v19 PHC hash")
	}
	seen := map[string]bool{}
	for _, kv := range strings.Split(parts[3], ",") {
		k, v, ok := strings.Cut(kv, "=")
		n, err := strconv.ParseUint(v, 10, 32)
		if !ok || err != nil || n == 0 || seen[k] {
			return out, fmt.Errorf("password: bad parameter %q", kv)
		}
		seen[k] = true
		switch k {
		case "m":
			out.m = uint32(n)
		case "t":
			out.t = uint32(n)
		case "p":
			if n > 255 {
				return out, errors.New("password: p out of range")
			}
			out.p = uint8(n)
		default:
			return out, fmt.Errorf("password: unknown parameter %q", k)
		}
	}
	if len(seen) != 3 {
		return out, errors.New("password: m, t and p are required")
	}
	var err error
	if out.salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil || len(out.salt) < 8 {
		return out, errors.New("password: bad salt")
	}
	if out.key, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil || len(out.key) < 16 || len(out.key) > 64 {
		return out, errors.New("password: bad key")
	}
	return out, nil
}
