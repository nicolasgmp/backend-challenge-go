package app

import (
	"slices"

	"jungle-gaming-challeng/internal/domain/ids"
)

const (
	ScopeWalletsRead   = "wallets:read"
	ScopeWalletsWrite  = "wallets:write"
	ScopeWageringRead  = "wagering:read"
	ScopeWageringWrite = "wagering:write"
)

type Caller struct {
	ProviderID ids.ProviderID
	Scopes     []string
}

func (c Caller) HasScope(scope string) bool {
	return slices.Contains(c.Scopes, scope)
}

func (c Caller) IsProvider() bool {
	return !c.ProviderID.IsZero()
}
