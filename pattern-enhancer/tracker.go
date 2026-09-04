package patternenhancer

import "sync"

// Tracker reconciles what the user is doing right now against what the daemon
// has confirmed, so the volume display can move at knob speed without lying.
//
// The volume keys come from a rotary encoder. Waiting for a round trip per
// detent would make the bar lag badly behind the knob; showing raw predictions
// and then snapping to whatever arrives would make it jump backwards mid-turn,
// because a confirmation in flight describes an older, lower value than the one
// the user has already spun past.
//
// The fix is to never display an authoritative number on its own. Display is
// always confirmed volume plus the motion not yet accounted for:
//
//	display = confirmed.Volume + outstanding
//
// A confirmation subtracts exactly the motion it covers from outstanding, so
// the sum stays monotonic through a burst and lands on the true value when the
// last acknowledgement arrives.
type Tracker struct {
	mu          sync.Mutex
	confirmed   State
	outstanding int // predicted steps the daemon has not confirmed yet
	pending     int // subset of outstanding not yet sent
}

func NewTracker() *Tracker {
	return &Tracker{}
}

// Nudge records local motion — one detent, or a coarse/fine key press.
func (t *Tracker) Nudge(steps int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.outstanding += steps
	t.pending += steps
}

// Flush claims the motion not yet sent, for delivery to the daemon as a single
// step count. It stays in outstanding until confirmed, so it keeps showing.
func (t *Tracker) Flush() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	steps := t.pending
	t.pending = 0
	return steps
}

// Settle folds in authoritative state from the daemon, discounting the motion
// that state already accounts for.
//
// appliedSteps is how much of the outstanding prediction this state covers —
// the step count returned by the Flush whose command produced it, or zero for
// an unsolicited update such as someone turning the physical knob.
//
// Older states are ignored: commands and the event stream arrive on separate
// connections, so a lower Seq is stale by definition.
func (t *Tracker) Settle(st State, appliedSteps int) int {
	t.mu.Lock()
	defer t.mu.Unlock()

	// A different Epoch means a different daemon process. Its Seq counter is
	// unrelated to the one this tracker has been comparing against, so the
	// only safe move is to adopt it wholesale and throw away any prediction —
	// motion in flight to the old instance will never be confirmed by this
	// one, and leaving it in outstanding would inflate the display forever.
	if st.Epoch != t.confirmed.Epoch {
		t.confirmed = st
		t.outstanding = 0
		t.pending = 0
		return t.display()
	}

	if st.Seq >= t.confirmed.Seq {
		t.confirmed = st
	}

	t.outstanding -= appliedSteps
	if t.outstanding < 0 {
		t.outstanding = 0
	}

	return t.display()
}

// Confirmed returns the last authoritative state, without prediction.
func (t *Tracker) Confirmed() State {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.confirmed
}

// Outstanding reports predicted-but-unconfirmed motion. Non-zero means the
// display is ahead of what the daemon has agreed to.
func (t *Tracker) Outstanding() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.outstanding
}

// Display is the volume to render: confirmed plus prediction, clamped to the
// range the device advertises.
func (t *Tracker) Display() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.display()
}

// Revert drops the prediction and falls back to confirmed state. Used when
// motion goes unconfirmed past a timeout, so the UI stops showing a volume the
// receiver never reached.
func (t *Tracker) Revert() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.outstanding = 0
	t.pending = 0
	return t.display()
}

// display requires t.mu.
func (t *Tracker) display() int {
	v := t.confirmed.Volume + t.outstanding

	// Predicting past the device's ceiling would show a value it can never
	// reach, which guarantees a visible snap back on the next confirmation.
	r := t.confirmed.Range
	if r.Max > r.Min {
		if v > r.Max {
			v = r.Max
		}
		if v < r.Min {
			v = r.Min
		}
	}
	return v
}
