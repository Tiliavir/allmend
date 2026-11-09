package account

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/tiliavir/allmend/internal/id"
)

// Hasher hashes and verifies passwords (internal/password).
type Hasher interface {
	Hash(pw string) string
	Verify(pw, encoded string) error
}

// Service implements account use cases.
type Service struct {
	Repo   Repository
	Hasher Hasher
	// AdminEmail and BreakGlassHash come from the environment.
	AdminEmail     string
	BreakGlassHash string
	Now            func() time.Time

	dummyOnce sync.Once
	dummyHash string
}

// Errors returned to the UI.
var (
	ErrLoginFailed  = errors.New("login failed")
	ErrForbidden    = errors.New("forbidden")
	ErrWeakPassword = errors.New("password too short")
	ErrSelf         = errors.New("admins cannot disable themselves")
)

// MinPasswordLength for local passwords.
const MinPasswordLength = 12

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// EnsureAdmin makes sure the configured first admin exists, is active and admin.
func (s *Service) EnsureAdmin(ctx context.Context) (Account, error) {
	a, err := s.Repo.ByEmail(ctx, s.AdminEmail)
	now := s.now()
	if errors.Is(err, ErrNotFound) {
		a = Account{ID: id.New(), Email: s.AdminEmail, IsAdmin: true, Status: Active, CreatedAt: now, UpdatedAt: now}
		for _, m := range Modules {
			a.Modules = append(a.Modules, m.ID)
		}
		return a, s.Repo.Insert(ctx, a)
	}
	if err != nil {
		return a, fmt.Errorf("account.EnsureAdmin: %w", err)
	}
	if a.IsAdmin && a.Status != Disabled {
		return a, nil
	}
	a.IsAdmin, a.UpdatedAt = true, now
	if a.Status == Disabled {
		a.Status = Active
	}
	return a, s.Repo.Update(ctx, a)
}

// GoogleIdentity is what a verified Google ID token says.
type GoogleIdentity struct {
	Subject, Email, Name string
	EmailVerified        bool
}

// LoginGoogle maps a verified Google identity to an account. An invited
// account is bound to the Google subject on its first login.
func (s *Service) LoginGoogle(ctx context.Context, g GoogleIdentity) (Account, error) {
	if g.Subject == "" || !g.EmailVerified {
		return Account{}, ErrLoginFailed
	}
	a, err := s.Repo.ByGoogleSub(ctx, g.Subject)
	if errors.Is(err, ErrNotFound) {
		email, eerr := NormalizeEmail(g.Email)
		if eerr != nil {
			return Account{}, ErrLoginFailed
		}
		a, err = s.Repo.ByEmail(ctx, email)
		if errors.Is(err, ErrNotFound) {
			return Account{}, ErrLoginFailed
		}
		if err == nil && a.GoogleSub != "" {
			return Account{}, ErrLoginFailed // address now belongs to another Google account
		}
		a.GoogleSub = g.Subject
	}
	if err != nil {
		return Account{}, fmt.Errorf("account.LoginGoogle: %w", err)
	}
	if a.Status == Disabled {
		return Account{}, ErrLoginFailed
	}
	changed := a.Status == Invited
	if a.Status == Invited {
		a.Status = Active
	}
	if a.Name == "" && g.Name != "" {
		a.Name, changed = g.Name, true
	}
	if changed || a.UpdatedAt.IsZero() {
		a.UpdatedAt = s.now()
	}
	if err := s.Repo.Update(ctx, a); err != nil {
		return Account{}, fmt.Errorf("account.LoginGoogle: %w", err)
	}
	return a, nil
}

// dummy keeps unknown-user logins as slow as real ones.
func (s *Service) dummy() string {
	s.dummyOnce.Do(func() { s.dummyHash = s.Hasher.Hash("allmend-dummy-password") })
	return s.dummyHash
}

// LoginLocal checks e-mail and password. The break-glass hash from the
// environment works for the configured first admin only.
func (s *Service) LoginLocal(ctx context.Context, email, pw string) (Account, error) {
	email, err := NormalizeEmail(email)
	if err != nil || pw == "" {
		_ = s.Hasher.Verify(pw, s.dummy())
		return Account{}, ErrLoginFailed
	}
	a, err := s.Repo.ByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) {
		_ = s.Hasher.Verify(pw, s.dummy())
		return Account{}, ErrLoginFailed
	}
	if err != nil {
		return Account{}, fmt.Errorf("account.LoginLocal: %w", err)
	}
	ok := a.PasswordHash != "" && s.Hasher.Verify(pw, a.PasswordHash) == nil
	if !ok && email == s.AdminEmail && s.BreakGlassHash != "" {
		ok = s.Hasher.Verify(pw, s.BreakGlassHash) == nil
	}
	if !ok || a.Status == Disabled {
		return Account{}, ErrLoginFailed
	}
	if a.Status == Invited {
		a.Status, a.UpdatedAt = Active, s.now()
		if err := s.Repo.Update(ctx, a); err != nil {
			return Account{}, fmt.Errorf("account.LoginLocal: %w", err)
		}
	}
	return a, nil
}

