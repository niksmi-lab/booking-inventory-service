package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Address            string
	DatabaseURL        string
	APIKey             string
	AdminAPIKey        string
	ReservationTTL     time.Duration
	CleanupInterval    time.Duration
	DBOperationTimeout time.Duration
	ShutdownTimeout    time.Duration
	DBMaxConnections   int32
	DBMinConnections   int32
	AutoMigrate        bool
	TrustedProxies     []string
}

func Load() (Config, error) {
	port := envOrDefault("PORT", "8080")
	cfg := Config{
		Address:     ":" + port,
		DatabaseURL: strings.TrimSpace(os.Getenv("DATABASE_URL")),
		APIKey:      strings.TrimSpace(os.Getenv("API_KEY")),
		AdminAPIKey: strings.TrimSpace(os.Getenv("ADMIN_API_KEY")),
	}

	var errs []error
	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	if cfg.APIKey == "" {
		errs = append(errs, errors.New("API_KEY is required"))
	}
	if cfg.AdminAPIKey == "" {
		errs = append(errs, errors.New("ADMIN_API_KEY is required"))
	}
	if cfg.APIKey != "" && len(cfg.APIKey) < 32 {
		errs = append(errs, errors.New("API_KEY must contain at least 32 characters"))
	}
	if cfg.AdminAPIKey != "" && len(cfg.AdminAPIKey) < 32 {
		errs = append(errs, errors.New("ADMIN_API_KEY must contain at least 32 characters"))
	}
	if cfg.APIKey != "" && cfg.APIKey == cfg.AdminAPIKey {
		errs = append(errs, errors.New("API_KEY and ADMIN_API_KEY must be different"))
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil || port == "0" {
		errs = append(errs, fmt.Errorf("PORT must be a number between 1 and 65535: %q", port))
	}

	cfg.ReservationTTL = duration("RESERVATION_TTL", 15*time.Minute, &errs)
	cfg.CleanupInterval = duration("CLEANUP_INTERVAL", time.Minute, &errs)
	cfg.DBOperationTimeout = duration("DB_OPERATION_TIMEOUT", 3*time.Second, &errs)
	cfg.ShutdownTimeout = duration("SHUTDOWN_TIMEOUT", 10*time.Second, &errs)
	cfg.DBMaxConnections = positiveInt32("DB_MAX_CONNECTIONS", 20, &errs)
	cfg.DBMinConnections = nonNegativeInt32("DB_MIN_CONNECTIONS", 2, &errs)
	cfg.AutoMigrate = boolean("AUTO_MIGRATE", true, &errs)

	if cfg.DBMinConnections > cfg.DBMaxConnections {
		errs = append(errs, errors.New("DB_MIN_CONNECTIONS cannot exceed DB_MAX_CONNECTIONS"))
	}
	if value := strings.TrimSpace(os.Getenv("TRUSTED_PROXIES")); value != "" {
		for _, proxy := range strings.Split(value, ",") {
			if proxy = strings.TrimSpace(proxy); proxy != "" {
				cfg.TrustedProxies = append(cfg.TrustedProxies, proxy)
			}
		}
	}

	return cfg, errors.Join(errs...)
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func duration(name string, fallback time.Duration, errs *[]error) time.Duration {
	value := envOrDefault(name, fallback.String())
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		*errs = append(*errs, fmt.Errorf("%s must be a positive duration: %q", name, value))
		return fallback
	}
	return parsed
}

func positiveInt32(name string, fallback int32, errs *[]error) int32 {
	value := envOrDefault(name, strconv.FormatInt(int64(fallback), 10))
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil || parsed <= 0 {
		*errs = append(*errs, fmt.Errorf("%s must be a positive integer: %q", name, value))
		return fallback
	}
	return int32(parsed)
}

func nonNegativeInt32(name string, fallback int32, errs *[]error) int32 {
	value := envOrDefault(name, strconv.FormatInt(int64(fallback), 10))
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil || parsed < 0 {
		*errs = append(*errs, fmt.Errorf("%s must be a non-negative integer: %q", name, value))
		return fallback
	}
	return int32(parsed)
}

func boolean(name string, fallback bool, errs *[]error) bool {
	value := envOrDefault(name, strconv.FormatBool(fallback))
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s must be a boolean: %q", name, value))
		return fallback
	}
	return parsed
}
