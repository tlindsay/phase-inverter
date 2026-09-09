package patternenhancer_test

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	pe "github.com/tlindsay/phase-inverter/pattern-enhancer"
	"github.com/tlindsay/phase-inverter/pattern-enhancer/server"
)

// countingBackend records how many separate volume commands reach the daemon,
// which is the number a rotary encoder burst must not blow up.
type countingBackend struct {
	*stubBackend
	mu       sync.Mutex
	commands int
	steps    int
}

func newCounting() *countingBackend {
	return &countingBackend{stubBackend: newStub()}
}

func (c *countingBackend) AdjustVolume(ctx context.Context, steps int) error {
	c.mu.Lock()
	c.commands++
	c.steps += steps
	c.mu.Unlock()
	return c.stubBackend.AdjustVolume(ctx, steps)
}

func (c *countingBackend) totals() (commands, steps int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.commands, c.steps
}

func liveConduit(t *testing.T, window time.Duration) (*countingBackend, *pe.Conduit) {
	t.Helper()
	be := newCounting()
	srv := server.New("office", be)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	cd := pe.NewConduit(pe.NewClient(ts.URL, "office"), window)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go cd.Run(ctx)

	return be, cd
}

func TestConduitCoalescesAnEncoderBurstIntoOneCommand(t *testing.T) {
	be, cd := liveConduit(t, 50*time.Millisecond)

	// One flick of the wrist: twenty detents in rapid succession.
	for i := 0; i < 20; i++ {
		cd.Nudge(1)
	}

	waitUntil(t, 3*time.Second, func() bool {
		_, steps := be.totals()
		return steps == 20
	}, "all 20 steps to reach the daemon")

	commands, steps := be.totals()
	if steps != 20 {
		t.Errorf("daemon saw %d steps, want 20 — motion was lost", steps)
	}
	// The whole point of the window: twenty detents must not be twenty HTTP
	// requests. The receiver would not survive it and the bar would stutter.
	if commands > 3 {
		t.Errorf("daemon saw %d commands for one burst, want a small number", commands)
	}
}

func TestConduitDisplayTracksTheKnobImmediately(t *testing.T) {
	_, cd := liveConduit(t, 50*time.Millisecond)

	waitUntil(t, 3*time.Second, func() bool {
		return cd.Display().Volume == 45
	}, "the initial snapshot to arrive")

	cd.Nudge(5)

	// No sleep: the display must reflect the knob before any round trip.
	if got := cd.Display().Volume; got != 50 {
		t.Errorf("Display().Volume = %d immediately after Nudge, want 50", got)
	}
	if cd.Display().Confirmed {
		t.Error("Display().Confirmed = true for motion the daemon has not acknowledged")
	}
}

func TestConduitDisplaySettlesOnConfirmedValue(t *testing.T) {
	_, cd := liveConduit(t, 30*time.Millisecond)

	waitUntil(t, 3*time.Second, func() bool {
		return cd.Display().Volume == 45
	}, "the initial snapshot to arrive")

	cd.Nudge(4)

	waitUntil(t, 3*time.Second, func() bool {
		d := cd.Display()
		return d.Confirmed && d.Volume == 49
	}, "the display to settle on a confirmed 49")
}

func TestConduitDisplayNeverGoesBackwardDuringABurst(t *testing.T) {
	_, cd := liveConduit(t, 30*time.Millisecond)

	waitUntil(t, 3*time.Second, func() bool {
		return cd.Display().Volume == 45
	}, "the initial snapshot to arrive")

	// Spin the knob while confirmations stream back concurrently, sampling
	// the display throughout. This is the regression that makes an optimistic
	// UI feel broken: a confirmation describing an older, lower value landing
	// while the user is still turning up.
	var mu sync.Mutex
	lowest := 45
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		last := 45
		for {
			select {
			case <-stop:
				return
			default:
			}
			v := cd.Display().Volume
			mu.Lock()
			if v < last {
				lowest = v
			}
			last = v
			mu.Unlock()
			time.Sleep(time.Millisecond)
		}
	}()

	for i := 0; i < 25; i++ {
		cd.Nudge(1)
		time.Sleep(2 * time.Millisecond)
	}

	waitUntil(t, 5*time.Second, func() bool {
		d := cd.Display()
		return d.Confirmed && d.Volume == 70
	}, "the burst to fully settle at 70")

	close(stop)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if lowest < 45 {
		t.Errorf("display dropped to %d mid-burst; it must never move backward while turning up", lowest)
	}
}

func waitUntil(t *testing.T, limit time.Duration, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestConduitRecallPresetSettlesTheDisplay(t *testing.T) {
	_, cd := liveConduit(t, 30*time.Millisecond)

	if err := cd.RecallPreset(context.Background(), 2); err != nil {
		t.Fatalf("RecallPreset returned error: %v", err)
	}

	// The command's own response is authoritative, so the menu reflects the
	// new station without waiting for the event stream to catch up.
	d := cd.Display()
	if d.Input != "siriusxm" {
		t.Errorf("Display().Input = %q, want %q", d.Input, "siriusxm")
	}
	if d.Playing.Album != "35 : SiriusXMU / Indie & Beyond" {
		t.Errorf("Display().Playing.Album = %q, want the recalled station", d.Playing.Album)
	}
}

func TestConduitListsStationsFromTheDaemon(t *testing.T) {
	_, cd := liveConduit(t, 30*time.Millisecond)

	got, err := cd.Presets(context.Background())
	if err != nil {
		t.Fatalf("Presets returned error: %v", err)
	}
	if len(got) != 2 || got[1].Num != 2 {
		t.Errorf("Presets = %+v, want the daemon's two stations", got)
	}
}
