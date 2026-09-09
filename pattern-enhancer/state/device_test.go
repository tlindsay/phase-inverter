package state

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	pe "github.com/tlindsay/phase-inverter/pattern-enhancer"
	"github.com/tlindsay/phase-inverter/pattern-enhancer/musiccast"
)

type fakeDevice struct {
	mu        sync.Mutex
	status    musiccast.Status
	muteSetTo *bool
	polls     int

	playInfo      musiccast.PlayInfo
	playInfoErr   error
	playInfoCalls int
	presets       []pe.Preset
	recalled      int
}

func newFakeDevice() *fakeDevice {
	return &fakeDevice{status: musiccast.Status{
		Power: "on", Volume: 45, Mute: false, Input: "phono",
	}}
}

func (f *fakeDevice) GetStatus(context.Context) (musiccast.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polls++
	return f.status, nil
}

// SetVolumeRelative moves the fake's own status, so a subsequent GetStatus
// reports the new value — the way real hardware behaves.
func (f *fakeDevice) SetVolumeRelative(_ context.Context, steps int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status.Volume += steps
	return nil
}

func (f *fakeDevice) SetInput(_ context.Context, in string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status.Input = in
	return nil
}

func (f *fakeDevice) SetPower(context.Context, bool) error { return nil }
func (f *fakeDevice) TogglePower(context.Context) error    { return nil }

func (f *fakeDevice) SetMute(_ context.Context, on bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.muteSetTo = &on
	return nil
}

func (f *fakeDevice) GetPlayInfo(context.Context) (musiccast.PlayInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.playInfoCalls++
	if f.playInfoErr != nil {
		return musiccast.PlayInfo{}, f.playInfoErr
	}
	return f.playInfo, nil
}

func (f *fakeDevice) GetPresetInfo(context.Context) ([]pe.Preset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.presets, nil
}

// RecallPreset moves the fake's own input, the way the real recall does: the
// receiver switches to the preset's source as part of recalling it.
func (f *fakeDevice) RecallPreset(_ context.Context, num int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recalled = num
	f.status.Input = "siriusxm"
	return nil
}

func (f *fakeDevice) playInfoCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.playInfoCalls
}

func (f *fakeDevice) pollCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.polls
}

func newTestDevice(t *testing.T, publish func(pe.State)) (*Device, *fakeDevice) {
	t.Helper()
	fake := newFakeDevice()
	m := NewStore(pe.Range{Min: 0, Max: 80, Step: 1})
	m.ApplySnapshot(fake.status)
	if publish == nil {
		publish = func(pe.State) {}
	}
	return NewDevice(m, fake, publish), fake
}

