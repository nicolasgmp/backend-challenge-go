package bootstrap

import (
	"errors"
	"fmt"
	"time"

	"jungle-gaming-challeng/internal/infra/observability"
)

const (
	EnvDatabaseURL         = "DATABASE_URL"
	EnvDBLockTimeout       = "DB_LOCK_TIMEOUT"
	EnvHTTPAddr            = "HTTP_ADDR"
	EnvOIDCIssuerURL       = "OIDC_ISSUER_URL"
	EnvOIDCKeysURL         = "OIDC_KEYS_URL"
	EnvOIDCAudience        = "OIDC_AUDIENCE"
	EnvSQSEndpoint         = "SQS_ENDPOINT"
	EnvAWSRegion           = "AWS_REGION"
	EnvAWSAccessKeyID      = "AWS_ACCESS_KEY_ID"
	EnvAWSSecretAccessKey  = "AWS_SECRET_ACCESS_KEY"
	EnvPendingReferenceTTL = "PENDING_REFERENCE_TTL"
	EnvShutdownTimeout     = "SHUTDOWN_TIMEOUT"
)

var ErrInvalidConfig = errors.New("bootstrap: invalid configuration")

type Config struct {
	DatabaseURL         observability.Secret
	DBLockTimeout       time.Duration
	HTTPAddr            string
	OIDCIssuerURL       string
	OIDCKeysURL         string
	OIDCAudience        string
	SQSEndpoint         string
	AWSRegion           string
	AWSAccessKeyID      observability.Secret
	AWSSecretAccessKey  observability.Secret
	PendingReferenceTTL time.Duration
	ShutdownTimeout     time.Duration
}

type reader struct {
	lookup func(string) string
	err    error
}

func LoadConfig(lookup func(string) string) (Config, error) {
	r := &reader{lookup: lookup}
	cfg := Config{
		DatabaseURL:         observability.NewSecret(r.required(EnvDatabaseURL)),
		DBLockTimeout:       r.duration(EnvDBLockTimeout, 5*time.Second),
		HTTPAddr:            r.optional(EnvHTTPAddr, ":8080"),
		OIDCIssuerURL:       r.required(EnvOIDCIssuerURL),
		OIDCKeysURL:         r.required(EnvOIDCKeysURL),
		OIDCAudience:        r.required(EnvOIDCAudience),
		SQSEndpoint:         r.required(EnvSQSEndpoint),
		AWSRegion:           r.required(EnvAWSRegion),
		AWSAccessKeyID:      observability.NewSecret(r.required(EnvAWSAccessKeyID)),
		AWSSecretAccessKey:  observability.NewSecret(r.required(EnvAWSSecretAccessKey)),
		PendingReferenceTTL: r.duration(EnvPendingReferenceTTL, 5*time.Minute),
		ShutdownTimeout:     r.duration(EnvShutdownTimeout, 25*time.Second),
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
