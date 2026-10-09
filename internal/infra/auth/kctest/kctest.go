//go:build integration

package kctest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	Image    = "quay.io/keycloak/keycloak:26.7.5"
	Realm    = "wallet"
	Audience = "wallet-service"

	InternalClient  = "wallet-internal"
	ProviderAClient = "provider-a"
	ProviderBClient = "provider-b"

	InternalSecret  = "local-test-internal-secret"
	ProviderASecret = "local-test-provider-a-secret"
	ProviderBSecret = "local-test-provider-b-secret"

	adminUser     = "admin"
	adminPassword = "local-test-admin-password"
	port          = "8080/tcp"
)

type Server struct {
	baseURL   string
	container testcontainers.Container
}

func Start(ctx context.Context, tb testing.TB, realmFile string) *Server {
	tb.Helper()

	ctr, err := testcontainers.Run(ctx, Image,
		testcontainers.WithExposedPorts(port),
		testcontainers.WithCmd("start-dev", "--import-realm"),
		testcontainers.WithEnv(map[string]string{
			"KC_BOOTSTRAP_ADMIN_USERNAME":       adminUser,
			"KC_BOOTSTRAP_ADMIN_PASSWORD":       adminPassword,
			"KEYCLOAK_INTERNAL_CLIENT_SECRET":   InternalSecret,
			"KEYCLOAK_PROVIDER_A_CLIENT_SECRET": ProviderASecret,
			"KEYCLOAK_PROVIDER_B_CLIENT_SECRET": ProviderBSecret,
		}),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			HostFilePath:      realmFile,
			ContainerFilePath: "/opt/keycloak/data/import/wallet-realm.json",
			FileMode:          0o644,
		}),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP("/realms/"+Realm+"/.well-known/openid-configuration").WithPort(port).WithStartupTimeout(3*time.Minute),
		),
	)
	testcontainers.CleanupContainer(tb, ctr)
	if err != nil {
		tb.Fatalf("kctest: start %s: %v", Image, err)
	}
	endpoint, err := ctr.PortEndpoint(ctx, port, "http")
	if err != nil {
		tb.Fatalf("kctest: endpoint: %v", err)
	}
	return &Server{baseURL: endpoint, container: ctr}
}

func (s *Server) IssuerURL() string {
	return s.baseURL + "/realms/" + Realm
}

func (s *Server) KeysURL() string {
	return s.IssuerURL() + "/protocol/openid-connect/certs"
}

func (s *Server) Stop(ctx context.Context, tb testing.TB) {
	tb.Helper()

	if err := s.container.Stop(ctx, nil); err != nil {
		tb.Fatalf("kctest: stop: %v", err)
	}
}

func (s *Server) Token(ctx context.Context, tb testing.TB, clientID, clientSecret string) string {
	tb.Helper()

	return s.requestToken(ctx, tb, Realm, url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
	})
}

func (s *Server) TokenFromAnotherRealm(ctx context.Context, tb testing.TB) string {
	tb.Helper()

	return s.requestToken(ctx, tb, "master", url.Values{
		"grant_type": {"password"},
		"client_id":  {"admin-cli"},
		"username":   {adminUser},
		"password":   {adminPassword},
	})
}

func (s *Server) requestToken(ctx context.Context, tb testing.TB, realm string, form url.Values) string {
	tb.Helper()

	endpoint := s.baseURL + "/realms/" + realm + "/protocol/openid-connect/token"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		tb.Fatalf("kctest: token request: %v", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		tb.Fatalf("kctest: token request: %v", err)
	}
	defer response.Body.Close()

	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil || body.AccessToken == "" {
		tb.Fatalf("kctest: token request for realm %s answered %d without a token", realm, response.StatusCode)
	}
	return body.AccessToken
}