func TestToggleMuteSendsTheInverseOfCurrentState(t *testing.T) {
	d, fake := newTestDevice(t, nil)

	if err := d.ToggleMute(context.Background()); err != nil {
		t.Fatalf("ToggleMute returned error: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.muteSetTo == nil {
		t.Fatal("SetMute was never called")
	}
	// Current state is unmuted, so a toggle must ask for mute on. Unlike
	// volume, the device offers no toggle for mute, so it is computed here —
	// acceptable because mute is binary and self-corrects on the next event.
	if !*fake.muteSetTo {
		t.Error("SetMute(false) on a toggle from unmuted; want SetMute(true)")
	}
}

func TestAdjustVolumeLeavesStateAlreadyUpdated(t *testing.T) {
	d, _ := newTestDevice(t, nil)

	if err := d.AdjustVolume(context.Background(), 3); err != nil {
		t.Fatalf("AdjustVolume returned error: %v", err)
	}

	// The command handler answers with State() the instant this returns. If
	// it still reads 45, every caller is told its write did nothing — and a
	// client crediting that against its own prediction shows the volume
	// jumping backwards, which is the exact failure the tracker exists to
	// prevent. Waiting for the UDP event is not an option: events are lossy.
	if got := d.State().Volume; got != 48 {
		t.Errorf("State().Volume = %d immediately after AdjustVolume, want 48", got)
	}
}

func TestSetInputLeavesStateAlreadyUpdated(t *testing.T) {
	d, _ := newTestDevice(t, nil)

	if err := d.SetInput(context.Background(), "spotify"); err != nil {
		t.Fatalf("SetInput returned error: %v", err)
	}

	if got := d.State().Input; got != "spotify" {
		t.Errorf("State().Input = %q immediately after SetInput, want %q", got, "spotify")
	}
}

func TestReconcileDoesNotPublishUnchangedState(t *testing.T) {
	var mu sync.Mutex
	var published []pe.State
	d, _ := newTestDevice(t, func(s pe.State) {
		mu.Lock()
		defer mu.Unlock()
		published = append(published, s)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go d.Run(ctx, make(chan musiccast.Event), 15*time.Millisecond)

	// Let several polls happen against a receiver that is not changing.
	time.Sleep(120 * time.Millisecond)
	cancel()

	mu.Lock()
	defer mu.Unlock()
	// Nothing changed, so repeated polls must not wake every SSE subscriber.
	if len(published) > 1 {
		t.Errorf("published %d times for an unchanging receiver, want at most 1", len(published))
	}
}

// unreachableDevice fails every call until it is switched on, standing in for
// a receiver in deep network standby.
type unreachableDevice struct {
	mu     sync.Mutex
	awake  bool
	status musiccast.Status
}

var errUnreachable = errors.New("no route to host")

func (u *unreachableDevice) wake() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.awake = true
}

func (u *unreachableDevice) GetStatus(context.Context) (musiccast.Status, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.awake {
		return musiccast.Status{}, errUnreachable
	}
	return u.status, nil
}

func (u *unreachableDevice) SetVolumeRelative(context.Context, int) error { return errUnreachable }
func (u *unreachableDevice) SetInput(context.Context, string) error       { return errUnreachable }
func (u *unreachableDevice) SetPower(context.Context, bool) error         { return errUnreachable }
func (u *unreachableDevice) TogglePower(context.Context) error            { return errUnreachable }
func (u *unreachableDevice) SetMute(context.Context, bool) error          { return errUnreachable }
func (u *unreachableDevice) RecallPreset(context.Context, int) error      { return errUnreachable }

func (u *unreachableDevice) GetPlayInfo(context.Context) (musiccast.PlayInfo, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.awake {
		return musiccast.PlayInfo{}, errUnreachable
	}
	return musiccast.PlayInfo{}, nil
}

func (u *unreachableDevice) GetPresetInfo(context.Context) ([]pe.Preset, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.awake {
		return nil, errUnreachable
	}
	return nil, nil
}

func TestRunSurvivesAnUnreachableReceiverAndConvergesLater(t *testing.T) {
	fake := &unreachableDevice{status: musiccast.Status{
		Power: "on", Volume: 45, Input: "phono",
	}}
	m := NewStore(pe.Range{Min: 0, Max: 80, Step: 1})
	d := NewDevice(m, fake, func(pe.State) {})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		d.Run(ctx, make(chan musiccast.Event), 15*time.Millisecond)
		close(done)
	}()

	// The node boots while the stereo is asleep. The daemon must stay up and
	// keep serving rather than exiting — systemd restarting it in a loop
	// against a receiver that is off for the night helps nobody.
	time.Sleep(60 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("Run exited because the receiver was unreachable")
	default:
	}

	if d.State().Online {
		t.Error("State().Online = true while the receiver is unreachable")
	}

	fake.wake()

	waitFor(t, func() bool {
		s := d.State()
		return s.Online && s.Volume == 45
	}, "state to converge once the receiver wakes")
}

func TestRunAppliesEventsAndPublishes(t *testing.T) {
	var mu sync.Mutex
	var published []pe.State
	d, _ := newTestDevice(t, func(s pe.State) {
		mu.Lock()
		defer mu.Unlock()
		published = append(published, s)
	})

	events := make(chan musiccast.Event, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go d.Run(ctx, events, time.Hour) // long poll: this test is about events

	vol := 46
	events <- musiccast.Event{Volume: &vol}

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(published) > 0 && published[len(published)-1].Volume == 46
	}, "a published state with volume 46")

	if got := d.State().Volume; got != 46 {
		t.Errorf("State().Volume = %d, want 46", got)
	}
}

