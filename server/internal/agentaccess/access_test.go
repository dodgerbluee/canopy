package agentaccess

import (
	"strings"
	"testing"
	"time"
)

func TestGrantScopeAndRateBoundaries(t *testing.T) {
	g := Grant{ID: "fixture", Scopes: []string{"logs:write"}, PlantIDs: []string{"one"}, ExpiresAt: time.Now().Add(time.Hour)}
	if !g.Has("logs:write") || g.Has("plants:write") || !g.Plant("one") || g.Plant("two") {
		t.Fatal("scope boundary")
	}
	now := time.Now()
	m := &Manager{Now: func() time.Time { return now }, rates: map[string][]time.Time{}}
	for range 10 {
		if !m.Limit(Principal{Grant: g}, true) {
			t.Fatal("early rate limit")
		}
	}
	if m.Limit(Principal{Grant: g}, true) {
		t.Fatal("write limit bypass")
	}
	now = now.Add(time.Minute + time.Second)
	if !m.Limit(Principal{Grant: g}, true) {
		t.Fatal("window did not expire")
	}
	if strings.Contains(ErrDenied.Error(), "token") {
		t.Fatal("credential details")
	}
}
