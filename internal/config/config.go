// Package config loads and validates runtime settings from the environment.
package config

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const (
	EnvironmentDevelopment = "development"
	EnvironmentProduction  = "production"
)

// Config is the validated application configuration.
type Config struct {
	Environment string
	LogLevel    slog.Level

	HTTP     HTTP
	Postgres Postgres
	Redis    Redis
	Admin    Admin
	Vote     Vote
	Cache    Cache
	Sync     Sync
	Polls    Polls
	Cookie   Cookie

	ProbeTimeout  time.Duration
	ProbeCacheFor time.Duration

	TrustedProxies []netip.Prefix
}

// HTTP configures the HTTP server.
type HTTP struct {
	Address           string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
	MaxRequestBytes   int64
	RequestTimeout    time.Duration
	ShutdownTimeout   time.Duration
}

// Postgres configures the PostgreSQL pool.
type Postgres struct {
	DSN             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
	ConnectTimeout  time.Duration
}

// Redis configures the Redis client (standalone, Sentinel, or cluster).
type Redis struct {
	Addresses    []string
	Username     string
	Password     string
	DB           int
	MasterName   string
	PoolSize     int
	MinIdleConns int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	MaxRetries   int
}

// Admin holds admin API credentials.
type Admin struct {
	Token string
}

// Vote configures vote deduplication and abuse limits.
type Vote struct {
	Shards            int
	DedupTTL          time.Duration
	CounterRetention  time.Duration
	RateLimit         int
	RateWindow        time.Duration
	PollIPQuota       int
	PollIPQuotaWindow time.Duration
}

// Cache configures the poll configuration cache.
type Cache struct {
	LocalTTL    time.Duration
	SharedTTL   time.Duration
	NegativeTTL time.Duration
	Capacity    int
}

// Sync configures the Redis→PostgreSQL sync job.
type Sync struct {
	Timeout     time.Duration
	SettleAfter time.Duration
}

// Polls bounds poll content and pagination.
type Polls struct {
	MaxQuestionLength int
	MaxOptionLength   int
	MaxOptions        int
	DefaultPageSize   int
	MaxPageSize       int
}

// Cookie configures the anonymous voter cookie.
type Cookie struct {
	Name     string
	TTL      time.Duration
	Secure   bool
	SameSite http.SameSite
	Domain   string
}

