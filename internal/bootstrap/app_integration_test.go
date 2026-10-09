//go:build integration

package bootstrap_test

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"go.uber.org/fx"

	"jungle-gaming-challeng/internal/bootstrap"
	"jungle-gaming-challeng/internal/infra/auth/kctest"
	"jungle-gaming-challeng/internal/infra/observability/logtest"
	"jungle-gaming-challeng/internal/infra/postgres/pgtest"
	"jungle-gaming-challeng/internal/infra/sqs"
	"jungle-gaming-challeng/internal/infra/sqs/sqstest"
)

func TestMain(m *testing.M) {
	pgtest.Main(m, "../../migrations")
}

type stack struct {
	env      map[string]string
	keycloak *kctest.Server
}

func newStack(t *testing.T) stack {
	t.Helper()

	ctx := context.Background()
	keycloak := kctest.Start(ctx, t, "../../deploy/keycloak/wallet-realm.json")
	endpoint := sqstest.StartEndpoint(ctx, t)
	client, err := sqs.NewClient(sqs.Config{
		Endpoint: endpoint, Region: sqstest.Region,
		AccessKeyID: sqstest.LocalAccessKeyID, SecretAccessKey: sqstest.LocalSecretAccessKey,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := sqs.EnsureQueues(ctx, client); err != nil {
		t.Fatalf("EnsureQueues: %v", err)
	}

	return stack{keycloak: keycloak, env: map[string]string{
		bootstrap.EnvDatabaseURL:        pgtest.URL(pgtest.NewDatabaseName(ctx, t, "migrated")),
		bootstrap.EnvHTTPAddr:           "127.0.0.1:0",
		bootstrap.EnvOIDCIssuerURL:      keycloak.IssuerURL(),
		bootstrap.EnvOIDCKeysURL:        keycloak.KeysURL(),
		bootstrap.EnvOIDCAudience:       kctest.Audience,
		bootstrap.EnvSQSEndpoint:        endpoint,
		bootstrap.EnvAWSRegion:          sqstest.Region,
		bootstrap.EnvAWSAccessKeyID:     sqstest.LocalAccessKeyID,
		bootstrap.EnvAWSSecretAccessKey: sqstest.LocalSecretAccessKey,
		bootstrap.EnvShutdownTimeout:    "20s",
	}}
}

var listeningOn = regexp.MustCompile(`"addr":"([^"]+)"`)

func position(t *testing.T, logs, text string) int {
	t.Helper()

	index := strings.Index(logs, text)
	if index < 0 {
		t.Fatalf("the logs do not contain %q:\n%s", text, logs)
	}
	return index
}

func TestApplicationStartsAndStopsTwice(t *testing.T) {
	ctx := context.Background()
	s := newStack(t)
	token := s.keycloak.Token(ctx, t, kctest.InternalClient, kctest.InternalSecret)
	baseline := runtime.NumGoroutine()

	for round := 1; round <= 2; round++ {
		logs := &logtest.Buffer{}
		application := fx.New(bootstrap.Options(lookupIn(s.env), logs))
		if err := application.Start(ctx); err != nil {
			t.Fatalf("round %d: Start: %v", round, err)
		}

		match := listeningOn.FindStringSubmatch(logs.String())
		if match == nil {
			t.Fatalf("round %d: the logs do not show the listening address:\n%s", round, logs.String())
		}
		base := "http://" + match[1]

		ready, err := http.Get(base + "/health/ready")
		if err != nil || ready.StatusCode != http.StatusOK {
			t.Fatalf("round %d: readiness = %v, %v, want 200", round, ready, err)
		}
		ready.Body.Close()

		body := `{"playerId":"0192f28f-5dc0-7d58-bdb2-814ad6a0f4a` + string(rune('0'+round)) + `","initialBalance":{"amount":"1000.00","currency":"BRL"}}`
		request, err := http.NewRequest(http.MethodPost, base+"/wallets", strings.NewReader(body))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		opened, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("round %d: open wallet: %v", round, err)
		}
		answer, _ := io.ReadAll(opened.Body)
		opened.Body.Close()
		if opened.StatusCode != http.StatusCreated {
			t.Fatalf("round %d: open wallet = %d %s, want 201", round, opened.StatusCode, answer)
		}

		if err := application.Stop(ctx); err != nil {
			t.Fatalf("round %d: Stop: %v", round, err)
		}

		output := logs.String()
		readinessOff := position(t, output, "shutdown started, readiness is now false")
		httpStopped := position(t, output, "http server stopped")
		consumerStopped := position(t, output, "consumer stopped")
		poolClosed := position(t, output, "database pool closed")
		if position(t, output, "sqs connections closed") < consumerStopped {
			t.Fatalf("round %d: the sqs connections were closed before the consumer stopped:\n%s", round, output)
		}
		if !(readinessOff < httpStopped && httpStopped < consumerStopped && consumerStopped < poolClosed) {
			t.Fatalf("round %d: shutdown order is wrong, want readiness, HTTP, workers, database:\n%s", round, output)
		}
		if strings.Count(output, "worker stopped") != 2 || position(t, output, `"worker":"outbox-publisher"`) > poolClosed || position(t, output, `"worker":"pending-references"`) > poolClosed {
			t.Fatalf("round %d: want both workers stopped before the database pool closes:\n%s", round, output)
		}
		if _, err := http.Get(base + "/health/live"); err == nil {
			t.Fatalf("round %d: the HTTP server still answers after Stop", round)
		}
		logtest.AssertNoLeak(t, output, token, "local-test-password", sqstest.LocalSecretAccessKey, "1000.00")
	}

	http.DefaultClient.CloseIdleConnections()
	deadline := time.Now().Add(10 * time.Second)
	for runtime.NumGoroutine() > baseline+2 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if now := runtime.NumGoroutine(); now > baseline+2 {
		t.Fatalf("goroutines = %d after two start and stop cycles, %d before: something was left running", now, baseline)
	}
}

func TestApplicationDoesNotStartWithoutItsDependencies(t *testing.T) {
	ctx := context.Background()
	s := newStack(t)

	tests := map[string]func(map[string]string){
		"database unreachable": func(env map[string]string) {
			env[bootstrap.EnvDatabaseURL] = "postgres://wallet:local-test-password@127.0.0.1:1/wallet?sslmode=disable"
		},
		"queues missing":            func(env map[string]string) { env[bootstrap.EnvSQSEndpoint] = "http://127.0.0.1:1" },
		"identity provider missing": func(env map[string]string) { env[bootstrap.EnvOIDCKeysURL] = "http://127.0.0.1:1/certs" },
	}

	for name, breakIt := range tests {
		t.Run(name, func(t *testing.T) {
			env := map[string]string{}
			for key, value := range s.env {
				env[key] = value
			}
			breakIt(env)
			logs := &logtest.Buffer{}

			application := fx.New(bootstrap.Options(lookupIn(env), logs))
			startCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
			defer cancel()
			err := application.Start(startCtx)
			if err == nil {
				_ = application.Stop(ctx)
				t.Fatal("the application started without one of its dependencies")
			}
			logtest.AssertNoLeak(t, logs.String()+err.Error(), "local-test-password", sqstest.LocalSecretAccessKey)
		})
	}
}
