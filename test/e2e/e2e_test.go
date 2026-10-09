//go:build e2e

package e2e_test

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestSameBetFiftyTimesAcrossInstances(t *testing.T) {
	s := environment(t)
	w := s.open(t, "1000.00")
	externalID := s.externalID("fifty")

	answers := inParallel(50, func(i int) answer { return s.submit(i, w, "BET", "25.00", externalID, "") })

	fresh := 0
	for i, got := range answers {
		if got.err != nil || got.status != http.StatusOK || got.text("transactionId") != answers[0].text("transactionId") || got.amount("balance") != "975.00" {
			t.Fatalf("send %d = %d %s (%v), want the same transaction with 975.00", i+1, got.status, got.raw, got.err)
		}
		if got.body["idempotentReplay"] == false {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatalf("%d sends were applied, want exactly one", fresh)
	}
	s.wantWallet(t, w, "975.00", 2)
}

func TestTwoBetsOfEightyAcrossInstances(t *testing.T) {
	s := environment(t)
	w := s.open(t, "100.00")
	ids := []string{s.externalID("eighty"), s.externalID("eighty")}

	check := func(round string, answers []answer) {
		statuses := map[string]int{}
		for _, got := range answers {
			if got.err != nil {
				t.Fatalf("%s: %v", round, got.err)
			}
			statuses[got.text("status")]++
			if got.text("status") == "REJECTED" && (got.status != http.StatusUnprocessableEntity || got.text("failureCode") != "INSUFFICIENT_FUNDS") {
				t.Fatalf("%s: rejection = %d %s, want 422 INSUFFICIENT_FUNDS", round, got.status, got.raw)
			}
		}
		if statuses["PROCESSED"] != 1 || statuses["REJECTED"] != 1 {
			t.Fatalf("%s: statuses = %v, want one PROCESSED and one REJECTED", round, statuses)
		}
		s.wantWallet(t, w, "20.00", 2)
	}

	first := inParallel(2, func(i int) answer { return s.submit(i, w, "BET", "80.00", ids[i], "") })
	check("dispute", first)
	resent := inParallel(2, func(i int) answer { return s.submit(i+1, w, "BET", "80.00", ids[i], "") })
	check("resend", resent)
	for i := range resent {
		if resent[i].body["idempotentReplay"] != true || resent[i].text("transactionId") != first[i].text("transactionId") {
			t.Fatalf("resend %d = %s, want the original result as a replay", i+1, resent[i].raw)
		}
	}
}

func TestManyWalletsAtTheSameTime(t *testing.T) {
	s := environment(t)
	wallets := make([]wallet, 30)
	for i := range wallets {
		wallets[i] = s.open(t, "100.00")
	}

	started := time.Now()
	answers := inParallel(len(wallets)*3, func(i int) answer {
		return s.submit(i, wallets[i/3], "BET", "10.00", s.externalID("many"), "")
	})
	for i, got := range answers {
		if got.err != nil || got.text("status") != "PROCESSED" {
			t.Fatalf("operation %d = %d %s (%v), want PROCESSED", i+1, got.status, got.raw, got.err)
		}
	}
	for _, w := range wallets {
		s.wantWallet(t, w, "70.00", 4)
	}
	t.Logf("90 operations over 30 wallets through 3 instances in %s", time.Since(started).Round(time.Millisecond))
}

func TestSameOperationByHTTPAndBySQS(t *testing.T) {
	s := environment(t)

	t.Run("in sequence", func(t *testing.T) {
		w := s.open(t, "1000.00")
		externalID := s.externalID("cross")
		if bet := s.submit(0, w, "BET", "25.00", externalID, ""); bet.text("status") != "PROCESSED" {
			t.Fatalf("bet by HTTP = %d %s", bet.status, bet.raw)
		}
		s.sendMessage(t, w, "msg-"+externalID, "BET", "25.00", externalID, "")
		s.waitHandled(t, "msg-"+externalID, 30*time.Second)
		s.wantWallet(t, w, "975.00", 2)
	})

	t.Run("at the same time", func(t *testing.T) {
		w := s.open(t, "1000.00")
		externalID := s.externalID("cross")
		s.sendMessage(t, w, "msg-"+externalID, "BET", "25.00", externalID, "")
		if bet := s.submit(1, w, "BET", "25.00", externalID, ""); bet.err != nil || bet.text("status") != "PROCESSED" {
			t.Fatalf("bet by HTTP = %d %s (%v)", bet.status, bet.raw, bet.err)
		}
		s.waitHandled(t, "msg-"+externalID, 30*time.Second)
		s.wantWallet(t, w, "975.00", 2)
	})

	t.Run("eighty by each channel", func(t *testing.T) {
		w := s.open(t, "100.00")
		byQueue, byHTTP := s.externalID("queue"), s.externalID("http")
		s.sendMessage(t, w, "msg-"+byQueue, "BET", "80.00", byQueue, "")
		if got := s.submit(2, w, "BET", "80.00", byHTTP, ""); got.err != nil {
			t.Fatalf("bet by HTTP: %v", got.err)
		}

		deadline := time.Now().Add(20 * time.Second)
		statuses := map[string]int{}
		for time.Now().Before(deadline) {
			statuses = map[string]int{}
			for _, id := range []string{byQueue, byHTTP} {
				statuses[s.transaction(t, id).text("status")]++
			}
			if statuses["PROCESSED"]+statuses["REJECTED"] == 2 {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if statuses["PROCESSED"] != 1 || statuses["REJECTED"] != 1 {
			t.Fatalf("statuses = %v, want one PROCESSED and one REJECTED across the two channels", statuses)
		}
		s.wantWallet(t, w, "20.00", 2)
	})
}

func TestReversalsBeforeTheirReference(t *testing.T) {
	s := environment(t)

	t.Run("resolved when the reference arrives", func(t *testing.T) {
		w := s.open(t, "1000.00")
		bet, refund := s.externalID("bet"), s.externalID("refund")

		pending := s.submit(0, w, "REFUND", "25.00", refund, bet)
		if pending.status != http.StatusAccepted || pending.text("status") != "PENDING_REFERENCE" {
			t.Fatalf("refund before its bet = %d %s, want 202 PENDING_REFERENCE", pending.status, pending.raw)
		}
		if got := s.submit(1, w, "BET", "25.00", bet, ""); got.text("status") != "PROCESSED" {
			t.Fatalf("bet = %d %s", got.status, got.raw)
		}
		resolved := s.waitStatus(t, refund, "PROCESSED", 40*time.Second)
		if resolved.amount("balance") != "1000.00" {
			t.Fatalf("resolved refund = %s, want the balance back to 1000.00", resolved.raw)
		}
		s.wantWallet(t, w, "1000.00", 3)
		if events := s.count(t, `SELECT count(*) FROM outbox WHERE group_key = $1 AND event_type = 'WagerTransactionPendingReference'`, w.id); events != 1 {
			t.Fatalf("pending reference events = %d, want 1", events)
		}
	})

	t.Run("rejected when the reference never arrives", func(t *testing.T) {
		w := s.open(t, "1000.00")
		rollback := s.externalID("rollback")

		pending := s.submit(2, w, "ROLLBACK", "25.00", rollback, s.externalID("never"))
		if pending.text("status") != "PENDING_REFERENCE" {
			t.Fatalf("rollback before its reference = %d %s", pending.status, pending.raw)
		}
		expired := s.waitStatus(t, rollback, "REJECTED", 90*time.Second)
		if expired.text("failureCode") != "REFERENCE_NOT_FOUND" {
			t.Fatalf("expired rollback = %s, want REFERENCE_NOT_FOUND (run the stack with a short PENDING_REFERENCE_TTL: make e2e)", expired.raw)
		}
		s.wantWallet(t, w, "1000.00", 1)
		if events := s.count(t, `SELECT count(*) FROM outbox WHERE group_key = $1 AND event_type = 'WagerTransactionRejected'`, w.id); events != 1 {
			t.Fatalf("rejection events = %d, want 1", events)
		}
	})
}

func TestInstanceKilledDuringLoad(t *testing.T) {
	s := environment(t)
	s.drainEvents(t)
	wallets := make([]wallet, 6)
	for i := range wallets {
		wallets[i] = s.open(t, "1000.00")
	}

	const perWallet = 20
	ids := make([][]string, len(wallets))
	for i := range ids {
		for range perWallet {
			ids[i] = append(ids[i], s.externalID("kill"))
		}
	}
	send := func(i int) answer {
		return s.submit(i, wallets[i%len(wallets)], "BET", "1.00", ids[i%len(wallets)][i/len(wallets)], "")
	}

	killed := make(chan error, 1)
	go func() {
		time.Sleep(150 * time.Millisecond)
		killed <- composeErr("kill", "wallet-2")
	}()
	inParallel(len(wallets)*perWallet, send)
	if err := <-killed; err != nil {
		t.Fatalf("kill wallet-2: %v", err)
	}
	compose(t, "up", "-d", "--wait", "wallet-2")

	for attempt := 1; attempt <= 3; attempt++ {
		failed := 0
		for _, got := range inParallel(len(wallets)*perWallet, send) {
			if got.err != nil || got.text("status") != "PROCESSED" {
				failed++
			}
		}
		if failed == 0 {
			break
		}
		if attempt == 3 {
			t.Fatalf("%d operations still failing after the instance came back", failed)
		}
		time.Sleep(2 * time.Second)
	}

	for _, w := range wallets {
		s.wantWallet(t, w, "980.00", perWallet+1)
		s.waitPublished(t, w, 90*time.Second)
	}

	delivered := s.drainEvents(t)
	rows, err := s.db.Query(context.Background(), `SELECT event_id::text FROM outbox WHERE group_key = ANY($1)`, walletIDs(wallets))
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	defer rows.Close()
	recorded := 0
	for rows.Next() {
		var eventID string
		if err := rows.Scan(&eventID); err != nil {
			t.Fatalf("scan: %v", err)
		}
		recorded++
		if delivered[eventID] == 0 {
			t.Fatalf("event %s was committed to the outbox and never reached the events queue", eventID)
		}
	}
	if want := len(wallets) * (2 + 2*perWallet); recorded != want {
		t.Fatalf("outbox has %d events for these wallets, want %d", recorded, want)
	}
}

func walletIDs(wallets []wallet) []string {
	ids := make([]string, len(wallets))
	for i, w := range wallets {
		ids[i] = w.id
	}
	return ids
}

func TestRestartKeepsIdempotencyAndPendingReferences(t *testing.T) {
	s := environment(t)
	w := s.open(t, "1000.00")
	bet, refund, lateBet := s.externalID("bet"), s.externalID("refund"), s.externalID("late-bet")
	original := s.submit(0, w, "BET", "25.00", bet, "")
	pending := s.submit(1, w, "REFUND", "40.00", refund, lateBet)
	if original.text("status") != "PROCESSED" || pending.text("status") != "PENDING_REFERENCE" {
		t.Fatalf("before the restart: bet %s, refund %s", original.raw, pending.raw)
	}

	compose(t, "restart", "wallet-1", "wallet-2", "wallet-3")
	for instance := range instances {
		s.waitReady(t, instance, http.StatusOK, 90*time.Second)
	}

	for instance := range instances {
		replay := s.submit(instance, w, "BET", "25.00", bet, "")
		if replay.status != http.StatusOK || replay.body["idempotentReplay"] != true || replay.text("transactionId") != original.text("transactionId") || replay.amount("balance") != "975.00" {
			t.Fatalf("replay on instance %d after the restart = %d %s, want the original result", instance+1, replay.status, replay.raw)
		}
	}
	if again := s.submit(2, w, "REFUND", "40.00", refund, lateBet); again.status != http.StatusAccepted || again.body["idempotentReplay"] != true {
		t.Fatalf("replay of the pending refund = %d %s, want 202 as a replay", again.status, again.raw)
	}

	if got := s.submit(0, w, "BET", "40.00", lateBet, ""); got.text("status") != "PROCESSED" {
		t.Fatalf("late bet = %d %s", got.status, got.raw)
	}
	s.waitStatus(t, refund, "PROCESSED", 40*time.Second)
	s.wantWallet(t, w, "975.00", 4)
}

func TestDependenciesGoDownAndComeBack(t *testing.T) {
	s := environment(t)
	w := s.open(t, "1000.00")

	t.Run("postgres", func(t *testing.T) {
		compose(t, "stop", "postgres")
		defer func() {
			compose(t, "start", "postgres")
			for instance := range instances {
				s.waitReady(t, instance, http.StatusOK, 90*time.Second)
			}
		}()
		s.waitReady(t, 0, http.StatusServiceUnavailable, 30*time.Second)

		externalID := s.externalID("down")
		refused := s.submit(0, w, "BET", "25.00", externalID, "")
		if refused.status != http.StatusServiceUnavailable || refused.text("code") != "SERVICE_UNAVAILABLE" {
			t.Fatalf("bet with the database down = %d %s (%v), want 503", refused.status, refused.raw, refused.err)
		}
		if live, err := s.client.Get(instances[0] + "/health/live"); err != nil || live.StatusCode != http.StatusOK {
			t.Fatalf("liveness with the database down = %v, %v, want 200", live, err)
		}

		compose(t, "start", "postgres")
		s.waitReady(t, 0, http.StatusOK, 90*time.Second)
		if again := s.submit(0, w, "BET", "25.00", externalID, ""); again.text("status") != "PROCESSED" || again.body["idempotentReplay"] != false {
			t.Fatalf("resend after the database came back = %d %s, want it processed for the first time", again.status, again.raw)
		}
		s.wantWallet(t, w, "975.00", 2)
	})

	t.Run("broker", func(t *testing.T) {
		compose(t, "stop", "ministack")
		defer func() {
			compose(t, "start", "ministack")
			compose(t, "run", "--rm", "queues")
		}()
		s.waitReady(t, 1, http.StatusServiceUnavailable, 30*time.Second)

		bet := s.submit(1, w, "BET", "25.00", s.externalID("no-broker"), "")
		if bet.text("status") != "PROCESSED" {
			t.Fatalf("bet with the broker down = %d %s (%v), want PROCESSED: operations do not depend on the broker", bet.status, bet.raw, bet.err)
		}
		if pending := s.count(t, `SELECT count(*) FROM outbox WHERE group_key = $1 AND published_at IS NULL`, w.id); pending == 0 {
			t.Fatal("no event is pending while the broker is down")
		}

		compose(t, "start", "ministack")
		compose(t, "run", "--rm", "queues")
		for instance := range instances {
			s.waitReady(t, instance, http.StatusOK, 90*time.Second)
		}
		s.waitPublished(t, w, 120*time.Second)
		s.wantWallet(t, w, "950.00", 3)
	})
}
