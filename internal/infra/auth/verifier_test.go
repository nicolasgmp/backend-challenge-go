//go:build integration

package auth_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/infra/auth"
	"jungle-gaming-challeng/internal/infra/auth/kctest"
)

const realmFile = "../../../deploy/keycloak/wallet-realm.json"

func verifier(server *kctest.Server, audience string, now func() time.Time) *auth.Verifier {
	return auth.NewVerifier(context.Background(), auth.Config{
		IssuerURL: server.IssuerURL(),
		KeysURL:   server.KeysURL(),
		Audience:  audience,
		Now:       now,
	})
}

func TestVerifierAgainstKeycloak(t *testing.T) {
	ctx := context.Background()
	server := kctest.Start(ctx, t, realmFile)
	valid := verifier(server, kctest.Audience, nil)

	providerToken := server.Token(ctx, t, kctest.ProviderAClient, kctest.ProviderASecret)
	internalToken := server.Token(ctx, t, kctest.InternalClient, kctest.InternalSecret)

	t.Run("provider token", func(t *testing.T) {
		caller, err := valid.Verify(ctx, providerToken)
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if caller.ProviderID.String() != "provider-a" {
			t.Fatalf("provider = %q, want provider-a", caller.ProviderID)
		}
		slices.Sort(caller.Scopes)
		if !slices.Equal(caller.Scopes, []string{app.ScopeWageringRead, app.ScopeWageringWrite}) {
			t.Fatalf("scopes = %v, want only the two wagering scopes", caller.Scopes)
		}
	})

	t.Run("internal service token", func(t *testing.T) {
		caller, err := valid.Verify(ctx, internalToken)
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		slices.Sort(caller.Scopes)
		if caller.IsProvider() || !slices.Equal(caller.Scopes, []string{app.ScopeWageringRead, app.ScopeWalletsRead, app.ScopeWalletsWrite}) {
			t.Fatalf("caller = %+v, want no provider and the three internal scopes", caller)
		}
	})

	t.Run("each provider gets its own identity", func(t *testing.T) {
		caller, err := valid.Verify(ctx, server.Token(ctx, t, kctest.ProviderBClient, kctest.ProviderBSecret))
		if err != nil || caller.ProviderID.String() != "provider-b" {
			t.Fatalf("caller = %+v, %v, want provider-b", caller, err)
		}
	})

	header, rest, _ := strings.Cut(providerToken, ".")
	payload, signature, _ := strings.Cut(rest, ".")
	inOneHour := func() time.Time { return time.Now().Add(time.Hour) }

	rejected := map[string]struct {
		verifier *auth.Verifier
		token    string
	}{
		"tampered payload":    {valid, header + "." + strings.Replace(payload, payload[10:14], "AAAA", 1) + "." + signature},
		"tampered signature":  {valid, header + "." + payload + "." + strings.Repeat("A", len(signature))},
		"payload of another":  {valid, header + "." + strings.Split(internalToken, ".")[1] + "." + signature},
		"expired":             {verifier(server, kctest.Audience, inOneHour), providerToken},
		"another issuer":      {valid, server.TokenFromAnotherRealm(ctx, t)},
		"issuer that differs": {auth.NewVerifier(ctx, auth.Config{IssuerURL: server.IssuerURL() + "/other", KeysURL: server.KeysURL(), Audience: kctest.Audience}), providerToken},
		"another audience":    {verifier(server, "another-service", nil), providerToken},
		"not a token":         {valid, "not-a-token"},
		"empty":               {valid, ""},
		"unsigned (alg none)": {valid, "eyJhbGciOiJub25lIn0." + payload + "."},
	}
	for name, tt := range rejected {
		t.Run(name, func(t *testing.T) {
			caller, err := tt.verifier.Verify(ctx, tt.token)
			if !errors.Is(err, auth.ErrInvalidToken) {
				t.Fatalf("err = %v, want %v", err, auth.ErrInvalidToken)
			}
			if caller.IsProvider() || len(caller.Scopes) != 0 {
				t.Fatalf("caller = %+v, want it empty for a rejected token", caller)
			}
		})
	}

	t.Run("identity provider down without cached keys", func(t *testing.T) {
		cold := verifier(server, kctest.Audience, nil)
		server.Stop(ctx, t)

		_, err := cold.Verify(ctx, providerToken)
		if !errors.Is(err, app.ErrIdPUnavailable) {
			t.Fatalf("err = %v, want %v", err, app.ErrIdPUnavailable)
		}
		if strings.Contains(err.Error(), providerToken) {
			t.Fatal("the error contains the token")
		}

		if _, err := valid.Verify(ctx, providerToken); err != nil {
			t.Fatalf("a verifier with cached keys rejected a valid token while the IdP was down: %v", err)
		}
	})
}
