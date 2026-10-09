package auth

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"jungle-gaming-challeng/internal/app"
)

func TestCallerFromClaims(t *testing.T) {
	tests := []struct {
		name         string
		claims       claims
		wantProvider string
		wantScopes   []string
		wantErr      error
	}{
		{"provider with two scopes", claims{ProviderID: "provider-a", Scope: "wagering:write wagering:read"}, "provider-a", []string{"wagering:write", "wagering:read"}, nil},
		{"internal service", claims{Scope: "wallets:write wallets:read wagering:read"}, "", []string{"wallets:write", "wallets:read", "wagering:read"}, nil},
		{"single scope", claims{ProviderID: "provider-a", Scope: "wagering:read"}, "provider-a", []string{"wagering:read"}, nil},
		{"no scope", claims{ProviderID: "provider-a"}, "provider-a", nil, nil},
		{"extra spaces between scopes", claims{Scope: "  wallets:read   wagering:read "}, "", []string{"wallets:read", "wagering:read"}, nil},
		{"provider id that is not a valid identifier", claims{ProviderID: strings.Repeat("a", 256), Scope: "wagering:read"}, "", nil, ErrInvalidToken},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caller, err := callerFrom(tt.claims)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if caller.ProviderID.String() != tt.wantProvider || caller.IsProvider() != (tt.wantProvider != "") {
				t.Fatalf("provider = %q, want %q", caller.ProviderID, tt.wantProvider)
			}
			if !slices.Equal(caller.Scopes, tt.wantScopes) {
				t.Fatalf("scopes = %v, want %v", caller.Scopes, tt.wantScopes)
			}
		})
	}
}

func TestClassify(t *testing.T) {
	unavailable := errors.New("failed to verify signature: fetching keys oidc: get keys failed Get \"http://idp/certs\": connection refused")
	if !errors.Is(classify(unavailable), app.ErrIdPUnavailable) {
		t.Fatal("a failure to fetch keys is not reported as the IdP being unavailable")
	}
	if strings.Contains(classify(unavailable).Error(), "http://idp") {
		t.Fatal("the error exposes the address of the IdP")
	}

	for _, message := range []string{"oidc: token is expired", "failed to verify signature: failed to verify id token signature", "oidc: malformed jwt"} {
		if !errors.Is(classify(errors.New(message)), ErrInvalidToken) {
			t.Fatalf("%q is not reported as an invalid token", message)
		}
	}
}
