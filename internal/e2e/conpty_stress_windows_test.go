//go:build windows

package e2e

import (
	"strings"
	"testing"
	"time"
)

// Both channels must progress while a large input is echoed. The old synchronous
// emulator-to-ConPTY bridge held the screen mutex until all input was consumed,
// preventing output from draining and deadlocking the input producer.
func TestConPTYLargeInputAndOutputProgressTogether(t *testing.T) {
	tm := helperStart(t, Options{Width: 120, Height: 40}, "duplex")
	tm.MustWaitFor("DUPLEX-READY", startupTimeout)
	done := make(chan error, 1)
	go func() { done <- tm.be.sendText(strings.Repeat("a", 1<<20)) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ConPTY input and output stopped making progress")
	}
	tm.MustWaitFor("DUPLEX-DONE", 15*time.Second)
	if !tm.Alive() {
		t.Fatal("the helper exited instead of completing its duplex exchange")
	}
}

// Run under the Windows race detector too: closing the input pipe must join
// the emulator reader without racing on the emulator's own closed flag.
func TestConPTYCloseStopsTheProcessAndInputReader(t *testing.T) {
	for range 3 {
		tm := helperStart(t, Options{Width: 40, Height: 10}, "output", "CLOSE-READY")
		tm.MustWaitFor("CLOSE-READY", startupTimeout)
		tm.Close()
		if tm.Alive() {
			t.Fatal("the process survived terminal cleanup")
		}
	}
}
