package showplan

import "strings"

// KV is one ordered key/value pair, preserving document order for
// property listings.
type KV struct {
	Key, Value string
}

// Plan is a parsed ShowPlanXML document.
type Plan struct {
	Version    string
	Build      string
	Statements []*Statement
	XML        string // the source document, decoded to UTF-8
}

// HasActual reports whether any node carries runtime counters — i.e.
// whether this is an actual plan rather than an estimated one.
func (p *Plan) HasActual() bool {
	for _, st := range p.Statements {
		for _, n := range st.Nodes() {
			if n.Runtime != nil {
				return true
			}
		}
	}
	return false
}

// Statement is one statement of the batch with its operator tree. Root is
// nil for statements that carry no query plan (SET, USE, ...).
type Statement struct {
	Text        string  // StatementText
	Type        string  // StatementType: SELECT, UPDATE, ...
	SubTreeCost float64 // StatementSubTreeCost — denominator for node cost %
	EstRows     float64
	DOP         int // QueryPlan DegreeOfParallelism
	QueryHash   string

	TimeStats      *TimeStats   // nil on estimated plans
	MemoryGrant    *MemoryGrant // nil when absent
	MissingIndexes []MissingIndex
	Warnings       []string // statement-level warnings
	Root           *Node
	Props          []KV // all statement + QueryPlan attributes, in order
}

// Nodes returns every operator of the statement in preorder.
func (s *Statement) Nodes() []*Node {
	var out []*Node
	var walk func(n *Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		out = append(out, n)
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(s.Root)
	return out
}

// TimeStats is the statement's QueryTimeStats, in milliseconds.
type TimeStats struct {
	CPUMS     int64
	ElapsedMS int64
}

// MemoryGrant is the statement's MemoryGrantInfo, in kilobytes.
type MemoryGrant struct {
	GrantedKB int64
	MaxUsedKB int64
}

// MissingIndex is one index the optimizer flagged as missing.
type MissingIndex struct {
	Impact   float64
	Database string
	Schema   string
	Table    string
	Equality []string // columns with EQUALITY usage
	Include  []string // columns with INCLUDE usage
}

// Object identifies the database object an operator touches. All names
// are stored without the surrounding [brackets].
type Object struct {
	Database  string
	Schema    string
	Table     string
	Index     string
	Alias     string
	IndexKind string
}

// IsZero reports whether no object was recorded.
func (o Object) IsZero() bool { return o == Object{} }

// Short returns "schema.table [index]", omitting missing parts.
func (o Object) Short() string {
	var sb strings.Builder
	if o.Schema != "" {
		sb.WriteString(o.Schema)
		sb.WriteByte('.')
	}
	sb.WriteString(o.Table)
	if o.Index != "" {
		sb.WriteString(" [")
		sb.WriteString(o.Index)
		sb.WriteByte(']')
	}
	return sb.String()
}

// Node is one operator (RelOp) in a statement's plan tree.
type Node struct {
	ID             int // NodeId
	PhysicalOp     string
	LogicalOp      string
	Object         Object
	EstRows        float64
	EstRowsRead    float64
	EstIO          float64
	EstCPU         float64
	EstSubtreeCost float64
	AvgRowSize     float64
	Parallel       bool
	ExecMode       string // EstimatedExecutionMode: Row / Batch

	Predicate     string // ScalarString of the operator's Predicate
	SeekPredicate string // ScalarString of the first seek predicate
	OutputColumns []string
	Warnings      []string
	Runtime       *Runtime // nil = estimated-only node
	Props         []KV     // all RelOp + operator-element attributes, in order
	Children      []*Node
}

// Cost returns the node's own cost as a fraction of stmtTotal — the SSMS
// per-operator cost: subtree cost minus the children's subtree costs,
// clamped at zero.
func (n *Node) Cost(stmtTotal float64) float64 {
	own := n.EstSubtreeCost
	for _, c := range n.Children {
		own -= c.EstSubtreeCost
	}
	if own < 0 {
		own = 0
	}
	if stmtTotal <= 0 {
		return 0
	}
	return own / stmtTotal
}

// Runtime aggregates a node's RunTimeCountersPerThread entries: row and
// read counters are summed across threads, times take the slowest thread.
type Runtime struct {
	Rows          int64
	RowsRead      int64
	Executions    int64
	ElapsedMS     int64
	CPUMS         int64
	LogicalReads  int64
	PhysicalReads int64
	Threads       int
}
