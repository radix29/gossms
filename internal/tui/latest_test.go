package tui

// latest is tuikit's loop.Latest, whose contract is pinned by its own tests
// (tuikit loop/latest_test.go). What is left here is the two test-only views
// of its token that the gossms tests used to get by touching its fields.

// latestAt advances l until token n is the current one, as if n runs had been
// started; n must not be below l's current token. It stands in for a test
// that needs a specific run current without starting it (a panic repair or a
// stale-guard check driven directly).
func latestAt(l *latest, n int) {
	for !l.Current(n) {
		l.Abandon()
	}
}

// latestToken returns l's current token — the run a test is about to check
// is (or is no longer) current. Tokens start at 0 and only grow.
func latestToken(l *latest) int {
	for n := 0; ; n++ {
		if l.Current(n) {
			return n
		}
	}
}
