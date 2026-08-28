// Package config loads process configuration from APP_-prefixed environment
// variables. The committed defaults live in .env, not in struct tags.
package config

import (
	"fmt"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// Config holds all runtime configuration for the currency-migration tool.
type Config struct {
	HTTPTimeout    time.Duration `split_words:"true"`
	WalletBaseURL  string        `split_words:"true"`
	WalletAPIToken string        `split_words:"true"`
	OutputDir      string        `split_words:"true"`
}

// Settings is the process-wide configuration singleton. Call Load once during
// startup, then read fields directly as config.Settings.Field.
//
//nolint:gochecknoglobals // .agents/architecture-go.md: config.Settings is the intended singleton.
var Settings Config

// Load populates Settings from the environment and validates it, returning a
// wrapped error when a required value is missing or malformed.
func Load() error {
	if err := envconfig.Process("app", &Settings); err != nil {
		return fmt.Errorf("process config: %w", err)
	}

	if err := Settings.validate(); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}

	return nil
}

// validate reports the first configuration value that is unusable.
func (c Config) validate() error {
	// APP_WALLET_API_TOKEN is deliberately absent: it is required only for live
	// API calls, not for --help, --dry-run planning, or the test suite. The
	// command checks it explicitly before a live operation.
	required := []struct {
		name  string
		value string
	}{
		{"APP_WALLET_BASE_URL", c.WalletBaseURL},
		{"APP_OUTPUT_DIR", c.OutputDir},
	}
	for _, f := range required {
		if f.value == "" {
			return fmt.Errorf("missing required env var %s", f.name)
		}
	}

	if c.HTTPTimeout <= 0 {
		return fmt.Errorf("app_http_timeout must be a positive duration, got %s", c.HTTPTimeout)
	}

	return nil
}
