package bootstrap

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"jungle-gaming-challeng/internal/infra/observability"
)

const (
	EnvDatabaseURL         = "DATABASE_URL"
	EnvDBMaxConns          = "DB_MAX_CONNS"
	EnvDBLockTimeout       = "DB_LOCK_TIMEOUT"
	EnvHTTPAddr            = "HTTP_ADDR"
	EnvHTTPRequestTimeout  = "HTTP_REQUEST_TIMEOUT"
	EnvOIDCIssuerURL       = "OIDC_ISSUER_URL"
	EnvOIDCKeysURL         = "OIDC_KEYS_URL"
	EnvOIDCAudience        = "OIDC_AUDIENCE"
	EnvSQSEndpoint         = "SQS_ENDPOINT"
	EnvAWSRegion           = "AWS_REGION"
	EnvAWSAccessKeyID      = "AWS_ACCESS_KEY_ID"
	EnvAWSSecretAccessKey  = "AWS_SECRET_ACCESS_KEY"
	EnvPendingReferenceTTL = "PENDING_REFERENCE_TTL"
	EnvShutdownTimeout     = "SHUTDOWN_TIMEOUT"
	EnvLogLevel            = "LOG_LEVEL"
)

var ErrInvalidConfig = errors.New("bootstrap: invalid configuration")

type Config struct {
	DatabaseURL         observability.Secret
	DBMaxConns          int
	DBLockTimeout       time.Duration
	HTTPAddr            string
	HTTPRequestTimeout  time.Duration
	OIDCIssuerURL       string
	OIDCKeysURL         string
	OIDCAudience        string
	SQSEndpoint         string
	AWSRegion           string
	AWSAccessKeyID      observability.Secret
	AWSSecretAccessKey  observability.Secret
	PendingReferenceTTL time.Duration
	ShutdownTimeout     time.Duration
	LogLevel            slog.Level
}

func Variables() []string {
	return []string{
		EnvDatabaseURL, EnvDBMaxConns, EnvDBLockTimeout, EnvHTTPAddr, EnvHTTPRequestTimeout,
		EnvOIDCIssuerURL, EnvOIDCKeysURL, EnvOIDCAudience, EnvSQSEndpoint, EnvAWSRegion,
		EnvAWSAccessKeyID, EnvAWSSecretAccessKey, EnvPendingReferenceTTL, EnvShutdownTimeout, EnvLogLevel,
	}
}

type reader struct {
	lookup func(string) string
	err    error
}

func LoadConfig(lookup func(string) string) (Config, error) {
	r := &reader{lookup: lookup}
	cfg := Config{
		DatabaseURL:         observability.NewSecret(r.required(EnvDatabaseURL)),
		DBMaxConns:          r.positiveInt(EnvDBMaxConns, 10),
		DBLockTimeout:       r.duration(EnvDBLockTimeout, 5*time.Second),
		HTTPAddr:            r.optional(EnvHTTPAddr, ":8080"),
		HTTPRequestTimeout:  r.duration(EnvHTTPRequestTimeout, 10*time.Second),
		OIDCIssuerURL:       r.required(EnvOIDCIssuerURL),
		OIDCKeysURL:         r.required(EnvOIDCKeysURL),
		OIDCAudience:        r.required(EnvOIDCAudience),
		SQSEndpoint:         r.required(EnvSQSEndpoint),
		AWSRegion:           r.required(EnvAWSRegion),
		AWSAccessKeyID:      observability.NewSecret(r.required(EnvAWSAccessKeyID)),
		AWSSecretAccessKey:  observability.NewSecret(r.required(EnvAWSSecretAccessKey)),
		PendingReferenceTTL: r.duration(EnvPendingReferenceTTL, 5*time.Minute),
		ShutdownTimeout:     r.duration(EnvShutdownTimeout, 25*time.Second),
		LogLevel:            r.logLevel(EnvLogLevel),
	}
	if r.err != nil {
		return Config{}, r.err
	}
	return cfg, nil
}

func (r *reader) fail(name, problem string) {
	if r.err == nil {
		r.err = fmt.Errorf("%w: %s %s", ErrInvalidConfig, name, problem)
	}
}

func (r *reader) required(name string) string {
	value := r.lookup(name)
	if value == "" {
		r.fail(name, "is required")
	}
	return value
}

func (r *reader) optional(name, fallback string) string {
	if value := r.lookup(name); value != "" {
		return value
	}
	return fallback
}

func (r *reader) positiveInt(name string, fallback int) int {
	raw := r.lookup(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		r.fail(name, "must be a positive integer")
	}
	return value
}

func (r *reader) duration(name string, fallback time.Duration) time.Duration {
	raw := r.lookup(name)
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		r.fail(name, "must be a positive duration such as 5s")
	}
	return value
}

func (r *reader) logLevel(name string) slog.Level {
	var level slog.Level
	if err := level.UnmarshalText([]byte(r.optional(name, "info"))); err != nil {
		r.fail(name, "must be debug, info, warn or error")
	}
	return level
}
