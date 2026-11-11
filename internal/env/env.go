// Package env loads the Allmend configuration from ALLMEND_* environment
// variables. Unknown ALLMEND_* variables are an error, so typos fail fast.
package env

import (
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

// Config is the complete runtime configuration.
type Config struct {
	Addr         string // listen address, default ":8080"
	DataDir      string // holds allmend.db and backup/, default "data"
	BaseURL      *url.URL
	AdminEmail   string // first admin; created at startup if missing
	AdminPWHash  string // optional break-glass argon2id hash for the first admin
	SecureCookie bool   // default true; false only for plain-HTTP development

	GoogleClientID     string
	GoogleClientSecret string

	BaseCurrency string // ISO 4217, default EUR

	AlphaVantageKey string
	FrankfurterURL  string
}

// DBPath is the path of the Allmend database file.
func (c Config) DBPath() string { return filepath.Join(c.DataDir, "allmend.db") }

// BackupDir is where pre-migration backups are written.
func (c Config) BackupDir() string { return filepath.Join(c.DataDir, "backup") }

// GoogleEnabled reports whether Google login is configured.
func (c Config) GoogleEnabled() bool { return c.GoogleClientID != "" }

var known = map[string]bool{
	"ALLMEND_ADDR": true, "ALLMEND_DATA_DIR": true, "ALLMEND_BASE_URL": true,
	"ALLMEND_ADMIN_EMAIL": true, "ALLMEND_ADMIN_PASSWORD_HASH": true,
	"ALLMEND_SECURE_COOKIE": true, "ALLMEND_GOOGLE_CLIENT_ID": true,
	"ALLMEND_GOOGLE_CLIENT_SECRET": true, "ALLMEND_ALPHAVANTAGE_KEY": true,
	"ALLMEND_BASE_CURRENCY": true, "ALLMEND_FRANKFURTER_URL": true,
}

// Load reads the configuration from environ (os.Environ() format).
func Load(environ []string) (Config, error) {
	vars := map[string]string{}
	var errs []error
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(k, "ALLMEND_") {
			continue
		}
		if !known[k] {
			errs = append(errs, fmt.Errorf("unknown variable %s", k))
			continue
		}
		vars[k] = v
	}

	c := Config{
		Addr:               or(vars["ALLMEND_ADDR"], ":8080"),
		DataDir:            or(vars["ALLMEND_DATA_DIR"], "data"),
		AdminEmail:         strings.ToLower(strings.TrimSpace(vars["ALLMEND_ADMIN_EMAIL"])),
		AdminPWHash:        vars["ALLMEND_ADMIN_PASSWORD_HASH"],
		GoogleClientID:     vars["ALLMEND_GOOGLE_CLIENT_ID"],
		GoogleClientSecret: vars["ALLMEND_GOOGLE_CLIENT_SECRET"],
		AlphaVantageKey:    vars["ALLMEND_ALPHAVANTAGE_KEY"],
		FrankfurterURL:     vars["ALLMEND_FRANKFURTER_URL"],
		SecureCookie:       true,
		BaseCurrency:       or(vars["ALLMEND_BASE_CURRENCY"], "EUR"),
	}
	if len(c.BaseCurrency) != 3 || strings.ToUpper(c.BaseCurrency) != c.BaseCurrency {
		errs = append(errs, errors.New("ALLMEND_BASE_CURRENCY must be an upper-case ISO 4217 code"))
	}

	if v, ok := vars["ALLMEND_SECURE_COOKIE"]; ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("ALLMEND_SECURE_COOKIE: %w", err))
		}
		c.SecureCookie = b
	}

	if c.AdminEmail == "" {
		errs = append(errs, errors.New("ALLMEND_ADMIN_EMAIL is required"))
	} else if a, err := mail.ParseAddress(c.AdminEmail); err != nil || a.Address != c.AdminEmail {
		errs = append(errs, errors.New("ALLMEND_ADMIN_EMAIL is not a plain e-mail address"))
	}

	raw := or(vars["ALLMEND_BASE_URL"], "http://localhost:8080")
	u, err := url.Parse(raw)
	switch {
	case err != nil:
		errs = append(errs, fmt.Errorf("ALLMEND_BASE_URL: %w", err))
	case u.Scheme != "https" && u.Scheme != "http", u.Host == "", u.Path != "" && u.Path != "/", u.RawQuery != "":
		errs = append(errs, errors.New("ALLMEND_BASE_URL must be scheme://host[:port] without path"))
	default:
		u.Path = ""
		c.BaseURL = u
	}

	if (c.GoogleClientID == "") != (c.GoogleClientSecret == "") {
		errs = append(errs, errors.New("set both ALLMEND_GOOGLE_CLIENT_ID and ALLMEND_GOOGLE_CLIENT_SECRET, or neither"))
	}
	if c.GoogleEnabled() && c.BaseURL != nil && c.BaseURL.Scheme != "https" && c.BaseURL.Hostname() != "localhost" {
		errs = append(errs, errors.New("login with Google needs an https ALLMEND_BASE_URL"))
	}
	if !c.GoogleEnabled() && c.AdminPWHash == "" {
		errs = append(errs, errors.New("without Google login ALLMEND_ADMIN_PASSWORD_HASH is required"))
	}

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("env.Load: %w", errors.Join(errs...))
	}
	return c, nil
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
