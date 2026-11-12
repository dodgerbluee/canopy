package platform

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// Problem mirrors the frozen MutationProblem contract shape
// (contracts/graphql/shared.graphql) for transport-level errors.
type Problem struct {
	Code            string `json:"code"`
	Field           string `json:"field,omitempty"`
	Message         string `json:"message"`
	CurrentRevision *int64 `json:"currentRevision,omitempty"`
}

type problemEnvelope struct {
	Problems []Problem `json:"problems"`
}

// WriteProblem emits a safe, structured error: internal details go to the
// log, never to the client.
func WriteProblem(w http.ResponseWriter, r *http.Request, log *slog.Logger, status int, code, message string) {
	if code == "INTERNAL" {
		log.Error("internal error", "path", r.URL.Path, "status", status)
		message = "Internal server error"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problemEnvelope{
		Problems: []Problem{{Code: code, Message: message}},
	})
}