// Changes an admin makes to an account.
type Changes struct {
	Email   string
	Name    string
	IsAdmin bool
	Status  Status
	Modules []Module
}

func (s *Service) requireAdmin(actor Account) error {
	if !actor.IsAdmin || actor.Status != Active {
		return ErrForbidden
	}
	return nil
}

// Invite creates an invited account.
func (s *Service) Invite(ctx context.Context, actor Account, c Changes) (Account, error) {
	if err := s.requireAdmin(actor); err != nil {
		return Account{}, err
	}
	email, err := NormalizeEmail(c.Email)
	if err != nil {
		return Account{}, err
	}
	now := s.now()
	a := Account{ID: id.New(), Email: email, Name: c.Name, IsAdmin: c.IsAdmin, Status: Invited, Modules: c.Modules, CreatedAt: now, UpdatedAt: now}
	return a, s.Repo.Insert(ctx, a)
}

// Update applies admin changes to an account.
func (s *Service) Update(ctx context.Context, actor Account, accountID string, c Changes) (Account, error) {
	if err := s.requireAdmin(actor); err != nil {
		return Account{}, err
	}
	a, err := s.Repo.Get(ctx, accountID)
	if err != nil {
		return a, err
	}
	email, err := NormalizeEmail(c.Email)
	if err != nil {
		return a, err
	}
	switch c.Status {
	case Invited, Active, Disabled:
	default:
		return a, fmt.Errorf("account: unknown status %q", c.Status)
	}
	if a.ID == actor.ID && c.Status == Disabled {
		return a, ErrSelf
	}
	if email != a.Email {
		a.GoogleSub = "" // a new address must be confirmed by a new Google login
	}
	a.Email, a.Name, a.IsAdmin, a.Status, a.Modules, a.UpdatedAt = email, c.Name, c.IsAdmin, c.Status, c.Modules, s.now()
	return a, s.Repo.Update(ctx, a)
}

// ResetPassword enables local login with a generated one-time password,
// which the admin passes on. It must be changed at the next login.
func (s *Service) ResetPassword(ctx context.Context, actor Account, accountID string) (string, error) {
	if err := s.requireAdmin(actor); err != nil {
		return "", err
	}
	a, err := s.Repo.Get(ctx, accountID)
	if err != nil {
		return "", err
	}
	pw := oneTimePassword()
	a.PasswordHash, a.PasswordMustChange, a.UpdatedAt = s.Hasher.Hash(pw), true, s.now()
	return pw, s.Repo.Update(ctx, a)
}

// RemovePassword disables local login for an account.
func (s *Service) RemovePassword(ctx context.Context, actor Account, accountID string) error {
	if err := s.requireAdmin(actor); err != nil {
		return err
	}
	a, err := s.Repo.Get(ctx, accountID)
	if err != nil {
		return err
	}
	a.PasswordHash, a.PasswordMustChange, a.UpdatedAt = "", false, s.now()
	return s.Repo.Update(ctx, a)
}

// ChangePassword sets the account's own password. The current password is
// needed unless a one-time password must be replaced.
func (s *Service) ChangePassword(ctx context.Context, a Account, current, next string) (Account, error) {
	if len([]rune(next)) < MinPasswordLength {
		return a, ErrWeakPassword
	}
	if !a.PasswordMustChange && (a.PasswordHash == "" || s.Hasher.Verify(current, a.PasswordHash) != nil) {
		return a, ErrLoginFailed
	}
	a.PasswordHash, a.PasswordMustChange, a.UpdatedAt = s.Hasher.Hash(next), false, s.now()
	return a, s.Repo.Update(ctx, a)
}

const otpAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

func oneTimePassword() string {
	b := make([]byte, 16)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(otpAlphabet))))
		b[i] = otpAlphabet[n.Int64()]
	}
	return string(b[:4]) + "-" + string(b[4:8]) + "-" + string(b[8:12]) + "-" + string(b[12:])
}
