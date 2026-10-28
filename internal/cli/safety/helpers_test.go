package safety

import "github.com/radix29/gossms/internal/tuikit/sqltext"

const wordKind = sqltext.KindWord

type testBatch struct {
	text      string
	startLine int
}

func splitForTest(script string) []testBatch {
	var out []testBatch
	for _, b := range sqltext.SplitBatches(script) {
		out = append(out, testBatch{b.Text, b.StartLine})
	}
	return out
}
