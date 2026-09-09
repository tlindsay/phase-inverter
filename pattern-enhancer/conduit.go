package patternenhancer

import (
	"context"
	"sync"
	"time"
)

// Display is what a UI renders: a volume that tracks the knob, plus enough
// context to be honest about how much of it is actually known to be true.
type Display struct {
	Volume  int
	Input   string
	Power   Power
	Mute    bool
	Range   Range
	Playing Playing

	// Confirmed is false while local motion is still unacknowledged. A UI
	// should render an unconfirmed value differently rather than presenting a
	// prediction as fact.
	Confirmed bool

	// Connected reports whether the event stream is currently up. Commands
	// still work when it is false — they travel on their own requests — but
	// changes made elsewhere will not appear until it recovers.
	Connected bool

	// Online reports whether the daemon can reach the receiver. Distinct from
	// Connected: the daemon may be perfectly reachable over the tailnet while
	// the stereo itself is asleep and answering nothing.
	Online bool
}

// Conduit drives a pattern-enhancer daemon on behalf of a UI.
//
// It exists because the volume keys are a rotary encoder. A single flick
// produces a dense run of detents, and the naive handling of that — one request
// per detent, display driven by whatever comes back — fails twice over: it
// floods the receiver, and it makes the bar lurch backwards whenever a
// confirmation describing an older value lands mid-turn.
//
// So detents accumulate in a short window and go out as one step count, while
// the display comes from a Tracker that adds unconfirmed motion on top of
// confirmed state. The bar moves at knob speed and still lands on the truth.
type Conduit struct {
	client  *Client
	tracker *Tracker
	window  time.Duration

	nudges chan int

	mu        sync.Mutex
	connected bool
}

// defaultWindow is short enough to feel instantaneous and long enough to
// swallow a fast flick of the encoder.
const defaultWindow = 50 * time.Millisecond

func NewConduit(c *Client, window time.Duration) *Conduit {
	if window <= 0 {
		window = defaultWindow
	}
	return &Conduit{
		client:  c,
		tracker: NewTracker(),
		window:  window,
		nudges:  make(chan int, 256),
	}
}

// Nudge records one detent, or a coarse/fine keypress worth of steps.
//
// It never blocks and never performs I/O: the encoder can outrun the network
// freely, and the accumulated motion goes out on the next window tick.
func (c *Conduit) Nudge(steps int) {
	c.tracker.Nudge(steps)
	select {
	case c.nudges <- steps:
	default:
		// Buffer full: the motion is already recorded in the tracker and the
		// pending flush will pick it up, so nothing is lost by dropping the
		// wakeup itself.
	}
}

// Display returns what the UI should render right now.
func (c *Conduit) Display() Display {
	st := c.tracker.Confirmed()

	c.mu.Lock()
	connected := c.connected
	c.mu.Unlock()

	return Display{
		Volume:    c.tracker.Display(),
		Input:     st.Input,
		Power:     st.Power,
		Mute:      st.Mute,
		Range:     st.Range,
		Playing:   st.Playing,
		Confirmed: c.tracker.Outstanding() == 0,
		Connected: connected,
		Online:    st.Online,
	}
}

// SetInput switches inputs, settling the display on the daemon's answer.
func (c *Conduit) SetInput(ctx context.Context, input string) error {
	st, err := c.client.SetInput(ctx, input)
	if err != nil {
		return err
	}
	c.tracker.Settle(st, 0)
	return nil
}

// RecallPreset selects a stored station, settling the display on the daemon's
// answer exactly as SetInput does — a recall changes the input too.
func (c *Conduit) RecallPreset(ctx context.Context, num int) error {
	st, err := c.client.RecallPreset(ctx, num)
	if err != nil {
		return err
	}
	c.tracker.Settle(st, 0)
	return nil
}

// Presets lists the stations the daemon can recall. Pass-through: there is no
// state to track and nothing to predict.
func (c *Conduit) Presets(ctx context.Context) ([]Preset, error) {
	return c.client.Presets(ctx)
}

// TogglePower flips power at the receiver.
func (c *Conduit) TogglePower(ctx context.Context) error {
	st, err := c.client.TogglePower(ctx)
	if err != nil {
		return err
	}
	c.tracker.Settle(st, 0)
	return nil
}

// SetPower drives power to an explicit state.
func (c *Conduit) SetPower(ctx context.Context, on bool) error {
	st, err := c.client.SetPower(ctx, on)
	if err != nil {
		return err
	}
	c.tracker.Settle(st, 0)
	return nil
}

// ToggleMute flips mute at the receiver.
func (c *Conduit) ToggleMute(ctx context.Context) error {
	st, err := c.client.ToggleMute(ctx)
	if err != nil {
		return err
	}
	c.tracker.Settle(st, 0)
	return nil
}

// Run drives the conduit until ctx is cancelled: it flushes accumulated volume
// motion and folds in pushed state from the daemon.
func (c *Conduit) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); c.runFlusher(ctx) }()
	go func() { defer wg.Done(); c.runEvents(ctx) }()
	wg.Wait()
}

// runFlusher batches detents into single commands.
func (c *Conduit) runFlusher(ctx context.Context) {
	timer := time.NewTimer(c.window)
	if !timer.Stop() {
		<-timer.C
	}
	armed := false

	for {
		select {
		case <-c.nudges:
			// Restart the window on every detent so a continuous turn goes
			// out as one command rather than one per window.
			if armed && !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(c.window)
			armed = true

		case <-timer.C:
			armed = false
			c.flush(ctx)

		case <-ctx.Done():
			return
		}
	}
}

// flush sends the accumulated motion as one command.
func (c *Conduit) flush(ctx context.Context) {
	steps := c.tracker.Flush()
	if steps == 0 {
		return
	}

	st, err := c.client.AdjustVolume(ctx, steps)
	if err != nil {
		// The motion never reached the daemon. Drop the prediction rather
		// than leaving the display permanently ahead of reality.
		c.tracker.Revert()
		return
	}
	c.tracker.Settle(st, steps)
}

// runEvents keeps the display current with changes made anywhere — the phone
// app, the remote, the front-panel knob.
func (c *Conduit) runEvents(ctx context.Context) {
	states := c.client.Events(ctx)
	for st := range states {
		c.setConnected(true)
		// appliedSteps is zero: an unsolicited update accounts for none of
		// the local prediction, so outstanding motion keeps showing on top.
		c.tracker.Settle(st, 0)
	}
	c.setConnected(false)
}

func (c *Conduit) setConnected(v bool) {
	c.mu.Lock()
	c.connected = v
	c.mu.Unlock()
}
