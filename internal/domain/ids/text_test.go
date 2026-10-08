package ids_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"jungle-gaming-challeng/internal/domain/ids"
)

func TestParseText(t *testing.T) {
	parsers := []struct {
		name  string
		parse func(string) (fmt.Stringer, error)
	}{
		{"ProviderID", func(s string) (fmt.Stringer, error) { return ids.ParseProviderID(s) }},
		{"ExternalTransactionID", func(s string) (fmt.Stringer, error) { return ids.ParseExternalTransactionID(s) }},
		{"RoundID", func(s string) (fmt.Stringer, error) { return ids.ParseRoundID(s) }},
		{"GameID", func(s string) (fmt.Stringer, error) { return ids.ParseGameID(s) }},
		{"IdempotencyKey", func(s string) (fmt.Stringer, error) { return ids.ParseIdempotencyKey(s) }},
	}

	longest := strings.Repeat("a", 255)
	inputs := []struct {
		name    string
		input   string
		want    string
		wantErr error
	}{
		{"valid", "round-987", "round-987", nil},
		{"space inside", "fortune chimp", "fortune chimp", nil},
		{"255 characters", longest, longest, nil},
		{"255 accented characters", strings.Repeat("é", 255), strings.Repeat("é", 255), nil},
		{"NUL inside", "a\x00b", "", ids.ErrInvalid},
		{"invalid UTF-8", "a\xffb", "", ids.ErrInvalid},
		{"256 characters", longest + "a", "", ids.ErrInvalid},
		{"empty", "", "", ids.ErrInvalid},
		{"leading space", " round-987", "", ids.ErrInvalid},
		{"trailing space", "round-987 ", "", ids.ErrInvalid},
		{"only spaces", "   ", "", ids.ErrInvalid},
	}

	for _, p := range parsers {
		for _, in := range inputs {
			t.Run(p.name+"/"+in.name, func(t *testing.T) {
				got, err := p.parse(in.input)
				if !errors.Is(err, in.wantErr) {
					t.Fatalf("err = %v, want %v", err, in.wantErr)
				}
				if got.String() != in.want {
					t.Fatalf("String() = %q, want %q", got.String(), in.want)
				}
			})
		}
	}
}

func TestTextIsZero(t *testing.T) {
	var none ids.ProviderID
	if !none.IsZero() {
		t.Fatal("IsZero() of the zero value = false, want true")
	}

	parsed, err := ids.ParseProviderID("provider-a")
	if err != nil {
		t.Fatalf("ParseProviderID: %v", err)
	}
	if parsed.IsZero() {
		t.Fatal("IsZero() of a parsed id = true, want false")
	}
}

func TestIDsMarshalAsText(t *testing.T) {
	wallet, err := ids.ParseWalletID(sampleUUID)
	if err != nil {
		t.Fatalf("ParseWalletID: %v", err)
	}
	provider, err := ids.ParseProviderID("provider-a")
	if err != nil {
		t.Fatalf("ParseProviderID: %v", err)
	}

	got, err := json.Marshal(struct {
		WalletID   ids.WalletID   `json:"walletId"`
		ProviderID ids.ProviderID `json:"providerId"`
	}{wallet, provider})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"walletId":"` + sampleUUID + `","providerId":"provider-a"}`
	if string(got) != want {
		t.Fatalf("Marshal = %s, want %s", got, want)
	}
}
