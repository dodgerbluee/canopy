package agentapi

import (
	"canopy/server/internal/agentapi/catalog"
	gqlx "canopy/server/internal/transport/graphql"
	"github.com/vektah/gqlparser/v2"
	"log/slog"
	"strings"
	"testing"
)

func TestEveryNamedOperationValidatesAgainstApplicationSchema(t *testing.T) {
	e := gqlx.NewAgentExecutor(&gqlx.Resolver{Log: slog.New(slog.DiscardHandler)})
	for _, tool := range catalog.Tools() {
		if strings.HasPrefix(tool.Root, "@") {
			continue
		}
		t.Run(tool.Name, func(t *testing.T) {
			doc, err := document(e.Schema(), tool)
			if err != nil {
				t.Fatal(err)
			}
			if _, errors := gqlparser.LoadQuery(e.Schema(), doc); errors != nil {
				t.Fatal(errors.Error(), doc)
			}
			schema := inputSchema(e.Schema(), tool)
			if schema["additionalProperties"] != false {
				t.Fatal("unknown tool inputs accepted")
			}
		})
	}
}
