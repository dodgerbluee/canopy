package graphql

import (
	"bytes"
	httpx "canopy/server/internal/transport/http"
	"context"
	"encoding/json"
	"errors"
	"github.com/vektah/gqlparser/v2/ast"
	"io"
	"net/http"
	"net/url"
)

// AgentExecutor accepts only server-built fixed documents from the finite
// application facade. It is not mounted on HTTP or exposed as an agent tool.
type AgentExecutor struct{ Handler *Handler }

func NewAgentExecutor(root *Resolver) *AgentExecutor {
	return &AgentExecutor{NewHandler(root, DefaultLimits(), root.Log)}
}
func (e *AgentExecutor) Schema() *ast.Schema { return e.Handler.schema }
func (e *AgentExecutor) Execute(ctx context.Context, p httpx.Principal, query string, vars map[string]any) (map[string]any, error) {
	body, _ := json.Marshal(gqlRequest{Query: query, Variables: vars})
	req := &http.Request{Method: "POST", URL: &url.URL{Path: "/api/graphql"}, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(body))}
	req = req.WithContext(httpx.WithPrincipal(ctx, p))
	w := &agentResponse{header: http.Header{}}
	e.Handler.ServeHTTP(w, req)
	var out struct {
		Data   map[string]any             `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if json.Unmarshal(w.body.Bytes(), &out) != nil {
		return nil, errors.New("application response unreadable")
	}
	if len(out.Errors) > 0 {
		return nil, errors.New(out.Errors[0].Message)
	}
	return out.Data, nil
}

type agentResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (w *agentResponse) Header() http.Header         { return w.header }
func (w *agentResponse) Write(b []byte) (int, error) { return w.body.Write(b) }
func (w *agentResponse) WriteHeader(status int)      { w.status = status }
