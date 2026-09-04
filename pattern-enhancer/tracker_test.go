package patternenhancer

import "testing"

func newTracker() *Tracker {
	t := NewTracker()
	t.Settle(State{Seq: 1, Volume: 45, Power: PowerOn,
		Range: Range{Min: 0, Max: 80, Step: 1}}, 0)
	return t
}

func TestDisplayShowsPredictedVolumeImmediately(t *testing.T) {
	tr := newTracker()

	tr.Nudge(4)

	// The bar has to move at knob speed, not network speed.
	if got := tr.Display(); got != 49 {
		t.Errorf("Display = %d, want 49 immediately after a nudge", got)
	}
}

func TestDisplayNeverGoesBackwardWhileTurningUp(t *testing.T) {
	tr := newTracker()

	// A burst: ten detents up, with a stale confirmation landing partway
	// through for only the first four.
	for i := 0; i < 10; i++ {
		tr.Nudge(1)
	}
	mid := tr.Display()

	sent := tr.Flush()
	if sent != 10 {
		t.Fatalf("Flush = %d, want 10", sent)
	}

	// Confirmation arrives describing only part of the motion — the receiver
	// had applied four steps when this snapshot was taken.
	tr.Settle(State{Seq: 2, Volume: 49, Power: PowerOn,
		Range: Range{Min: 0, Max: 80, Step: 1}}, 4)

	after := tr.Settle(State{Seq: 3, Volume: 55, Power: PowerOn,
		Range: Range{Min: 0, Max: 80, Step: 1}}, 6)

	if after < mid {
		t.Errorf("Display went backward mid-burst: %d -> %d", mid, after)
	}
	if after != 55 {
		t.Errorf("Display = %d, want 55 once the whole burst is confirmed", after)
	}
}

func TestOutstandingClearsOnceFullyConfirmed(t *testing.T) {
	tr := newTracker()

	tr.Nudge(6)
	sent := tr.Flush()
	tr.Settle(State{Seq: 2, Volume: 51, Power: PowerOn,
		Range: Range{Min: 0, Max: 80, Step: 1}}, sent)

	if tr.Outstanding() != 0 {
		t.Errorf("Outstanding = %d, want 0 after full confirmation", tr.Outstanding())
	}
	if got := tr.Display(); got != 51 {
		t.Errorf("Display = %d, want the confirmed 51", got)
	}
}

func TestDisplayClampsToAdvertisedRange(t *testing.T) {
	tr := newTracker()

	tr.Nudge(100)

	// Predicting past the device's ceiling would show a number the receiver
	// can never reach, guaranteeing a visible snap back.
	if got := tr.Display(); got != 80 {
		t.Errorf("Display = %d, want it clamped to the advertised max of 80", got)
	}
}

func TestFlushTakesPendingMotionOnlyOnce(t *testing.T) {
	tr := newTracker()

	tr.Nudge(3)
	first := tr.Flush()
	second := tr.Flush()

	if first != 3 {
		t.Errorf("first Flush = %d, want 3", first)
	}
	// Flushing twice must not re-send motion already on the wire.
	if second != 0 {
		t.Errorf("second Flush = %d, want 0", second)
	}
	// But it is still unconfirmed, so it must still show.
	if got := tr.Display(); got != 48 {
		t.Errorf("Display = %d, want 48 while in flight", got)
	}
}

func TestStaleUpdateFromTheSameDaemonIsIgnored(t *testing.T) {
	tr := newTracker()

	tr.Settle(State{Seq: 9, Volume: 60, Power: PowerOn, Epoch: "abc",
		Range: Range{Min: 0, Max: 80, Step: 1}}, 0)

	// A lower Seq from the same daemon is genuinely stale — it lost the race
	// against the command response that already reported a newer value.
	got := tr.Settle(State{Seq: 7, Volume: 50, Power: PowerOn, Epoch: "abc",
		Range: Range{Min: 0, Max: 80, Step: 1}}, 0)

	if got != 60 {
		t.Errorf("Display = %d, want 60 — a stale same-epoch update must not win", got)
	}
}

func TestUpdateFromARestartedDaemonIsAdopted(t *testing.T) {
	tr := newTracker()

	tr.Settle(State{Seq: 9, Volume: 60, Power: PowerOn, Epoch: "abc",
		Range: Range{Min: 0, Max: 80, Step: 1}}, 0)

	// The daemon restarts and its Seq counter starts over. Every value it
	// sends now looks "older" than what the client already holds, so a
	// Seq-only comparison discards all of them and the display freezes
	// forever at a number the receiver left behind long ago.
	got := tr.Settle(State{Seq: 1, Volume: 30, Power: PowerOn, Epoch: "xyz",
		Range: Range{Min: 0, Max: 80, Step: 1}}, 0)

	if got != 30 {
		t.Errorf("Display = %d, want 30 — a new epoch must be adopted regardless of Seq", got)
	}
}

func TestRestartClearsOutstandingPrediction(t *testing.T) {
	tr := newTracker()

	tr.Nudge(10)
	tr.Flush()

	// Whatever motion was in flight cannot be confirmed by a daemon that no
	// longer remembers it, so it must not keep inflating the display.
	tr.Settle(State{Seq: 1, Volume: 30, Power: PowerOn, Epoch: "xyz",
		Range: Range{Min: 0, Max: 80, Step: 1}}, 0)

	if tr.Outstanding() != 0 {
		t.Errorf("Outstanding = %d after a daemon restart, want 0", tr.Outstanding())
	}
	if got := tr.Display(); got != 30 {
		t.Errorf("Display = %d, want 30", got)
	}
}

func TestExternalChangeIsAdoptedWhenNothingOutstanding(t *testing.T) {
	tr := newTracker()

	// Someone turned the physical knob. With no local prediction pending,
	// the daemon's value is simply the truth.
	got := tr.Settle(State{Seq: 9, Volume: 30, Power: PowerOn,
		Range: Range{Min: 0, Max: 80, Step: 1}}, 0)

	if got != 30 {
		t.Errorf("Display = %d, want 30", got)
	}
}
