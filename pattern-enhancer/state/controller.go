package state

import (
	"context"
	"sync"
)

// VolumeSetter is the slice of the MusicCast client the Controller needs.
// Narrow on purpose: it keeps the burst logic testable without a receiver.
type VolumeSetter interface {
	SetVolumeRelative(ctx context.Context, steps int) error
}

// Controller serialises writes to one receiver and absorbs bursts.
//
// The volume hotkeys come from a rotary encoder, so a single flick of the wrist
// produces a dense run of discrete steps. Issuing one HTTP write per detent
// would both swamp the receiver and interleave unpredictably. Instead exactly
// one write is ever in flight; everything arriving behind it accumulates into a
// pending delta that is sent as a single step count when the current write
// finishes.
//
// Steps accumulate rather than replace, so no motion is ever dropped — turning
// up five and back down three lands at plus two, not at minus three.
type Controller struct {
	store *Store
	vol   VolumeSetter

	// AfterWrite runs after each successful write to the receiver. It exists
	// so state can be refreshed before a command handler replies: the pushed
	// UDP event confirming the write is both asynchronous and lossy, so
	// answering from cache would report the pre-command value.
	//
	// Deliberately hung off the write rather than off AdjustVolume: during a
	// burst most callers only add to the pending delta without writing
	// anything, and refreshing once per detent would undo the coalescing.
	AfterWrite func(ctx context.Context)

	mu       sync.Mutex
	pending  int
	inFlight bool
	idle     chan struct{} // closed and replaced each time the writer drains
}

func NewController(s *Store, vol VolumeSetter) *Controller {
	return &Controller{
		store: s,
		vol:   vol,
		idle:  make(chan struct{}),
	}
}

// AdjustVolume moves the volume by steps, signed for direction.
//
// It returns as soon as the change is accepted, which may be before it reaches
// the receiver. The caller learns the real outcome from state, not from this
// return value — that separation is what lets a knob spin freely without each
// detent waiting on a round trip.
func (c *Controller) AdjustVolume(ctx context.Context, steps int) error {
	c.mu.Lock()
	c.pending += steps

	if c.inFlight {
		// Someone else is already writing; they will pick this up.
		c.mu.Unlock()
		return nil
	}
	c.inFlight = true
	c.mu.Unlock()

	return c.drain(ctx)
}

// drain writes the accumulated delta, repeating while more piles up behind it.
func (c *Controller) drain(ctx context.Context) error {
	for {
		c.mu.Lock()
		steps := c.pending
		c.pending = 0
		if steps == 0 {
			c.inFlight = false
			close(c.idle)
			c.idle = make(chan struct{})
			c.mu.Unlock()
			return nil
		}
		c.mu.Unlock()

		if err := c.vol.SetVolumeRelative(ctx, steps); err != nil {
			// Abandon the motion rather than banking it for later.
			//
			// Re-queueing looks like it preserves intent, but nothing here
			// ever retries: the steps would simply sit in the accumulator and
			// ride along with whatever command came next, whenever that was.
			// A failed nudge from while the receiver was in standby would
			// then land minutes later on top of an unrelated keypress, and
			// the volume would jump somewhere nobody asked for. The client
			// reverts its own prediction on error, so dropping it here is
			// what keeps both ends agreeing.
			c.mu.Lock()
			c.pending = 0
			c.inFlight = false
			close(c.idle)
			c.idle = make(chan struct{})
			c.mu.Unlock()
			return err
		}

		if c.AfterWrite != nil {
			c.AfterWrite(ctx)
		}
	}
}

// Drain blocks until no write is in flight and nothing is pending.
//
// Exists for tests and for shutdown, where losing a half-applied burst would
// leave the receiver somewhere the user did not ask for.
func (c *Controller) Drain() {
	for {
		c.mu.Lock()
		if !c.inFlight && c.pending == 0 {
			c.mu.Unlock()
			return
		}
		wait := c.idle
		c.mu.Unlock()
		<-wait
	}
}
