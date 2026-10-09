package app_test

import (
	"errors"
	"fmt"
	"testing"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/domain/ids"
)

func TestSentinelsSurviveWrapping(t *testing.T) {
	sentinels := []error{app.ErrTransient, app.ErrLockTimeout, app.ErrUniqueViolation, app.ErrNotFound, app.ErrForbidden, app.ErrIdPUnavailable}

	for _, sentinel := range sentinels {
		t.Run(sentinel.Error(), func(t *testing.T) {
			wrapped := fmt.Errorf("insert transaction: %w", sentinel)

			for _, other := range sentinels {
				if errors.Is(wrapped, other) != (other == sentinel) {
					t.Fatalf("errors.Is(%v, %v) = %t", wrapped, other, other != sentinel)
				}
			}
		})
	}
}

func TestCaller(t *testing.T) {
	provider, err := ids.ParseProviderID("provider-a")
	if err != nil {
		t.Fatalf("ParseProviderID: %v", err)
	}

	tests := []struct {
		name       string
		caller     app.Caller
		scope      string
		hasScope   bool
		isProvider bool
	}{
		{"provider with the scope", app.Caller{ProviderID: provider, Scopes: []string{app.ScopeWageringWrite, app.ScopeWageringRead}}, app.ScopeWageringRead, true, true},
		{"provider without the scope", app.Caller{ProviderID: provider, Scopes: []string{app.ScopeWageringRead}}, app.ScopeWalletsWrite, false, true},
		{"internal service", app.Caller{Scopes: []string{app.ScopeWalletsRead, app.ScopeWalletsWrite}}, app.ScopeWalletsWrite, true, false},
		{"no scopes", app.Caller{}, app.ScopeWalletsRead, false, false},
		{"scope is not matched by prefix", app.Caller{Scopes: []string{"wallets:readonly"}}, app.ScopeWalletsRead, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.caller.HasScope(tt.scope); got != tt.hasScope {
				t.Fatalf("HasScope(%q) = %t, want %t", tt.scope, got, tt.hasScope)
			}
			if got := tt.caller.IsProvider(); got != tt.isProvider {
				t.Fatalf("IsProvider() = %t, want %t", got, tt.isProvider)
			}
		})
	}
}

func TestCallerProviderPolicy(t *testing.T) {
	providerA, err := ids.ParseProviderID("provider-a")
	if err != nil {
		t.Fatalf("ParseProviderID: %v", err)
	}
	providerB, err := ids.ParseProviderID("provider-b")
	if err != nil {
		t.Fatalf("ParseProviderID: %v", err)
	}

	tests := []struct {
		name      string
		caller    app.Caller
		target    ids.ProviderID
		canSubmit bool
		canRead   bool
	}{
		{"same provider", app.Caller{ProviderID: providerA}, providerA, true, true},
		{"another provider", app.Caller{ProviderID: providerA}, providerB, false, false},
		{"internal service", app.Caller{}, providerA, false, true},
		{"provider reading an internal transaction", app.Caller{ProviderID: providerA}, ids.ProviderID{}, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.caller.CanSubmitAs(tt.target); got != tt.canSubmit {
				t.Fatalf("CanSubmitAs = %t, want %t", got, tt.canSubmit)
			}
			if got := tt.caller.CanReadProvider(tt.target); got != tt.canRead {
				t.Fatalf("CanReadProvider = %t, want %t", got, tt.canRead)
			}
		})
	}
}
