package ui

import "testing"

// TestBarDoneIsIdempotent covers the double-bar-line bug: callers invoke
// Done() explicitly on the success path (before the follow-up message) and
// again through a deferred call. The finish sequence — close, wait, final
// render, newline — must run exactly once, and the second call must be a
// no-op rather than panicking on a double channel close.
func TestBarDoneIsIdempotent(t *testing.T) {
	b := NewBar(100, "test")
	for i := 0; i < 10; i++ {
		b.Add(10)
	}
	b.Done() // explicit, success path
	b.Done() // deferred, safety net
	b.Done() // any other path

	// Running under -race with no panic or double channel close is the
	// assertion; nothing more to check here.
}

// TestBarSilentWhenNotTerminal documents that a piped stdout keeps the bar
// quiet: without a terminal the ticker never starts and the Done sequence
// must not render or print. Tests run with a non-TTY os.Stdout, so this
// exercises the silent branch directly.
func TestBarSilentWhenNotTerminal(t *testing.T) {
	b := NewBar(50, "quiet")
	b.Add(50)
	b.Done()
}
