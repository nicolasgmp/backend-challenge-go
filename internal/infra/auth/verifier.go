package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/domain/ids"
)

var ErrInvalidToken = errors.New("auth: invalid token")

type Config struct {
	IssuerURL string
	KeysURL   string
	Audience  string
	Now       func() time.Time
}

type Verifier struct {
	verifier *oidc.IDTokenVerifier
}

type claims struct {
	ProviderID string `json:"provider_id"`
	Scope      string `json:"scope"`
}

func NewVerifier(ctx context.Context, cfg Config) *Verifier {
	keys := oidc.NewRemoteKeySet(ctx, cfg.KeysURL)
	return &Verifier{verifier: oidc.NewVerifier(cfg.IssuerURL, keys, &oidc.Config{ClientID: cfg.Audience, Now: cfg.Now})}
}

func (v *Verifier) Verify(ctx context.Context, rawToken string) (app.Caller, error) {
	token, err := v.verifier.Verify(ctx, rawToken)
	if err != nil {
		return app.Caller{}, ErrInvalidToken
	}
	var parsed claims
	if err := token.Claims(&parsed); err != nil {
		return app.Caller{}, ErrInvalidToken
	}
	return callerFrom(parsed)
}

func callerFrom(parsed claims) (app.Caller, error) {
	caller := app.Caller{Scopes: strings.Fields(parsed.Scope)}
	if parsed.ProviderID == "" {
		return caller, nil
	}
	providerID, err := ids.ParseProviderID(parsed.ProviderID)
	if err != nil {
		return app.Caller{}, ErrInvalidToken
	}
	caller.ProviderID = providerID
	return caller, nil
}

func Ping(ctx context.Context, keysURL string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, keysURL, nil)
	if err != nil {
		return fmt.Errorf("%w: invalid keys url", app.ErrIdPUnavailable)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("%w: signing keys could not be fetched", app.ErrIdPUnavailable)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: keys endpoint answered %d", app.ErrIdPUnavailable, response.StatusCode)
	}
	return nil
}
