// Package config reads settings from the environment.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const (
	defaultPort        = 3000
	defaultServiceName = "api-go-starter"
)

// Config holds every setting the serve command reads at startup.
type Config struct {
	Port                   int
	LogLevel               string
	DatabaseURL            string
	StatementTimeoutMS     int
	CORSOrigins            []string
	TrustProxy             bool
	SessionTTLHours        int32
	RateLimitPerMinute     int
	AuthRateLimitPerMinute int
	TracingEnabled         bool
	ServiceName            string
}

// Migrate holds the settings for the migrate command.
type Migrate struct {
	DatabaseURL string
}

// Seed holds the settings for the seed command.
type Seed struct {
	DatabaseURL string
	Enabled     bool
}

// Healthcheck holds the settings for the healthcheck command.
type Healthcheck struct {
	Port int
}

type reader struct {
	env    func(string) string
	issues []string
}

func (r *reader) str(name, fallback string) string {
	if value := strings.TrimSpace(r.env(name)); value != "" {
		return value
	}
	return fallback
}

func (r *reader) integer(name string, fallback, low, high int) int {
	raw := r.str(name, "")
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < low || value > high {
		r.issues = append(r.issues, fmt.Sprintf("%s must be a whole number from %d to %d", name, low, high))
		return fallback
	}
	return value
}

func (r *reader) int32(name string, fallback, low, high int32) int32 {
	raw := r.str(name, "")
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || int32(value) < low || int32(value) > high {
		r.issues = append(r.issues, fmt.Sprintf("%s must be a whole number from %d to %d", name, low, high))
		return fallback
	}
	return int32(value)
}

func (r *reader) flag(name string) bool {
	switch raw := r.str(name, "false"); raw {
	case "true":
		return true
	case "false":
		return false
	default:
		r.issues = append(r.issues, name+" must be true or false")
		return false
	}
}

func (r *reader) databaseURL(name, value string) {
	if parsed, err := url.Parse(value); value == "" || err != nil || parsed.Scheme == "" {
		r.issues = append(r.issues, name+" must be a Postgres URL")
	}
}

func (r *reader) err() error {
	if len(r.issues) > 0 {
		return errors.New("invalid environment: " + strings.Join(r.issues, "; "))
	}
	return nil
}

func newReader(env func(string) string) *reader {
	if env == nil {
		env = os.Getenv
	}
	return &reader{env: env}
}

// Load reads the serve configuration, naming every invalid variable without echoing its value.
// A nil env reads the process environment.
func Load(env func(string) string) (Config, error) {
	r := newReader(env)
	cfg := Config{
		Port:                   r.integer("PORT", defaultPort, 1, 65535),
		LogLevel:               r.str("LOG_LEVEL", "info"),
		DatabaseURL:            r.str("DATABASE_URL", ""),
		StatementTimeoutMS:     r.integer("DATABASE_STATEMENT_TIMEOUT_MS", 5000, 1, 600_000),
		TrustProxy:             r.flag("TRUST_PROXY"),
		SessionTTLHours:        r.int32("SESSION_TTL_HOURS", 168, 1, 8760),
		RateLimitPerMinute:     r.integer("RATE_LIMIT_PER_MINUTE", 300, 1, 1_000_000),
		AuthRateLimitPerMinute: r.integer("AUTH_RATE_LIMIT_PER_MINUTE", 10, 1, 1_000_000),
		TracingEnabled:         r.str("OTEL_EXPORTER_OTLP_ENDPOINT", "") != "",
		ServiceName:            r.str("OTEL_SERVICE_NAME", defaultServiceName),
	}
	for _, origin := range strings.Split(r.str("CORS_ORIGINS", ""), ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			cfg.CORSOrigins = append(cfg.CORSOrigins, origin)
		}
	}
	switch cfg.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		r.issues = append(r.issues, "LOG_LEVEL must be debug, info, warn, or error")
	}
	r.databaseURL("DATABASE_URL", cfg.DatabaseURL)
	if err := r.err(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// LoadMigrate reads the migrate configuration. A nil env reads the process environment.
func LoadMigrate(env func(string) string) (Migrate, error) {
	r := newReader(env)
	cfg := Migrate{DatabaseURL: r.str("MIGRATION_DATABASE_URL", "")}
	r.databaseURL("MIGRATION_DATABASE_URL", cfg.DatabaseURL)
	if err := r.err(); err != nil {
		return Migrate{}, err
	}
	return cfg, nil
}

// LoadSeed reads the seed configuration, preferring MIGRATION_DATABASE_URL over DATABASE_URL.
// A nil env reads the process environment.
func LoadSeed(env func(string) string) (Seed, error) {
	r := newReader(env)
	cfg := Seed{
		DatabaseURL: r.str("MIGRATION_DATABASE_URL", r.str("DATABASE_URL", "")),
		Enabled:     r.flag("SEED_DEMO_DATA"),
	}
	r.databaseURL("MIGRATION_DATABASE_URL or DATABASE_URL", cfg.DatabaseURL)
	if err := r.err(); err != nil {
		return Seed{}, err
	}
	return cfg, nil
}

// LoadHealthcheck reads the healthcheck configuration. A nil env reads the process environment.
func LoadHealthcheck(env func(string) string) (Healthcheck, error) {
	r := newReader(env)
	cfg := Healthcheck{Port: r.integer("PORT", defaultPort, 1, 65535)}
	if err := r.err(); err != nil {
		return Healthcheck{}, err
	}
	return cfg, nil
}
