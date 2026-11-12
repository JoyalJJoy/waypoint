package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Env                string
	HTTPAddr           string
	DatabaseURL        string
	MigrateDatabaseURL string
	RedisURL           string
	NATSURL            string
	SMTPAddr           string
	JWTSecret          string
	AccessTokenTTL     time.Duration
	RefreshTokenTTL    time.Duration
	CookieSecure       bool
	AllowedOrigins     []string
	LogLevel           string
}

func Load() (Config, error) {
	// Local development convenience. Environment variables always win.
	loadDotEnv()

	cfg := Config{
		Env:                env("APP_ENV", "development"),
		HTTPAddr:           env("HTTP_ADDR", ":8080"),
		DatabaseURL:        env("DATABASE_URL", ""),
		MigrateDatabaseURL: env("MIGRATE_DATABASE_URL", env("DATABASE_URL", "")),
		RedisURL:           env("REDIS_URL", "redis://localhost:6380/0"),
		NATSURL:            env("NATS_URL", "nats://localhost:4222"),
		SMTPAddr:           env("SMTP_ADDR", ""),
		JWTSecret:          env("JWT_SECRET", ""),
		AccessTokenTTL:     envDuration("ACCESS_TOKEN_TTL", 15*time.Minute),
		RefreshTokenTTL:    envDuration("REFRESH_TOKEN_TTL", 30*24*time.Hour),
		CookieSecure:       envBool("COOKIE_SECURE", false),
		AllowedOrigins:     splitCSV(env("ALLOWED_ORIGINS", "http://localhost:3000")),
		LogLevel:           env("LOG_LEVEL", "info"),
	}

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if len(cfg.JWTSecret) < 32 {
		return Config{}, fmt.Errorf("JWT_SECRET must be at least 32 characters")
	}
	if cfg.Env == "production" && !cfg.CookieSecure {
		return Config{}, fmt.Errorf("COOKIE_SECURE must be true in production")
	}

	return cfg, nil
}

// LoadMigrate returns the owner connection string for schema migrations.
// Migrations only need a database, so they must not be blocked by unrelated
// configuration such as the JWT secret.
func LoadMigrate() (string, error) {
	loadDotEnv()

	url := env("MIGRATE_DATABASE_URL", env("DATABASE_URL", ""))
	if url == "" {
		return "", fmt.Errorf("MIGRATE_DATABASE_URL is required")
	}
	return url, nil
}

func loadDotEnv() {
	for _, path := range []string{".env", "../.env"} {
		if _, err := os.Stat(path); err == nil {
			_ = godotenv.Load(path)
		}
	}
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
