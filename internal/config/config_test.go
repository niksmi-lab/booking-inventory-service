package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("API_KEY", "service-key-0123456789abcdef012345")
	t.Setenv("ADMIN_API_KEY", "admin-key-0123456789abcdef01234567")
	t.Setenv("RESERVATION_TTL", "20m")
	t.Setenv("DB_MAX_CONNECTIONS", "12")
	t.Setenv("TRUSTED_PROXIES", "10.0.0.0/8, 192.168.0.0/16")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ReservationTTL != 20*time.Minute || cfg.DBMaxConnections != 12 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if len(cfg.TrustedProxies) != 2 {
		t.Fatalf("TrustedProxies = %v", cfg.TrustedProxies)
	}
}

func TestLoadRejectsUnsafeOrMissingConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("API_KEY", "same")
	t.Setenv("ADMIN_API_KEY", "same")
	t.Setenv("PORT", "invalid")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil")
	}
	for _, want := range []string{"DATABASE_URL is required", "must be different", "PORT must be"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Load() error %q does not contain %q", err, want)
		}
	}
}
