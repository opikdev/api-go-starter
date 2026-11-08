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

// Config holds every setting the API reads at startup.
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

// Load reads the configuration, naming every invalid variable without echoing its value.
func Load(env func(string) string) (Config, error) {
	if env == nil {
		env = os.Getenv
	}
	r := &reader{env: env}
	cfg := Config{
		Port:                   r.integer("PORT", 3000, 1, 65535),
		LogLevel:               r.str("LOG_LEVEL", "info"),
		DatabaseURL:            r.str("DATABASE_URL", ""),
		StatementTimeoutMS:     r.integer("DATABASE_STATEMENT_TIMEOUT_MS", 5000, 1, 600_000),
		TrustProxy:             r.flag("TRUST_PROXY"),
		SessionTTLHours:        r.int32("SESSION_TTL_HOURS", 168, 1, 8760),
		RateLimitPerMinute:     r.integer("RATE_LIMIT_PER_MINUTE", 300, 1, 1_000_000),
		AuthRateLimitPerMinute: r.integer("AUTH_RATE_LIMIT_PER_MINUTE", 10, 1, 1_000_000),
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
	if parsed, err := url.Parse(cfg.DatabaseURL); cfg.DatabaseURL == "" || err != nil || parsed.Scheme == "" {
		r.issues = append(r.issues, "DATABASE_URL must be a Postgres URL")
	}
	if len(r.issues) > 0 {
		return Config{}, errors.New("invalid environment: " + strings.Join(r.issues, "; "))
	}
	return cfg, nil
}