// Load reads and validates configuration from the environment.
func Load() (*Config, error) {
	_ = godotenv.Load()

	l := &loader{}
	cfg := &Config{
		Environment: l.oneOf("APP_ENV", EnvironmentDevelopment, EnvironmentDevelopment, EnvironmentProduction),
		LogLevel:    parseLogLevel(l, "LOG_LEVEL", slog.LevelInfo),

		HTTP: HTTP{
			Address:           l.optionalString("HTTP_ADDRESS", ":8080"),
			ReadHeaderTimeout: l.duration("HTTP_READ_HEADER_TIMEOUT", 5*time.Second, time.Second, time.Minute),
			ReadTimeout:       l.duration("HTTP_READ_TIMEOUT", 15*time.Second, time.Second, 5*time.Minute),
			WriteTimeout:      l.duration("HTTP_WRITE_TIMEOUT", 15*time.Second, time.Second, 5*time.Minute),
			IdleTimeout:       l.duration("HTTP_IDLE_TIMEOUT", 90*time.Second, time.Second, 10*time.Minute),
			MaxHeaderBytes:    l.integer("HTTP_MAX_HEADER_BYTES", 16<<10, 1<<10, 1<<20),
			MaxRequestBytes:   int64(l.integer("HTTP_MAX_REQUEST_BYTES", 64<<10, 1<<10, 8<<20)),
			RequestTimeout:    l.duration("HTTP_REQUEST_TIMEOUT", 5*time.Second, 100*time.Millisecond, time.Minute),
			ShutdownTimeout:   l.duration("HTTP_SHUTDOWN_TIMEOUT", 20*time.Second, time.Second, 5*time.Minute),
		},

		Postgres: Postgres{
			DSN:             l.requiredString("DATABASE_URL"),
			MaxConns:        int32(l.integer("DATABASE_MAX_CONNS", 20, 1, 500)),
			MinConns:        int32(l.integer("DATABASE_MIN_CONNS", 2, 0, 500)),
			MaxConnLifetime: l.duration("DATABASE_MAX_CONN_LIFETIME", 30*time.Minute, time.Minute, 24*time.Hour),
			MaxConnIdleTime: l.duration("DATABASE_MAX_CONN_IDLE_TIME", 5*time.Minute, time.Second, time.Hour),
			ConnectTimeout:  l.duration("DATABASE_CONNECT_TIMEOUT", 10*time.Second, time.Second, time.Minute),
		},

		Redis: Redis{
			Addresses:    l.stringList("REDIS_ADDRESSES"),
			Username:     l.optionalString("REDIS_USERNAME", ""),
			Password:     l.optionalString("REDIS_PASSWORD", ""),
			DB:           l.integer("REDIS_DB", 0, 0, 15),
			MasterName:   l.optionalString("REDIS_MASTER_NAME", ""),
			PoolSize:     l.integer("REDIS_POOL_SIZE", 128, 1, 10000),
			MinIdleConns: l.integer("REDIS_MIN_IDLE_CONNS", 16, 0, 10000),
			DialTimeout:  l.duration("REDIS_DIAL_TIMEOUT", 2*time.Second, 100*time.Millisecond, time.Minute),
			ReadTimeout:  l.duration("REDIS_READ_TIMEOUT", 500*time.Millisecond, 50*time.Millisecond, 30*time.Second),
			WriteTimeout: l.duration("REDIS_WRITE_TIMEOUT", 500*time.Millisecond, 50*time.Millisecond, 30*time.Second),
			MaxRetries:   l.integer("REDIS_MAX_RETRIES", 2, 0, 10),
		},

		Admin: Admin{Token: l.secret("ADMIN_TOKEN", 32)},

		Vote: Vote{
			Shards:            l.integer("VOTE_SHARDS", 16, 1, 1024),
			DedupTTL:          l.duration("VOTE_DEDUP_TTL", time.Hour, time.Minute, 30*24*time.Hour),
			CounterRetention:  l.duration("VOTE_COUNTER_RETENTION", 7*24*time.Hour, time.Hour, 90*24*time.Hour),
			RateLimit:         l.integer("VOTE_RATE_LIMIT", 100, 0, 1000000),
			RateWindow:        l.duration("VOTE_RATE_WINDOW", 10*time.Second, time.Second, time.Hour),
			PollIPQuota:       l.integer("VOTE_POLL_IP_QUOTA", 0, 0, 1000000),
			PollIPQuotaWindow: l.duration("VOTE_POLL_IP_QUOTA_WINDOW", time.Hour, time.Minute, 30*24*time.Hour),
		},

		Cache: Cache{
			LocalTTL:    l.duration("POLL_CACHE_LOCAL_TTL", 2*time.Second, 100*time.Millisecond, time.Minute),
			SharedTTL:   l.duration("POLL_CACHE_SHARED_TTL", time.Minute, time.Second, time.Hour),
			NegativeTTL: l.duration("POLL_CACHE_NEGATIVE_TTL", 10*time.Second, time.Second, 10*time.Minute),
			Capacity:    l.integer("POLL_CACHE_CAPACITY", 4096, 16, 1<<20),
		},

		Sync: Sync{
			Timeout:     l.duration("SYNC_TIMEOUT", 30*time.Second, time.Second, 10*time.Minute),
			SettleAfter: l.duration("SYNC_SETTLE_AFTER", 10*time.Minute, time.Minute, 24*time.Hour),
		},

		Polls: Polls{
			MaxQuestionLength: l.integer("POLL_MAX_QUESTION_LENGTH", 500, 10, 10000),
			MaxOptionLength:   l.integer("POLL_MAX_OPTION_LENGTH", 200, 1, 10000),
			MaxOptions:        l.integer("POLL_MAX_OPTIONS", 20, 2, 1000),
			DefaultPageSize:   l.integer("POLL_PAGE_SIZE_DEFAULT", 20, 1, 1000),
			MaxPageSize:       l.integer("POLL_PAGE_SIZE_MAX", 100, 1, 1000),
		},

		Cookie: Cookie{
			Name:     l.optionalString("VOTER_COOKIE_NAME", "voter_id"),
			TTL:      l.duration("VOTER_COOKIE_TTL", 30*24*time.Hour, time.Hour, 365*24*time.Hour),
			Secure:   l.boolean("VOTER_COOKIE_SECURE", true),
			SameSite: parseSameSite(l, "VOTER_COOKIE_SAMESITE", http.SameSiteLaxMode),
			Domain:   l.optionalString("VOTER_COOKIE_DOMAIN", ""),
		},

		ProbeTimeout:   l.duration("PROBE_TIMEOUT", 2*time.Second, 100*time.Millisecond, 30*time.Second),
		ProbeCacheFor:  l.duration("PROBE_CACHE_FOR", time.Second, 0, time.Minute),
		TrustedProxies: l.prefixList("TRUSTED_PROXY_CIDRS"),
	}

	if len(cfg.Redis.Addresses) == 0 {
		l.fail("REDIS_ADDRESSES is required (comma-separated host:port list)")
	}
	if cfg.Postgres.MinConns > cfg.Postgres.MaxConns {
		l.fail("DATABASE_MIN_CONNS must not exceed DATABASE_MAX_CONNS")
	}
	if cfg.Polls.DefaultPageSize > cfg.Polls.MaxPageSize {
		l.fail("POLL_PAGE_SIZE_DEFAULT must not exceed POLL_PAGE_SIZE_MAX")
	}
	if cfg.Cache.LocalTTL > cfg.Cache.SharedTTL {
		l.fail("POLL_CACHE_LOCAL_TTL must not exceed POLL_CACHE_SHARED_TTL")
	}
	if cfg.Environment == EnvironmentProduction {
		validateProduction(l, cfg)
	}

	if err := l.err(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func validateProduction(l *loader, cfg *Config) {
	if !cfg.Cookie.Secure {
		l.fail("VOTER_COOKIE_SECURE must be true in production")
	}
	if cfg.Cookie.SameSite == http.SameSiteNoneMode && !cfg.Cookie.Secure {
		l.fail("VOTER_COOKIE_SAMESITE=none requires VOTER_COOKIE_SECURE=true")
	}
}

func parseLogLevel(l *loader, key string, fallback slog.Level) slog.Level {
	switch l.oneOf(key, fallback.String(), "debug", "info", "warn", "error") {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func parseSameSite(l *loader, key string, fallback http.SameSite) http.SameSite {
	switch l.oneOf(key, sameSiteName(fallback), "lax", "strict", "none") {
	case "strict":
		return http.SameSiteStrictMode
	case "none":
		return http.SameSiteNoneMode
	default:
		return http.SameSiteLaxMode
	}
}

func sameSiteName(mode http.SameSite) string {
	switch mode {
	case http.SameSiteStrictMode:
		return "strict"
	case http.SameSiteNoneMode:
		return "none"
	default:
		return "lax"
	}
}

type loader struct {
	problems []string
}

func (l *loader) fail(format string, args ...any) {
	l.problems = append(l.problems, fmt.Sprintf(format, args...))
}

func (l *loader) err() error {
	if len(l.problems) == 0 {
		return nil
	}
	return fmt.Errorf("invalid configuration:\n  - %s", strings.Join(l.problems, "\n  - "))
}

func (l *loader) requiredString(key string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		l.fail("%s is required", key)
	}
	return value
}

func (l *loader) optionalString(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func (l *loader) secret(key string, minLength int) string {
	value := os.Getenv(key)
	if value == "" {
		l.fail("%s is required; generate one with `openssl rand -hex 32`", key)
		return ""
	}
	if len(value) < minLength {
		l.fail("%s must be at least %d characters", key, minLength)
	}
	return value
}

func (l *loader) integer(key string, fallback, minimum, maximum int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		l.fail("%s must be an integer, got %q", key, raw)
		return fallback
	}
	if value < minimum || value > maximum {
		l.fail("%s must be between %d and %d, got %d", key, minimum, maximum, value)
		return fallback
	}
	return value
}

func (l *loader) duration(key string, fallback, minimum, maximum time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		l.fail("%s must be a duration such as 250ms or 30s, got %q", key, raw)
		return fallback
	}
	if value < minimum || value > maximum {
		l.fail("%s must be between %s and %s, got %s", key, minimum, maximum, value)
		return fallback
	}
	return value
}

func (l *loader) boolean(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		l.fail("%s must be a boolean, got %q", key, raw)
		return fallback
	}
	return value
}

func (l *loader) stringList(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}

func (l *loader) prefixList(key string) []netip.Prefix {
	raw := l.stringList(key)
	prefixes := make([]netip.Prefix, 0, len(raw))
	for _, entry := range raw {
		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			l.fail("%s contains an invalid CIDR %q", key, entry)
			continue
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes
}

func (l *loader) oneOf(key, fallback string, allowed ...string) string {
	value := l.optionalString(key, fallback)
	for _, candidate := range allowed {
		if strings.EqualFold(value, candidate) {
			return strings.ToLower(value)
		}
	}
	l.fail("%s must be one of %s, got %q", key, strings.Join(allowed, ", "), value)
	return fallback
}
