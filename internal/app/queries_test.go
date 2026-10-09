package app_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"jungle-gaming-challeng/internal/app"
	"jungle-gaming-challeng/internal/domain/failure"
	"jungle-gaming-challeng/internal/domain/ids"
	"jungle-gaming-challeng/internal/domain/wager"
	"jungle-gaming-challeng/internal/domain/wallet"
)

func TestGetWallet(t *testing.T) {
	f := newFixture()
	w := f.open(t, 100000)
	f.submit(t, operation(t, w, wager.Bet, 2500, "tx-1", ""))

	got, err := f.service.GetWallet(t.Context(), w.ID)
	if err != nil || got.Balance.Amount() != "975.00" || got.Version != 2 {
		t.Fatalf("GetWallet = %+v, %v, want 975.00 at version 2", got, err)
	}

	_, err = f.service.GetWallet(t.Context(), parsed(t, ids.ParseWalletID, otherUUID))
	wantInvalidInput(t, err, failure.WalletNotFound)
}

func TestListLedgerPages(t *testing.T) {
	f := newFixture()
	w := f.open(t, 100000)
	for _, externalID := range []string{"tx-1", "tx-2", "tx-3", "tx-4"} {
		f.submit(t, operation(t, w, wager.Bet, 100, externalID, ""))
	}

	var versions []int64
	cursor := ""
	for page := 1; page <= 3; page++ {
		got, err := f.service.ListLedger(t.Context(), w.ID, cursor, 2)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		for _, entry := range got.Entries {
			versions = append(versions, entry.Fields().WalletVersion)
		}
		if page == 2 {
			f.submit(t, operation(t, w, wager.Bet, 100, "tx-5", ""))
		}
		if (got.NextCursor == "") != (page == 3) {
			t.Fatalf("page %d: next cursor = %q", page, got.NextCursor)
		}
		cursor = got.NextCursor
	}
	if !slices.Equal(versions, []int64{1, 2, 3, 4, 5, 6}) {
		t.Fatalf("versions = %v, want each entry once, in order, including the one created during navigation", versions)
	}
}

func TestListLedgerRejects(t *testing.T) {
	f := newFixture()
	w := f.open(t, 100000)
	f.submit(t, operation(t, w, wager.Bet, 100, "tx-1", ""))
	f.submit(t, operation(t, w, wager.Bet, 100, "tx-2", ""))
	first, err := f.service.ListLedger(t.Context(), w.ID, "", 1)
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first page = %+v, %v, want a next cursor", first, err)
	}
	if strings.Contains(first.NextCursor, "1") {
		t.Fatalf("cursor = %q, want it opaque", first.NextCursor)
	}

	tests := []struct {
		name     string
		walletID ids.WalletID
		cursor   string
		limit    int
		code     failure.Code
	}{
		{"limit zero", w.ID, "", 0, failure.MalformedRequest},
		{"limit above the maximum", w.ID, "", app.MaxLedgerPageSize + 1, failure.MalformedRequest},
		{"tampered cursor", w.ID, first.NextCursor + "x", 1, failure.InvalidCursor},
		{"cursor that is not base64", w.ID, "!!!", 1, failure.InvalidCursor},
		{"cursor that is not a number", w.ID, "YWJj", 1, failure.InvalidCursor},
		{"unknown wallet", parsed(t, ids.ParseWalletID, otherUUID), "", 1, failure.WalletNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.service.ListLedger(t.Context(), tt.walletID, tt.cursor, tt.limit)
			wantInvalidInput(t, err, tt.code)
		})
	}

	if _, err := f.service.ListLedger(t.Context(), w.ID, "", app.MaxLedgerPageSize); err != nil {
		t.Fatalf("limit at the maximum: %v", err)
	}
}

func TestTransactionVisibility(t *testing.T) {
	f := newFixture()
	w := f.open(t, 100000)
	bet := f.submit(t, operation(t, w, wager.Bet, 2500, "tx-1", ""))
	stored := f.store.TransactionStates()
	opening := stored[slices.IndexFunc(stored, func(s wager.State) bool { return s.Kind == wager.Opening })]

	owner := app.Caller{ProviderID: bet.External.ProviderID, Scopes: []string{app.ScopeWageringRead}}
	stranger := app.Caller{ProviderID: parsed(t, ids.ParseProviderID, "provider-b"), Scopes: []string{app.ScopeWageringRead}}
	internal := app.Caller{Scopes: []string{app.ScopeWageringRead}}

	tests := []struct {
		name    string
		caller  app.Caller
		id      ids.TransactionID
		visible bool
	}{
		{"owner sees its transaction", owner, bet.ID, true},
		{"another provider does not", stranger, bet.ID, false},
		{"internal service sees it", internal, bet.ID, true},
		{"provider does not see an opening", owner, opening.ID, false},
		{"internal service sees an opening", internal, opening.ID, true},
		{"unknown id", internal, parsed(t, ids.ParseTransactionID, otherUUID), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := f.service.GetTransaction(t.Context(), tt.caller, tt.id)
			if tt.visible && (err != nil || got.ID != tt.id) {
				t.Fatalf("GetTransaction = %+v, %v, want the transaction", got, err)
			}
			if !tt.visible {
				wantInvalidInput(t, err, failure.TransactionNotFound)
			}
		})
	}
}

