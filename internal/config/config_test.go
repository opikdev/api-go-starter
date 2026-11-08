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
	if cfg.Port != 3000 || cfg.TrustProxy || len(cfg.CORSOrigins) != 0 || cfg.AuthRateLimitPerMinute != 10 || cfg.SessionTTLHours != 168 {
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
		"a bad flag":             {"DATABASE_URL": dbURL, "TRUST_PROXY": "yes"},
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
