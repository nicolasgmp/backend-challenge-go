package ids_test

import (
	"errors"
	"fmt"
	"testing"

	"jungle-gaming-challeng/internal/domain/ids"
)

const (
	sampleUUID = "0192f291-27dd-7d3f-8071-5f8685deef37"
	zeroUUID   = "00000000-0000-0000-0000-000000000000"
)

func TestParseUUID(t *testing.T) {
	parsers := []struct {
		name  string
		parse func(string) (fmt.Stringer, error)
	}{
		{"WalletID", func(s string) (fmt.Stringer, error) { return ids.ParseWalletID(s) }},
		{"PlayerID", func(s string) (fmt.Stringer, error) { return ids.ParsePlayerID(s) }},
		{"TransactionID", func(s string) (fmt.Stringer, error) { return ids.ParseTransactionID(s) }},
		{"EntryID", func(s string) (fmt.Stringer, error) { return ids.ParseEntryID(s) }},
		{"EventID", func(s string) (fmt.Stringer, error) { return ids.ParseEventID(s) }},
	}
	inputs := []struct {
		name    string
		input   string
		want    string
		wantErr error
	}{
		{"valid", sampleUUID, sampleUUID, nil},
		{"malformed", "not-a-uuid", zeroUUID, ids.ErrInvalid},
		{"empty", "", zeroUUID, ids.ErrInvalid},
		{"all zeros", zeroUUID, zeroUUID, ids.ErrInvalid},
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

func TestUUIDIsZero(t *testing.T) {
	var none ids.WalletID
	if !none.IsZero() {
		t.Fatal("IsZero() of the zero value = false, want true")
	}

	parsed, err := ids.ParseWalletID(sampleUUID)
	if err != nil {
		t.Fatalf("ParseWalletID: %v", err)
	}
	if parsed.IsZero() {
		t.Fatal("IsZero() of a parsed id = true, want false")
	}
}

func TestNewIDs(t *testing.T) {
	generators := []struct {
		name string
		next func() (fmt.Stringer, error)
	}{
		{"WalletID", func() (fmt.Stringer, error) { return ids.NewWalletID() }},
		{"TransactionID", func() (fmt.Stringer, error) { return ids.NewTransactionID() }},
		{"EntryID", func() (fmt.Stringer, error) { return ids.NewEntryID() }},
		{"EventID", func() (fmt.Stringer, error) { return ids.NewEventID() }},
	}

	for _, g := range generators {
		t.Run(g.name, func(t *testing.T) {
			previous := ""
			for i := 0; i < 100; i++ {
				id, err := g.next()
				if err != nil {
					t.Fatalf("generate: %v", err)
				}
				current := id.String()
				if current[14] != '7' {
					t.Fatalf("%s is not a version 7 UUID", current)
				}
				if current <= previous {
					t.Fatalf("%s does not sort after %s", current, previous)
				}
				previous = current
			}
		})
	}
}
