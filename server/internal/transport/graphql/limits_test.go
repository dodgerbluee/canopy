package graphql

import (
	"log/slog"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"

	"canopy/server/internal/transport/graphql/gql"
)

// TestFrozenOperationsFitDefaultLimits guards the interaction between the
// accepted contract and the shipped budget: every frozen client operation
// must pass Validate under DefaultLimits, charged at the SDL default page
// size (variables are invisible to the validator). T10 found Plants at
// 5055 vs a 5000 budget; T11 found Purchases at 446,680 (25×100×25
// fan-out) — this test is the tripwire for that class of bug.
// fragmentClosure returns the fragment definitions from fragments.graphql
// that the operation file transitively spreads. gqlparser rejects unused
// fragments in a loaded document, so the closure must be exact.
func fragmentClosure(t *testing.T, opsSrc, fragsSrc string) string {
	t.Helper()
	defs := map[string]string{}
	rx := regexp.MustCompile(`(?ms)^fragment (\w+) on \w+ \{.*?\n\}`)
	for _, m := range rx.FindAllStringSubmatch(fragsSrc, -1) {
		defs[m[1]] = m[0]
	}
	needed := map[string]bool{}
	queue := []string{}
	for _, m := range regexp.MustCompile(`\.\.\.(\w+)`).FindAllStringSubmatch(opsSrc, -1) {
		if _, ok := defs[m[1]]; ok && !needed[m[1]] {
			needed[m[1]] = true
			queue = append(queue, m[1])
		}
	}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		for _, m := range regexp.MustCompile(`\.\.\.(\w+)`).FindAllStringSubmatch(defs[name], -1) {
			if _, ok := defs[m[1]]; ok && !needed[m[1]] {
				needed[m[1]] = true
				queue = append(queue, m[1])
			}
		}
	}
	var out strings.Builder
	for _, m := range regexp.MustCompile(`(?ms)^fragment (\w+) on \w+ \{.*?\n\}`).FindAllStringSubmatch(fragsSrc, -1) {
		if needed[m[1]] {
			out.WriteString(m[0] + "\n")
		}
	}
	return out.String()
}

func TestFrozenOperationsFitDefaultLimits(t *testing.T) {
	es := gql.NewExecutableSchema(gql.Config{Resolvers: NewResolver(nil, slog.New(slog.DiscardHandler))})
	schema := es.Schema()
	for _, file := range []string{"collection", "session-portability", "purchases"} {
		ops, err := os.ReadFile("../../../../contracts/operations/" + file + ".graphql")
		if err != nil {
			t.Fatalf("read %s operations: %v", file, err)
		}
		src := string(ops)
		if file == "collection" || file == "purchases" { // these operations spread the fragments
			frag, err := os.ReadFile("../../../../contracts/operations/fragments.graphql")
			if err != nil {
				t.Fatalf("read fragments: %v", err)
			}
			src += "\n" + fragmentClosure(t, src, string(frag))
		}
		doc, gerrs := gqlparser.LoadQuery(schema, src)
		if len(gerrs) > 0 {
			t.Fatalf("load %s: %v", file, gerrs)
		}
		if err := Validate(schema, doc, DefaultLimits()); err != nil {
			t.Errorf("%s: frozen operations exceed default limits: %v", file, err)
		}
	}
}

func TestLimits(t *testing.T) {
	es := gql.NewExecutableSchema(gql.Config{Resolvers: NewResolver(nil, slog.New(slog.DiscardHandler))})
	schema := es.Schema()
	lim := DefaultLimits()

	cases := []struct {
		name    string
		query   string
		limits  Limits
		wantErr string // "" = must pass
	}{
		{
			name:  "session query is cheap",
			query: `query Session { session { authenticated workspace { id displayName } } }`,
		},
		{
			name:    "depth over limit rejected",
			query:   "{ locations " + strings.Repeat("{ parent ", lim.MaxDepth) + "{ name }" + strings.Repeat(" }", lim.MaxDepth) + " }",
			wantErr: "levels deep",
		},
		{
			name:    "alias flood rejected",
			query:   aliasQuery(lim.MaxAliases + 1),
			wantErr: "aliases",
		},
		{
			name:  "aliases at the limit pass",
			query: aliasQuery(lim.MaxAliases),
		},
		{
			name:    "page size over cap rejected",
			query:   `{ plants(first: 101) { items { id } pageInfo { hasNextPage } } }`,
			wantErr: "out of range",
		},
		{
			name:  "page size at the cap passes the range check",
			query: `{ plants(first: 100) { items { id } pageInfo { hasNextPage } } }`,
			limits: func() Limits {
				l := DefaultLimits()
				l.MaxComplexity = 100000 // isolate the range check from the budget
				return l
			}(),
		},
		{
			name:    "complexity charged at requested page size",
			query:   fatPage(100),
			limits:  tight(),
			wantErr: "complexity",
		},
		{
			name:   "same shape at first:1 fits the same budget",
			query:  fatPage(1),
			limits: tight(),
		},
		{
			name:   "default page size is charged (no first given)",
			query:  `{ plants { items { id name scientificName cultivar rootstock type count category } pageInfo { hasNextPage } } }`,
			limits: tight(),
			// 25 rows charged by default -> over the tight budget.
			wantErr: "complexity",
		},
		{
			name:    "negative first rejected",
			query:   `{ plants(first: -1) { items { id } } }`,
			wantErr: "out of range",
		},
		{
			name:  "fragment cost counted once per use",
			query: `query P { plants(first: 2) { items { ...f } } } fragment f on Plant { id name }`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			limits := tc.limits
			if (limits == Limits{}) {
				limits = lim
			}
			doc, gerrs := gqlparser.LoadQuery(schema, tc.query)
			if gerrs != nil {
				t.Fatalf("schema validation: %v", gerrs)
			}
			err := Validate(schema, doc, limits)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected rejection: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want rejection containing %q, got pass", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want rejection containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func aliasQuery(n int) string {
	var b strings.Builder
	b.WriteString("{")
	for i := 0; i < n; i++ {
		b.WriteString(" a")
		b.WriteString(itoa(i))
		b.WriteString(": session { authenticated }")
	}
	b.WriteString(" }")
	return b.String()
}

func itoa(n int) string {
	// tiny helper to avoid strconv noise in the table
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

func fatPage(first int) string {
	return "{ plants(first: " + itoa(first) + ") { items { id name scientificName cultivar rootstock type count category } pageInfo { hasNextPage endCursor } } }"
}

func tight() Limits {
	l := DefaultLimits()
	l.MaxComplexity = 150
	return l
}
