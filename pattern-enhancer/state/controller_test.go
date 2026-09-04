package state

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	pe "github.com/tlindsay/phase-inverter/pattern-enhancer"
)

// fakeVolume records every write and can be made slow, so a burst of callers
// piles up behind one in-flight request the way it does against real hardware.
type fakeVolume struct {
	mu      sync.Mutex
	calls   []int
	delay   time.Duration
	err     error
	entered chan struct{} // signalled once the first call is inside
	release chan struct{} // blocks the first call until closed
	once    sync.Once
}

func (f *fakeVolume) SetVolumeRelative(ctx context.Context, steps int) error {
	f.once.Do(func() {
		if f.entered != nil {
			close(f.entered)
		}
		if f.release != nil {
			<-f.release
		}
	})

	if f.delay > 0 {
		time.Sleep(f.delay)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, steps)
	return f.err
}

func (f *fakeVolume) totals() (calls int, steps int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.calls {
		steps += s
	}
	return len(f.calls), steps
}

func TestAdjustVolumeCoalescesBurstBehindInFlightWrite(t *testing.T) {
	fake := &fakeVolume{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	ctl := NewController(NewStore(pe.Range{Min: 0, Max: 80, Step: 1}), fake)

	// One writer gets in and is held there.
	go func() { _ = ctl.AdjustVolume(context.Background(), 1) }()
	<-fake.entered

	// A rotary encoder's worth of detents arrives while that write is stuck.
	var wg sync.WaitGroup
	for i := 0; i < 9; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = ctl.AdjustVolume(context.Background(), 1)
		}()
	}

	// Give them all time to land in the accumulator, then let the first go.
	time.Sleep(100 * time.Millisecond)
	close(fake.release)
	wg.Wait()
	ctl.Drain()

	calls, steps := fake.totals()
	if steps != 10 {
		t.Errorf("device received %d total steps, want 10 — motion was lost", steps)
	}
	// The whole point: ten detents must not become ten HTTP writes.
	if calls >= 10 {
		t.Errorf("device received %d separate writes for 10 detents, want far fewer", calls)
	}
}

func TestAdjustVolumeSendsOppositeDirectionsNetted(t *testing.T) {
	fake := &fakeVolume{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	ctl := NewController(NewStore(pe.Range{Min: 0, Max: 80, Step: 1}), fake)

	go func() { _ = ctl.AdjustVolume(context.Background(), 5) }()
	<-fake.entered

	var wg sync.WaitGroup
	for _, d := range []int{-2, -1} {
		wg.Add(1)
		go func(d int) {
			defer wg.Done()
			_ = ctl.AdjustVolume(context.Background(), d)
		}(d)
	}
	time.Sleep(100 * time.Millisecond)
	close(fake.release)
	wg.Wait()
	ctl.Drain()

	_, steps := fake.totals()
	// Turning the knob up then back down must land where the user left it.
	if steps != 2 {
		t.Errorf("net steps = %d, want 2", steps)
	}
}

func TestFailedWriteDoesNotLeakIntoTheNextCommand(t *testing.T) {
	fake := &fakeVolume{err: errors.New("response_code 5")}
	ctl := NewController(NewStore(pe.Range{Min: 0, Max: 80, Step: 1}), fake)

	// Fails because the receiver is in standby and refuses volume changes.
	if err := ctl.AdjustVolume(context.Background(), 6); err == nil {
		t.Fatal("expected an error from the failing write")
	}

	// Later, with the receiver awake, the user asks for a small change.
	fake.mu.Lock()
	fake.err = nil
	fake.calls = nil
	fake.mu.Unlock()

	if err := ctl.AdjustVolume(context.Background(), 1); err != nil {
		t.Fatalf("AdjustVolume returned error: %v", err)
	}
	ctl.Drain()

	_, steps := fake.totals()
	// Motion that failed must be abandoned, not banked. Replaying it here
	// would jump the volume by 7 when the user asked for 1 — and the earlier
	// request could be minutes old, from before the receiver was even on.
	if steps != 1 {
		t.Errorf("device received %d steps, want 1 — the failed write leaked forward", steps)
	}
}

func TestAdjustVolumeSingleCallWritesImmediately(t *testing.T) {
	fake := &fakeVolume{}
	ctl := NewController(NewStore(pe.Range{Min: 0, Max: 80, Step: 1}), fake)

	if err := ctl.AdjustVolume(context.Background(), 4); err != nil {
		t.Fatalf("AdjustVolume returned error: %v", err)
	}
	ctl.Drain()

	calls, steps := fake.totals()
	if calls != 1 || steps != 4 {
		t.Errorf("calls=%d steps=%d, want calls=1 steps=4", calls, steps)
	}
}
