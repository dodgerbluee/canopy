package graphql

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	"canopy/server/internal/transport/graphql/gql"
	"github.com/vektah/gqlparser/v2"
)

func todayClientDocument(t *testing.T) string {
	t.Helper()
	source, err := os.ReadFile("../../../../contracts/operations/overview.graphql")
	if err != nil {
		t.Fatal(err)
	}
	var definitions strings.Builder
	for _, name := range []string{"fragments", "care", "journal"} {
		contents, err := os.ReadFile("../../../../contracts/operations/" + name + ".graphql")
		if err != nil {
			t.Fatal(err)
		}
		definitions.Write(contents)
		definitions.WriteString("\n")
	}
	return string(source) + "\n" + fragmentClosure(t, string(source), definitions.String())
}

// The actual client operation includes union fragments omitted by the older
// reduced overview tests; validating it catches missing insight membership.
func TestTodayClientOperationMatchesExecutableSchema(t *testing.T) {
	schema := gql.NewExecutableSchema(gql.Config{Resolvers: NewResolver(nil, slog.New(slog.DiscardHandler))}).Schema()
	doc, problems := gqlparser.LoadQuery(schema, todayClientDocument(t))
	if len(problems) != 0 {
		t.Fatalf("Today client operation does not match server: %v", problems)
	}
	if err := Validate(schema, doc, DefaultLimits()); err != nil {
		t.Fatal(err)
	}
}

func TestTodayClientOperationThroughHTTP(t *testing.T) {
	e := newEnv(t, DefaultLimits())
	overviewDailyWater(t, e)
	response := e.gql(todayClientDocument(t), nil)
	if len(response.Errors) != 0 {
		t.Fatalf("Today query failed: %s", response.Raw)
	}
	overview := dataOf(t, response, "todayOverview")
	due := overview["dueTasks"].(map[string]any)["items"].([]any)
	if len(due) == 0 {
		t.Fatal("expected real seeded care occurrences")
	}
}
