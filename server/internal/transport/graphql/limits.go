package graphql

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

// Limits bound what one request may ask the server to do. They are
// enforced before execution so oversized queries never reach the database.
type Limits struct {
	MaxBodyBytes  int64
	MaxDepth      int
	MaxAliases    int
	MaxPageSize   int
	MaxComplexity int
}

func DefaultLimits() Limits {
	return Limits{
		MaxBodyBytes: 64 << 10,
		MaxDepth:     12,
		MaxAliases:   30,
		MaxPageSize:  100,
		// 500_000: the budget must accommodate the frozen contract. The
		// accepted Purchases operation (contracts/operations/purchases.graphql)
		// is the worst case: purchases(25 default) × lines(first:100) ×
		// allocations(25 default) × receipt assets measures 446,680 under
		// the page-size charge model. That shape is also the server's real
		// worst case: commands cap lines at 100/purchase and allocations at
		// 25/line, so charged cost equals executable work. 500_000 keeps
		// ~12% headroom; depth/alias/page caps still bind every other shape.
		MaxComplexity: 500_000,
	}
}

// defaultPageSize mirrors the SDL default (first: Int = 25) so cost is
// charged even when the client relies on the default.
const defaultPageSize = 25

type LimitsViolation struct{ Msg string }

func (v *LimitsViolation) Error() string { return v.Msg }

// Validate enforces depth, alias-count, page-size and complexity limits.
// Complexity is charged against the REQUESTED page size: a connection
// field costs 1 plus its subtree multiplied by the number of rows it may
// return (first, or the SDL default). doc must already be schema-validated
// (gqlparser.LoadQuery), so field/fragment references resolve.
func Validate(schema *ast.Schema, doc *ast.QueryDocument, lim Limits) error {
	v := &walker{schema: schema, doc: doc, lim: lim}
	for _, op := range doc.Operations {
		var root *ast.Definition
		switch op.Operation {
		case ast.Query:
			root = schema.Query
		case ast.Mutation:
			root = schema.Mutation
		case ast.Subscription:
			root = schema.Subscription
		}
		if root == nil {
			return &LimitsViolation{Msg: "Operation is not supported by this schema."}
		}
		cost, err := v.walkSet(op.SelectionSet, root, 1, 0)
		if err != nil {
			return err
		}
		if cost > v.lim.MaxComplexity {
			return &LimitsViolation{Msg: fmt.Sprintf(
				"Query complexity %d exceeds the limit of %d.", cost, v.lim.MaxComplexity)}
		}
	}
	if v.aliases > v.lim.MaxAliases {
		return &LimitsViolation{Msg: fmt.Sprintf(
			"Query uses %d aliases; at most %d are allowed.", v.aliases, v.lim.MaxAliases)}
	}
	return nil
}

type walker struct {
	schema  *ast.Schema
	doc     *ast.QueryDocument
	lim     Limits
	aliases int
}

// walkSet sums the cost of a selection set. connSize is non-zero inside a
// Connection subtree: the connection's rows (its edges list) are charged
// at the requested page size exactly once.
func (w *walker) walkSet(set ast.SelectionSet, parent *ast.Definition, depth, connSize int) (int, error) {
	if depth > w.lim.MaxDepth {
		return 0, &LimitsViolation{Msg: fmt.Sprintf(
			"Query is %d levels deep; at most %d are allowed.", depth, w.lim.MaxDepth)}
	}
	cost := 0
	for _, sel := range set {
		var (
			c   int
			err error
		)
		switch s := sel.(type) {
		case *ast.Field:
			c, err = w.walkField(s, parent, depth, connSize)
		case *ast.FragmentSpread:
			frag := w.doc.Fragments.ForName(s.Name)
			if frag == nil {
				continue // schema validation rejects unknown fragments first
			}
			c, err = w.walkSet(frag.SelectionSet, w.schema.Types[frag.TypeCondition], depth+1, 0)
		case *ast.InlineFragment:
			t := parent
			if s.TypeCondition != "" {
				t = w.schema.Types[s.TypeCondition]
			}
			c, err = w.walkSet(s.SelectionSet, t, depth+1, connSize)
		}
		if err != nil {
			return 0, err
		}
		cost += c
		if cost > w.lim.MaxComplexity {
			return cost, &LimitsViolation{Msg: fmt.Sprintf(
				"Query complexity exceeds the limit of %d.", w.lim.MaxComplexity)}
		}
	}
	return cost, nil
}

func (w *walker) walkField(f *ast.Field, parent *ast.Definition, depth, connSize int) (int, error) {
	if f.Alias != "" && f.Alias != f.Name {
		w.aliases++
	}
	def := f.Definition
	if def == nil && parent != nil {
		def = parent.Fields.ForName(f.Name) // meta fields like __typename
	}
	if def == nil {
		return 1, nil
	}
	typeName := def.Type.Name()
	switch {
	case strings.HasSuffix(typeName, "Connection"):
		// Fan-out is charged at the requested page size, inside (edges).
		size := defaultPageSize
		if n, bounded := staticIntArg(f.Arguments, "first"); bounded {
			if n < 0 || n > w.lim.MaxPageSize {
				return 0, &LimitsViolation{Msg: fmt.Sprintf(
					"first=%d is out of range; pages may request at most %d.", n, w.lim.MaxPageSize)}
			}
			size = n
		}
		child, err := w.walkSet(f.SelectionSet, w.schema.Types[typeName], depth+1, size)
		if err != nil {
			return 0, err
		}
		return 1 + child, nil
	case def.Type.Elem != nil:
		// Plain list: rows multiply the subtree. Inside a Connection, the
		// items list inherits the connection's page size.
		mult := defaultPageSize
		if connSize > 0 && f.Name == "items" {
			mult = connSize
		}
		if n, bounded := staticIntArg(f.Arguments, "first"); bounded {
			if n < 0 || n > w.lim.MaxPageSize {
				return 0, &LimitsViolation{Msg: fmt.Sprintf(
					"first=%d is out of range; pages may request at most %d.", n, w.lim.MaxPageSize)}
			}
			mult = n
		}
		child, err := w.walkSet(f.SelectionSet, w.schema.Types[typeName], depth+1, 0)
		if err != nil {
			return 0, err
		}
		return 1 + child*mult, nil
	default:
		child, err := w.walkSet(f.SelectionSet, w.schema.Types[typeName], depth+1, 0)
		if err != nil {
			return 0, err
		}
		return 1 + child, nil
	}
}

// staticIntArg reads a static Int argument. bounded=false means the value
// is a runtime variable (SDL default already charged; the resolver clamps
// at execution time) or absent.
func staticIntArg(args ast.ArgumentList, name string) (val int, bounded bool) {
	for _, a := range args {
		if a.Name != name || a.Value == nil {
			continue
		}
		if a.Value.Kind == ast.Variable {
			return 0, false
		}
		n, err := strconv.Atoi(a.Value.Raw)
		if err != nil {
			return 0, false // non-integer literal: schema validation rejects it
		}
		return n, true
	}
	return 0, false
}
