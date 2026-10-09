package bootstrap_test

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.uber.org/fx"

	"jungle-gaming-challeng/internal/bootstrap"
)

const (
	databaseURL = "postgres://wallet:s3cr3t-db-password@db:5432/wallet"
	awsSecret   = "s3cr3t-aws-key"
)

func completeEnv() map[string]string {
	return map[string]string{
		bootstrap.EnvDatabaseURL:        databaseURL,
		bootstrap.EnvOIDCIssuerURL:      "http://keycloak:8080/realms/wallet",
		bootstrap.EnvOIDCKeysURL:        "http://keycloak:8080/realms/wallet/protocol/openid-connect/certs",
		bootstrap.EnvOIDCAudience:       "wallet-service",
		bootstrap.EnvSQSEndpoint:        "http://ministack:4566",
		bootstrap.EnvAWSRegion:          "us-east-1",
		bootstrap.EnvAWSAccessKeyID:     "local-key-id",
		bootstrap.EnvAWSSecretAccessKey: awsSecret,
	}
}

func lookupIn(env map[string]string) func(string) string {
	return func(name string) string { return env[name] }
}

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := bootstrap.LoadConfig(lookupIn(completeEnv()))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.DatabaseURL.Reveal() != databaseURL || cfg.AWSSecretAccessKey.Reveal() != awsSecret || cfg.OIDCAudience != "wallet-service" {
		t.Fatal("the required values were not read")
	}
	if cfg.DBMaxConns != 10 || cfg.DBLockTimeout != 5*time.Second || cfg.HTTPAddr != ":8080" || cfg.HTTPRequestTimeout != 10*time.Second {
		t.Fatalf("defaults = %d, %s, %s, %s, want 10, 5s, :8080, 10s", cfg.DBMaxConns, cfg.DBLockTimeout, cfg.HTTPAddr, cfg.HTTPRequestTimeout)
	}
	if cfg.PendingReferenceTTL != 5*time.Minute || cfg.ShutdownTimeout != 25*time.Second || cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("defaults = %s, %s, %s, want 5m, 25s, INFO", cfg.PendingReferenceTTL, cfg.ShutdownTimeout, cfg.LogLevel)
	}

	for _, printed := range []string{fmt.Sprintf("%v", cfg), fmt.Sprintf("%+v", cfg), fmt.Sprintf("%#v", cfg)} {
		if strings.Contains(printed, "s3cr3t") || !strings.Contains(printed, "[REDACTED]") {
			t.Fatalf("printed configuration exposes a secret: %s", printed)
		}
	}
}

func TestLoadConfigRejects(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{bootstrap.EnvDatabaseURL, ""},
		{bootstrap.EnvOIDCIssuerURL, ""},
		{bootstrap.EnvOIDCKeysURL, ""},
		{bootstrap.EnvOIDCAudience, ""},
		{bootstrap.EnvSQSEndpoint, ""},
		{bootstrap.EnvAWSRegion, ""},
		{bootstrap.EnvAWSAccessKeyID, ""},
		{bootstrap.EnvAWSSecretAccessKey, ""},
		{bootstrap.EnvDBMaxConns, "zero-s3cr3t"},
		{bootstrap.EnvDBMaxConns, "0"},
		{bootstrap.EnvDBLockTimeout, "five-s3cr3t"},
		{bootstrap.EnvDBLockTimeout, "-5s"},
		{bootstrap.EnvHTTPRequestTimeout, "10"},
		{bootstrap.EnvPendingReferenceTTL, "0s"},
		{bootstrap.EnvShutdownTimeout, "soon"},
		{bootstrap.EnvLogLevel, "loud-s3cr3t"},
	}

	for _, tt := range tests {
		t.Run(tt.name+"="+tt.value, func(t *testing.T) {
			env := completeEnv()
			env[tt.name] = tt.value

			_, err := bootstrap.LoadConfig(lookupIn(env))
			if !errors.Is(err, bootstrap.ErrInvalidConfig) {
				t.Fatalf("err = %v, want %v", err, bootstrap.ErrInvalidConfig)
			}
			if !strings.Contains(err.Error(), tt.name) || strings.Contains(err.Error(), "s3cr3t") {
				t.Fatalf("message = %q, want the variable name and never a value", err.Error())
			}
		})
	}
}

func TestValidateApp(t *testing.T) {
	if err := fx.ValidateApp(bootstrap.Options(lookupIn(completeEnv()), io.Discard)); err != nil {
		t.Fatalf("ValidateApp: %v", err)
	}
}

func TestOptionsFailWithoutARequiredVariable(t *testing.T) {
	env := completeEnv()
	delete(env, bootstrap.EnvDatabaseURL)

	err := fx.New(bootstrap.Options(lookupIn(env), io.Discard), fx.NopLogger).Err()
	if !errors.Is(err, bootstrap.ErrInvalidConfig) || !strings.Contains(err.Error(), bootstrap.EnvDatabaseURL) {
		t.Fatalf("err = %v, want the missing variable named", err)
	}
}
