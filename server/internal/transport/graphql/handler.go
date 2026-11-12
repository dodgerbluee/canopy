package graphql

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"canopy/server/internal/transport/graphql/gql"
	httpx "canopy/server/internal/transport/http"
)

// Handler is the POST /api/graphql endpoint. Auth (cookie + CSRF + Origin)
// is enforced upstream by httpx.RequirePrincipal; this handler owns body
// size, content type, query validation and the cost limits in limits.go.
type Handler struct {
	srv    http.Handler
	schema *ast.Schema
	limits Limits
	log    *slog.Logger

	// Maintenance, when set and true, refuses mutations with 503 while
	// the stack is quiesced for a backup (T38.a). Reads stay open.
	Maintenance func(ctx context.Context) bool
}

func NewHandler(root gql.ResolverRoot, lim Limits, log *slog.Logger) *Handler {
	es := gql.NewExecutableSchema(gql.Config{Resolvers: root})
	srv := handler.New(es)
	// handler.New ships without transports in this gqlgen version; the only
	// transport we accept is JSON-over-POST (GET is not routed by the mux).
	srv.AddTransport(transport.POST{})
	return &Handler{srv: srv, schema: es.Schema(), limits: lim, log: log}
}

type gqlRequest struct {
	Query         string         `json:"query"`
	OperationName string         `json:"operationName"`
	Variables     map[string]any `json:"variables"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	ct, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";")
	if strings.TrimSpace(ct) != "application/json" {
		h.writeErrors(w, http.StatusUnsupportedMediaType,
			gqlerror.Errorf("Content-Type must be application/json."))
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, h.limits.MaxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.writeErrors(w, http.StatusRequestEntityTooLarge,
			gqlerror.Errorf("Request body exceeds %d bytes.", h.limits.MaxBodyBytes))
		return
	}

	var req gqlRequest
	if err := json.Unmarshal(body, &req); err != nil || strings.TrimSpace(req.Query) == "" {
		h.writeErrors(w, http.StatusBadRequest,
			gqlerror.Errorf("Request body must be a JSON object with a non-empty query."))
		return
	}

	doc, gerrs := gqlparser.LoadQuery(h.schema, req.Query)
	if gerrs != nil {
		// Schema-level rejection (syntax, unknown fields, bad fragments):
		// GraphQL-shaped errors, 200 per the wire spec.
		for i := range gerrs {
			setCode(gerrs[i], "VALIDATION")
		}
		h.writeErrors(w, http.StatusOK, gerrs...)
		return
	}
	if vErr := Validate(h.schema, doc, h.limits); vErr != nil {
		var msg string
		if lv, ok := vErr.(*LimitsViolation); ok {
			msg = lv.Msg
		} else {
			msg = vErr.Error()
		}
		e := gqlerror.Errorf("%s", msg)
		setCode(e, "CAPABILITY_UNAVAILABLE")
		h.writeErrors(w, http.StatusOK, e)
		return
	}

	// Parse field names so aliases/fragments cannot bypass credential permissions.
	if p, ok := httpx.PrincipalFrom(r.Context()); ok && p.Role == "MEMBER" {
		for _, op := range doc.Operations {
			if req.OperationName == "" || op.Name == req.OperationName {
				if containsAdminField(op.SelectionSet) {
					e := gqlerror.Errorf("Administrator access required.")
					setCode(e, "FORBIDDEN")
					h.writeErrors(w, http.StatusForbidden, e)
					return
				}
			}
		}
	}
	// quiesce gate: mutations and uploads are refused while a backup
	// runs; queries keep working so status pages and reads survive
	if h.Maintenance != nil && h.Maintenance(r.Context()) {
		for _, op := range doc.Operations {
			if op.Operation == ast.Mutation {
				e := gqlerror.Errorf("The system is in maintenance mode; writes are paused.")
				setCode(e, "CAPABILITY_UNAVAILABLE")
				h.writeErrors(w, http.StatusServiceUnavailable, e)
				return
			}
		}
	}

	// Garden answers may legitimately outlast the API's 30-second write
	// default. Extend only this authenticated, bounded operation; prevent
	// aliases/fragments from multiplying synchronous model calls per request.
	questions := 0
	connectionTests := 0
	var countQuestions func(ast.SelectionSet)
	countQuestions = func(set ast.SelectionSet) {
		for _, selection := range set {
			switch node := selection.(type) {
			case *ast.Field:
				if node.Name == "testAIConnection" {
					connectionTests++
				}
				if node.Name == "askGarden" {
					questions++
				}
				countQuestions(node.SelectionSet)
			case *ast.InlineFragment:
				countQuestions(node.SelectionSet)
			case *ast.FragmentSpread:
				if node.Definition != nil {
					countQuestions(node.Definition.SelectionSet)
				}
			}
		}
	}
	for _, op := range doc.Operations {
		if req.OperationName == "" || op.Name == req.OperationName {
			countQuestions(op.SelectionSet)
		}
	}
	if connectionTests > 1 || (connectionTests > 0 && questions > 0) {
		e := gqlerror.Errorf("Run one AI connection test per request, separately from garden questions.")
		setCode(e, "VALIDATION")
		h.writeErrors(w, http.StatusOK, e)
		return
	}
	if questions > 1 {
		e := gqlerror.Errorf("Ask one garden question per request.")
		setCode(e, "VALIDATION")
		h.writeErrors(w, http.StatusOK, e)
		return
	}
	if questions == 1 {
		if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(160 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			h.writeErrors(w, http.StatusOK, gqlerror.Errorf("Could not prepare the garden answer response."))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 155*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	h.srv.ServeHTTP(w, r)
}

func setCode(e *gqlerror.Error, code string) {
	if e.Extensions == nil {
		e.Extensions = map[string]any{}
	}
	if _, exists := e.Extensions["code"]; !exists {
		e.Extensions["code"] = code
	}
}

func (h *Handler) writeErrors(w http.ResponseWriter, status int, errs ...*gqlerror.Error) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"errors": errs})
}

func containsAdminField(set ast.SelectionSet) bool {
	for _, selection := range set {
		switch n := selection.(type) {
		case *ast.Field:
			switch n.Name {
			case "aiConnections", "saveAIConnection", "saveAIFeatureBindings", "deleteAIConnection", "testAIConnection", "agentSettings", "saveAgentGrant", "rotateAgentCredential", "revokeAgentGrant", "decideAgentAction":
				return true
			}
			if containsAdminField(n.SelectionSet) {
				return true
			}
		case *ast.InlineFragment:
			if containsAdminField(n.SelectionSet) {
				return true
			}
		case *ast.FragmentSpread:
			if n.Definition != nil && containsAdminField(n.Definition.SelectionSet) {
				return true
			}
		}
	}
	return false
}
