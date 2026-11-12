package graphql

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"testing"

	httpx "canopy/server/internal/transport/http"
)

func TestMemberCredentialFieldsCannotUseAliasesOrFragments(t *testing.T) {
	h := NewHandler(NewResolver(nil, slog.New(slog.DiscardHandler)), DefaultLimits(), slog.New(slog.DiscardHandler))
	for _, query := range []string{
		`query { hidden: aiConnections { connections { id } } }`,
		`query { ...Access } fragment Access on Query { agentSettings { enabled } }`,
		`query { ... on Query { agentSettings { endpoint } } }`,
	} {
		data, _ := json.Marshal(map[string]string{"query": query})
		r := httptest.NewRequest("POST", "/api/graphql", bytes.NewReader(data))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(httpx.WithPrincipal(r.Context(), httpx.Principal{Role: "MEMBER"}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("member query status=%d body=%s", w.Code, w.Body.String())
		}
	}
}
