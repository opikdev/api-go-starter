package config

import (
	"strings"
	"testing"
)

func env(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

const dbURL = "postgres://user:secret-value@localhost/db"

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{"DATABASE_URL": dbURL}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 3000 || cfg.TrustProxyHops != 0 || len(cfg.CORSOrigins) != 0 || cfg.AuthRateLimitPerMinute != 10 || cfg.SessionTTLHours != 168 {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadParsesOrigins(t *testing.T) {
	cfg, err := Load(env(map[string]string{"DATABASE_URL": dbURL, "CORS_ORIGINS": "https://a.example, https://b.example,"}))
	if err != nil || strings.Join(cfg.CORSOrigins, "|") != "https://a.example|https://b.example" {
		t.Fatalf("origins = %v, %v", cfg.CORSOrigins, err)
	}
}

func TestLoadRejects(t *testing.T) {
	cases := map[string]map[string]string{
		"a missing DATABASE_URL": {},
		"a bad port":             {"DATABASE_URL": dbURL, "PORT": "70000"},
		"a non-numeric limit":    {"DATABASE_URL": dbURL, "RATE_LIMIT_PER_MINUTE": "many"},
		"negative proxy hops":    {"DATABASE_URL": dbURL, "TRUST_PROXY_HOPS": "-1"},
		"a bad log level":        {"DATABASE_URL": dbURL, "LOG_LEVEL": "loud"},
	}
	for label, values := range cases {
		if _, err := Load(env(values)); err == nil {
			t.Errorf("%s: accepted", label)
		}
	}
}

func TestLoadNamesEveryProblemWithoutSecrets(t *testing.T) {
	_, err := Load(env(map[string]string{"DATABASE_URL": "not a url secret-value", "PORT": "x"}))
	if err == nil || !strings.Contains(err.Error(), "PORT") || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatal("error contains a secret")
	}
}

func TestLoadReadsTracingSettings(t *testing.T) {
	cfg, _ := Load(env(map[string]string{"DATABASE_URL": dbURL}))
	if cfg.TracingEnabled || cfg.ServiceName != "api-go-starter" {
		t.Fatalf("defaults = %+v", cfg)
	}
	cfg, _ = Load(env(map[string]string{"DATABASE_URL": dbURL, "OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4318", "OTEL_SERVICE_NAME": "billing"}))
	if !cfg.TracingEnabled || cfg.ServiceName != "billing" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadMigrate(t *testing.T) {
	cfg, err := LoadMigrate(env(map[string]string{"MIGRATION_DATABASE_URL": dbURL, "DATABASE_URL": "postgres://app@localhost/db"}))
	if err != nil || cfg.DatabaseURL != dbURL {
		t.Fatalf("cfg = %+v, %v", cfg, err)
	}
	if _, err := LoadMigrate(env(map[string]string{"DATABASE_URL": dbURL})); err == nil || !strings.Contains(err.Error(), "MIGRATION_DATABASE_URL") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadSeed(t *testing.T) {
	cfg, err := LoadSeed(env(map[string]string{"DATABASE_URL": dbURL}))
	if err != nil || cfg.Enabled || cfg.DatabaseURL != dbURL {
		t.Fatalf("seeding must be off by default: %+v, %v", cfg, err)
	}
	owner := "postgres://owner@localhost/db"
	cfg, err = LoadSeed(env(map[string]string{"DATABASE_URL": dbURL, "MIGRATION_DATABASE_URL": owner, "SEED_DEMO_DATA": "true"}))
	if err != nil || !cfg.Enabled || cfg.DatabaseURL != owner {
		t.Fatalf("cfg = %+v, %v", cfg, err)
	}
	for label, values := range map[string]map[string]string{
		"no database URL": {"SEED_DEMO_DATA": "true"},
		"a bad flag":      {"DATABASE_URL": dbURL, "SEED_DEMO_DATA": "yes"},
	} {
		if _, err := LoadSeed(env(values)); err == nil {
			t.Errorf("%s: accepted", label)
		}
	}
}

func TestLoadHealthcheck(t *testing.T) {
	if cfg, err := LoadHealthcheck(env(nil)); err != nil || cfg.Port != 3000 {
		t.Fatalf("default = %+v, %v", cfg, err)
	}
	if cfg, err := LoadHealthcheck(env(map[string]string{"PORT": "8080"})); err != nil || cfg.Port != 8080 {
		t.Fatalf("cfg = %+v, %v", cfg, err)
	}
	if _, err := LoadHealthcheck(env(map[string]string{"PORT": "0"})); err == nil {
		t.Fatal("accepted port 0")
	}
}

func TestLoadRejectsTheRemovedTrustProxy(t *testing.T) {
	_, err := Load(env(map[string]string{"DATABASE_URL": dbURL, "TRUST_PROXY": "true"}))
	want := "TRUST_PROXY was replaced by TRUST_PROXY_HOPS. Set TRUST_PROXY_HOPS=1 for the old behavior."
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v", err)
	}
	cfg, err := Load(env(map[string]string{"DATABASE_URL": dbURL, "TRUST_PROXY_HOPS": "2"}))
	if err != nil || cfg.TrustProxyHops != 2 {
		t.Fatalf("cfg = %+v, %v", cfg, err)
	}
}
