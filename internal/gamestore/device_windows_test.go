//go:build windows

package gamestore

import "testing"

func TestWindowsDeviceGameIDsAreRejected(t *testing.T) {
	for _, id := range []string{"con", "prn", "aux", "nul", "com1", "com9", "lpt1", "lpt9"} {
		// Stop before any I/O if validation regresses: the negative fixture
		// must never open a DOS device to find out whether it is safe.
		if err := ValidateID(id); err == nil {
			t.Fatalf("Windows device identifier %q was accepted", id)
		}
	}
	s := newStore(t)
	record, _ := sampleRecord(t)
	for _, id := range []string{"console", "nul1", "com10", "lpt0", "con-game"} {
		if err := s.Put(Saved{ID: id, Kind: Hotseat, Player: "Ada", Opponent: "Ben", Record: record, Finished: true}); err != nil {
			t.Fatalf("ordinary identifier %q was over-rejected: %v", id, err)
		}
		if got, err := s.Get(id); err != nil || got.ID != id {
			t.Fatalf("ordinary identifier %q did not read back: %+v %v", id, got, err)
		}
	}
}
