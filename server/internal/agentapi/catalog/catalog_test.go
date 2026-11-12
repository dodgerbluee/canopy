package catalog

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"testing"
)

func TestPinnedSDKAndFiniteCatalog(t *testing.T) {
	seen := map[string]bool{}
	for _, tool := range Tools() {
		if seen[tool.Name] || tool.Scope == "" || tool.Root == "" {
			t.Fatal("invalid catalog")
		}
		seen[tool.Name] = true
	}
	if !seen["photo_attach"] || seen["execute_graphql"] {
		t.Fatal("catalog boundary")
	}
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "canopy-spike", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "ping", Description: "SDK compatibility fixture"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, map[string]string, error) {
		return nil, map[string]string{"status": "ok"}, nil
	})
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "fixture", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "ping"})
	if err != nil || r.IsError {
		t.Fatal("SDK call", err)
	}
}
