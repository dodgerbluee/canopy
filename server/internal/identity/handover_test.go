package identity

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestRegistrationHandoverPreservesGardenAndConsumesOnce(t *testing.T) {
	users, admin := accountFixture(t)
	ctx := context.Background()
	owner := parseTestID(admin.ID)
	before, err := users.Sessions.NewForUser(ctx, owner, owner)
	if err != nil {
		t.Fatal(err)
	}
	original, err := users.Pool.Query(ctx, "SELECT id,display_name,timezone,default_currency FROM workspace WHERE owner_id=$1", owner)
	if err != nil {
		t.Fatal(err)
	}
	var wsID, name, tz, currency string
	original.Next()
	if err = original.Scan(&wsID, &name, &tz, &currency); err != nil {
		t.Fatal(err)
	}
	original.Close()
	token, err := users.PrepareHandover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = users.Sessions.Verify(ctx, before.Token); !errors.Is(err, ErrSessionExpired) {
		t.Fatal("old session survived preparation")
	}
	if ok, err := users.HandoverAvailable(ctx, "wrong-token"); err != nil || ok {
		t.Fatal("unguarded handover")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			u, ws, e := users.RegisterExisting(ctx, token, SetupInput{Username: "new-owner", Password: "new-owner-password", WorkspaceName: "Do not overwrite", Timezone: "UTC", Currency: "EUR"})
			if e == nil && (u.ID != admin.ID || uuidString(ws.ID) != wsID || ws.DisplayName != name || ws.Timezone != tz || ws.DefaultCurrency != currency) {
				e = errors.New("garden identity or settings changed")
			}
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	success := 0
	for e := range errs {
		if e == nil {
			success++
		} else if !errors.Is(e, ErrHandover) {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatalf("successful registrations=%d", success)
	}
	if _, _, err = users.Login(ctx, "new-owner", "new-owner-password"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = users.Login(ctx, "gardener", "garden-passphrase"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatal("old credentials survived")
	}
	if ok, err := users.HandoverAvailable(ctx, token); err != nil || ok {
		t.Fatal("used link still available")
	}
}
func TestRegistrationHandoverExpiryAndRecovery(t *testing.T) {
	users, admin := accountFixture(t)
	ctx := context.Background()
	token, err := users.PrepareHandover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = users.Pool.Exec(ctx, "UPDATE registration_handover SET expires_at=now()-interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = users.RegisterExisting(ctx, token, SetupInput{Username: "new-owner", Password: "new-owner-password"}); !errors.Is(err, ErrHandover) {
		t.Fatal("expired handover accepted")
	}
	token, err = users.PrepareHandover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	svc, _ := newOwnerService(NewQueries(users.Pool), newFakeClock())
	if _, err = svc.Recover(ctx, "operator-recovery-password"); err != nil {
		t.Fatal(err)
	}
	if ok, err := users.HandoverAvailable(ctx, token); err != nil || ok {
		t.Fatal("recovery left handover active")
	}
	if _, _, err = users.Login(ctx, admin.Username, "operator-recovery-password"); err != nil {
		t.Fatal(err)
	}
}