func TestStatsCountUdpEventsSeparatelyFromPolls(t *testing.T) {
	d, _ := newTestDevice(t, nil)

	events := make(chan musiccast.Event, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go d.Run(ctx, events, 20*time.Millisecond)

	vol := 46
	events <- musiccast.Event{Volume: &vol}
	vol2 := 47
	events <- musiccast.Event{Volume: &vol2}

	waitFor(t, func() bool { return d.Stats().EventsReceived >= 2 }, "two UDP events to be counted")

	// Counting events apart from polls is the whole point: if the UDP
	// subscription silently lapses, state still converges via polling and
	// everything looks healthy. A zero event count next to a rising poll
	// count is the only visible symptom.
	s := d.Stats()
	if s.EventsReceived < 2 {
		t.Errorf("EventsReceived = %d, want at least 2", s.EventsReceived)
	}
	if s.LastEventAt.IsZero() {
		t.Error("LastEventAt is zero after receiving events")
	}

	waitFor(t, func() bool { return d.Stats().Polls >= 2 }, "polls to be counted")
}

func TestStatsRecordPollFailures(t *testing.T) {
	fake := &unreachableDevice{}
	m := NewStore(pe.Range{Min: 0, Max: 80, Step: 1})
	d := NewDevice(m, fake, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go d.Run(ctx, make(chan musiccast.Event), 15*time.Millisecond)

	waitFor(t, func() bool { return d.Stats().PollErrors >= 2 }, "poll failures to be recorded")

	if got := d.Stats().LastError; got == "" {
		t.Error("LastError is empty after repeated poll failures")
	}
}

func TestRunPollsToReconcile(t *testing.T) {
	d, fake := newTestDevice(t, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go d.Run(ctx, make(chan musiccast.Event), 20*time.Millisecond)

	// Polling is what makes a lost UDP datagram or a lapsed subscription a
	// freshness problem rather than a correctness one.
	waitFor(t, func() bool { return fake.pollCount() >= 2 }, "at least two reconcile polls")
}

func TestRunStopsOnContextCancel(t *testing.T) {
	d, _ := newTestDevice(t, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		d.Run(ctx, make(chan musiccast.Event), time.Hour)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// GetVolumeRange for the test fakes.
func (f *fakeDevice) GetVolumeRange(context.Context) (pe.Range, error) {
	return pe.Range{Min: 0, Max: 80, Step: 1}, nil
}

func (u *unreachableDevice) GetVolumeRange(context.Context) (pe.Range, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.awake {
		return pe.Range{}, errUnreachable
	}
	return pe.Range{Min: 0, Max: 80, Step: 1}, nil
}

func TestReconcileFoldsInPlayInfoForTheSelectedInput(t *testing.T) {
	d, fake := newTestDevice(t, nil)
	fake.status.Input = "siriusxm"
	fake.playInfo = musiccast.PlayInfo{
		Input:  "siriusxm",
		Artist: "National",
		Album:  "35 : SiriusXMU / Indie & beyond",
		Track:  "Mistaken For Strangers",
	}

	d.reconcile(context.Background())

	got := d.State().Playing
	if got.Track != "Mistaken For Strangers" || got.Artist != "National" {
		t.Errorf("Playing = %+v, want the track the source reported", got)
	}
}

func TestReconcileDropsPlayInfoDescribingAnotherInput(t *testing.T) {
	d, fake := newTestDevice(t, nil)

	// The turntable is playing, but netusb still answers with the SiriusXM
	// session from earlier. Reporting that would tell the user their record is
	// a song on a channel they are not listening to.
	fake.status.Input = "phono"
	fake.playInfo = musiccast.PlayInfo{
		Input:  "siriusxm",
		Artist: "National",
		Track:  "Mistaken For Strangers",
	}

	d.reconcile(context.Background())

	if got := d.State().Playing; got != (pe.Playing{}) {
		t.Errorf("Playing = %+v while on phono, want it empty", got)
	}
}

func TestPlayInfoFailureLeavesTheReceiverOnline(t *testing.T) {
	d, fake := newTestDevice(t, nil)
	fake.playInfoErr = errors.New("netusb busy")

	d.reconcile(context.Background())

	// getStatus answered, which is what proves the receiver is reachable. A
	// failed second call about what is playing must not contradict it.
	if !d.State().Online {
		t.Error("State().Online = false after a play-info failure; getStatus succeeded")
	}
}

func TestPlayInfoUpdatedEventFetchesTheDetail(t *testing.T) {
	d, fake := newTestDevice(t, nil)
	fake.status.Input = "siriusxm"
	fake.playInfo = musiccast.PlayInfo{
		Input: "siriusxm", Track: "Bloodbuzz Ohio", Artist: "The National",
	}

	events := make(chan musiccast.Event)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A long poll interval, so anything that arrives came from the event
	// rather than from the loop coming round again.
	go d.Run(ctx, events, time.Hour)

	events <- musiccast.Event{Input: strPtr("siriusxm"), PlayInfoUpdated: true}

	waitFor(t, func() bool {
		return d.State().Playing.Track == "Bloodbuzz Ohio"
	}, "state never picked up the pushed play info")

	if fake.playInfoCallCount() == 0 {
		t.Error("the push never triggered a getPlayInfo")
	}
}

func TestRecallPresetLeavesStateAlreadyUpdated(t *testing.T) {
	d, fake := newTestDevice(t, nil)

	if err := d.RecallPreset(context.Background(), 2); err != nil {
		t.Fatalf("RecallPreset returned error: %v", err)
	}

	if fake.recalled != 2 {
		t.Errorf("recalled preset %d, want 2", fake.recalled)
	}
	// A recall switches the input too, and the command handler answers from
	// State the moment this returns.
	if got := d.State().Input; got != "siriusxm" {
		t.Errorf("State().Input = %q after a recall, want %q", got, "siriusxm")
	}
}

func TestPresetsPassThroughToTheReceiver(t *testing.T) {
	d, fake := newTestDevice(t, nil)
	fake.presets = []pe.Preset{
		{Num: 1, Input: "siriusxm", Text: "36 : Alt Nation / New Alternative Rock"},
	}

	got, err := d.Presets(context.Background())
	if err != nil {
		t.Fatalf("Presets returned error: %v", err)
	}
	if len(got) != 1 || got[0].Num != 1 {
		t.Errorf("Presets = %+v, want the receiver's list verbatim", got)
	}
}
