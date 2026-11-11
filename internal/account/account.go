// Package account holds Allmend accounts, their module access and the admin
// role.
package account

import (
	"context"
	"errors"
	"net/mail"
	"slices"
	"strings"
	"time"
)

// Module identifies an Allmend module an account may be given access to.
type Module string

// The modules. The start page is not a module: it is always available.
const (
	Velo        Module = "velo"
	Finanzen    Module = "finanzen"
	Arbeitswege Module = "arbeitswege"
	Orte        Module = "orte"
	Seiten      Module = "seiten"
	Schiffe     Module = "schiffe"
	Ausgaben    Module = "ausgaben"
)

// ModuleInfo describes a module for navigation and admin forms.
type ModuleInfo struct {
	ID    Module
	Name  string
	Path  string
	Icon  string
	Ready bool // implemented in this version; unready modules are hidden
}

// Modules lists all modules in navigation order.
var Modules = []ModuleInfo{
	{Velo, "Velo", "/velo", "bike", true},
	{Finanzen, "Finanzen", "/finanzen", "chart", true},
	{Arbeitswege, "Arbeitswege", "/arbeitswege", "route", true},
	{Orte, "Orte", "/orte", "pin", true},
	{Ausgaben, "Ausgaben", "/ausgaben", "coins", true},
	{Seiten, "Seiten", "/seiten", "page", true},
	{Schiffe, "Schiffe versenken", "/schiffe", "ship", true},
}

// KnownModule reports whether m is one of Modules.
func KnownModule(m Module) bool {
	return slices.ContainsFunc(Modules, func(i ModuleInfo) bool { return i.ID == m })
}

// Status of an account.
type Status string

// Account states.
const (
	Invited  Status = "invited"  // created by an admin, not logged in yet
	Active   Status = "active"   // may log in
	Disabled Status = "disabled" // may not log in; sessions are deleted
)

// Theme is the colour scheme an account chose.
type Theme string

// Themes. Auto follows the device setting.
const (
	ThemeAuto  Theme = "auto"
	ThemeLight Theme = "light"
	ThemeDark  Theme = "dark"
)

// Themes lists the choices in display order.
var Themes = []Theme{ThemeAuto, ThemeLight, ThemeDark}

// Account is a person using Allmend.
type Account struct {
	ID                 string
	Email              string
	Name               string
	IsAdmin            bool
	Status             Status
	GoogleSub          string // empty until first Google login
	PasswordHash       string // empty: no local login
	PasswordMustChange bool
	Modules            []Module
	Theme              Theme
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// DisplayName is the name, or the part of the e-mail before "@".
func (a Account) DisplayName() string {
	if a.Name != "" {
		return a.Name
	}
	local, _, _ := strings.Cut(a.Email, "@")
	return local
}

// Can reports whether the account may use module m.
func (a Account) Can(m Module) bool { return slices.Contains(a.Modules, m) }

// HasLocalLogin reports whether a password is set.
func (a Account) HasLocalLogin() bool { return a.PasswordHash != "" }

// Errors.
var (
	ErrNotFound     = errors.New("account not found")
	ErrEmailTaken   = errors.New("e-mail already used by another account")
	ErrInvalidEmail = errors.New("not a valid e-mail address")
	ErrUnknownMod   = errors.New("unknown module")
	ErrLastAdmin    = errors.New("the last active admin cannot lose admin rights or be disabled")
	ErrUnknownTheme = errors.New("unknown theme")
	ErrHasData      = errors.New("account still owns or shares data")
)

// NormalizeEmail validates and lower-cases a plain address ("a@b.c").
func NormalizeEmail(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s {
		return "", ErrInvalidEmail
	}
	return s, nil
}

// Repository stores accounts. Implementations live in internal/store.
type Repository interface {
	Get(ctx context.Context, id string) (Account, error)
	ByEmail(ctx context.Context, email string) (Account, error)
	ByGoogleSub(ctx context.Context, sub string) (Account, error)
	List(ctx context.Context) ([]Account, error)
	Insert(ctx context.Context, a Account) error
	// Update writes all fields including Modules; it refuses to leave no active admin.
	Update(ctx context.Context, a Account) error
	// Delete removes an account with its sessions, module grants and page
	// grants. Any other data that refers to it makes Delete fail with
	// ErrHasData; nothing is deleted then.
	Delete(ctx context.Context, id string) error
}