func TestProviderTransactionVisibility(t *testing.T) {
	f := newFixture()
	w := f.open(t, 100000)
	bet := f.submit(t, operation(t, w, wager.Bet, 2500, "tx-1", ""))
	provider, externalID := bet.External.ProviderID, bet.External.ExternalID

	owner := app.Caller{ProviderID: provider}
	stranger := app.Caller{ProviderID: parsed(t, ids.ParseProviderID, "provider-b")}

	for name, caller := range map[string]app.Caller{"owner": owner, "internal service": {}} {
		got, err := f.service.GetProviderTransaction(t.Context(), caller, provider, externalID)
		if err != nil || got.ID != bet.ID {
			t.Fatalf("%s: GetProviderTransaction = %+v, %v, want the bet", name, got, err)
		}
	}

	f.store.Calls = nil
	_, err := f.service.GetProviderTransaction(t.Context(), stranger, provider, externalID)
	if !errors.Is(err, app.ErrForbidden) || len(f.store.Calls) != 0 {
		t.Fatalf("path of another provider: err = %v, calls = %v, want forbidden before any read", err, f.store.Calls)
	}

	_, err = f.service.GetProviderTransaction(t.Context(), owner, provider, parsed(t, ids.ParseExternalTransactionID, "tx-9"))
	wantInvalidInput(t, err, failure.TransactionNotFound)
}

func TestReconcileWallet(t *testing.T) {
	f := newFixture()
	w := f.open(t, 100000)
	f.submit(t, operation(t, w, wager.Bet, 2500, "tx-1", ""))
	f.submit(t, operation(t, w, wager.Loss, 0, "tx-2", ""))
	before := f.snapshot(t, w.ID)

	got, err := f.service.ReconcileWallet(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("ReconcileWallet: %v", err)
	}
	if got.WalletID != w.ID || !got.Consistent || got.CheckedEntries != 2 {
		t.Fatalf("reconciliation = %+v, want consistent over 2 entries", got)
	}
	if got.StoredBalance.Amount() != "975.00" || got.CalculatedBalance.Amount() != "975.00" || got.Difference.Amount() != "0.00" {
		t.Fatalf("reconciliation = %+v, want 975.00, 975.00 and 0.00", got)
	}
	if slices.Contains(f.metrics.Calls, "ReconciliationDivergence") || f.snapshot(t, w.ID) != before {
		t.Fatal("a consistent reconciliation counted a divergence or changed data")
	}

	_, err = f.service.ReconcileWallet(t.Context(), parsed(t, ids.ParseWalletID, otherUUID))
	wantInvalidInput(t, err, failure.WalletNotFound)
}

func TestReconcileWalletWithoutEntries(t *testing.T) {
	f := newFixture()
	w := f.open(t, 0)

	got, err := f.service.ReconcileWallet(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("ReconcileWallet: %v", err)
	}
	if !got.Consistent || got.CheckedEntries != 0 || got.StoredBalance.Amount() != "0.00" || got.CalculatedBalance.Amount() != "0.00" {
		t.Fatalf("reconciliation = %+v, want consistent zeros over no entries", got)
	}
}

func TestReconcileWalletReportsDivergence(t *testing.T) {
	f := newFixture()
	w := f.open(t, 100000)
	f.submit(t, operation(t, w, wager.Bet, 2500, "tx-1", ""))

	corrupted := f.wallet(t, w.ID)
	corrupted.Balance = amount(t, 90000, "BRL")
	tampered, err := wallet.Rehydrate(corrupted)
	if err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	if err := f.store.Wallets().UpdateBalance(t.Context(), tampered); err != nil {
		t.Fatalf("UpdateBalance: %v", err)
	}
	before := f.snapshot(t, w.ID)

	got, err := f.service.ReconcileWallet(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("ReconcileWallet: %v", err)
	}
	if got.Consistent || got.Difference.Amount() != "-75.00" || got.StoredBalance.Amount() != "900.00" || got.CalculatedBalance.Amount() != "975.00" {
		t.Fatalf("reconciliation = %+v, want 900.00 stored, 975.00 calculated and -75.00 of difference", got)
	}
	if !slices.Contains(f.metrics.Calls, "ReconciliationDivergence") {
		t.Fatalf("metrics = %v, want the divergence counted", f.metrics.Calls)
	}
	if !strings.Contains(f.logs.String(), "diverges") || !strings.Contains(f.logs.String(), w.ID.String()) {
		t.Fatal("the divergence was not logged with the wallet id")
	}
	if f.snapshot(t, w.ID) != before {
		t.Fatal("the reconciliation changed data")
	}
}
