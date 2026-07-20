package config

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Port        string
	DatabaseURL string
	Environment string
	BunDebug    bool

	// JWT / keys
	JWTPrivateKeyPath string // path to PEM private key
	JWTPublicKeyPath  string // path to PEM public key
	AccessTokenTTL    time.Duration
	RefreshTokenTTL   time.Duration

	// LDAP
	LDAPServer   string
	LDAPBindDN   string
	LDAPBindPass string
	LDAPBaseDN   string

	// CORS - Add this
	AllowedOrigins []string

	// Emails allowed to post dashboard marquee announcements (and related notify features).
	// Comma-separated via NOTIFY_EMAILS; defaults match the frontend allowlist.
	NotifyEmails []string

	// Redis cache
	RedisURL      string        // empty disables caching
	CacheTTLShort time.Duration // volatile ranges (include today)
	CacheTTLLong  time.Duration // immutable historical ranges
}

// Load loads environment variables and returns a Config struct
func Load() *Config {
	_ = godotenv.Load()

	accessTTLMin, _ := strconv.Atoi(getEnv("ACCESS_TOKEN_MINUTES", "15"))
	refreshTTLDays, _ := strconv.Atoi(getEnv("REFRESH_TOKEN_DAYS", "10"))

	cacheTTLShortSec, _ := strconv.Atoi(getEnv("CACHE_TTL_SHORT_SECONDS", "120"))   // 2m
	cacheTTLLongSec, _ := strconv.Atoi(getEnv("CACHE_TTL_LONG_SECONDS", "21600")) // 6h

	// Parse allowed origins from env (comma-separated)
	allowedOrigins := strings.Split(
		getEnv("ALLOWED_ORIGINS", "http://localhost:3000,http://localhost:5173"),
		",",
	)

	notifyEmails := parseEmailList(getEnv(
		"NOTIFY_EMAILS",
		"jdanso@ecggh.com,yadofo@ecggh.com",
	))

	return &Config{
		Port:              getEnv("APP_PORT", "8780"),
		DatabaseURL:       getEnv("DATABASE_URL", "postgres://postgres:password@localhost:5432/ea-5?sslmode=disable"),
		Environment:       getEnv("ENVIRONMENT", "development"),
		BunDebug:          getEnvAsBool("BUNDEBUG", false),
		JWTPrivateKeyPath: getEnv("JWT_PRIVATE_KEY_PATH", "keys/jwt_private.pem"),
		JWTPublicKeyPath:  getEnv("JWT_PUBLIC_KEY_PATH", "keys/jwt_public.pem"),
		AccessTokenTTL:    time.Duration(accessTTLMin) * time.Minute,      // default 15m
		RefreshTokenTTL:   time.Duration(refreshTTLDays) * 24 * time.Hour, // default 10d
		//LDAPServer:        getEnv("LDAP_SERVER", "ldap://ldap.example.com:389"),
		LDAPServer:     getEnv("LDAP_SERVER", "ldap://localhost:10389"),
		LDAPBindDN:     getEnv("LDAP_BIND_DN", ""),
		LDAPBindPass:   getEnv("LDAP_BIND_PASS", ""),
		LDAPBaseDN:     getEnv("LDAP_BASE_DN", ""),
		AllowedOrigins: allowedOrigins, // Add this
		NotifyEmails:   notifyEmails,

		RedisURL:      getEnv("REDIS_URL", ""),
		CacheTTLShort: time.Duration(cacheTTLShortSec) * time.Second,
		CacheTTLLong:  time.Duration(cacheTTLLongSec) * time.Second,
	}
}

func parseEmailList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		email := strings.TrimSpace(strings.ToLower(p))
		if email != "" {
			out = append(out, email)
		}
	}
	return out
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func getEnvAsBool(key string, fallback bool) bool {
	valStr := os.Getenv(key)
	if valStr == "" {
		return fallback
	}
	val, err := strconv.ParseBool(valStr)
	if err != nil {
		log.Printf("invalid bool for %s, defaulting to %v\n", key, fallback)
		return fallback
	}
	return val
}
