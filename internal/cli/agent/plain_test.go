package agent

import "testing"

func TestPlain(t *testing.T) {
	md := "# Title\n\nsome `code` and **bold**\n\n## Sec\n\n| A | Bee |\n|---|---|\n| x\\|y | `z` |\n| long cell | 1 |\n\n```bash\ngossms version\n```\n\n### Sub\n"
	want := "Title\n=====\n\nsome code and bold\n\nSec\n---\n\nA          Bee\nx|y        z\nlong cell  1\n\n    gossms version\n\nSub\n"
	if got := Plain(md); got != want {
		t.Errorf("Plain =\n%s\nwant\n%s", got, want)
	}
}
