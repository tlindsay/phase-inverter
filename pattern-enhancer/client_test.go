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

// stubBackend is a minimal device for the server to drive, so these tests
// exercise the real HTTP contract end to end rather than a hand-written mock
// of it. Client and server share the wire types, so a decode mismatch here is
// a genuine contract break, not a fixture drifting.
type stubBackend struct {
	mu    sync.Mutex
	state pe.State
}

var stubPresets = []pe.Preset{
	{Num: 1, Input: "siriusxm", Text: "36 : Alt Nation / New Alternative Rock"},
	{Num: 2, Input: "siriusxm", Text: "35 : SiriusXMU / Indie & Beyond"},
}

func newStub() *stubBackend {
	return &stubBackend{state: pe.State{
		Seq: 3, Power: pe.PowerOn, Volume: 45, Input: "phono",
		Range: pe.Range{Min: 0, Max: 80, Step: 1},
	}}
}

func (s *stubBackend) State() pe.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *stubBackend) AdjustVolume(_ context.Context, steps int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Volume += steps
	s.state.Seq++
	return nil
}

func (s *stubBackend) SetInput(_ context.Context, in string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Input = in
	s.state.Seq++
	return nil
}

func (s *stubBackend) SetPower(context.Context, bool) error { return nil }
func (s *stubBackend) TogglePower(context.Context) error    { return nil }
func (s *stubBackend) SetMute(context.Context, bool) error  { return nil }
func (s *stubBackend) ToggleMute(context.Context) error     { return nil }

func (s *stubBackend) RecallPreset(_ context.Context, num int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Input = "siriusxm"
	s.state.Playing = pe.Playing{Album: stubPresets[num-1].Text}
	s.state.Seq++
	return nil
}

func (s *stubBackend) Presets(context.Context) ([]pe.Preset, error) { return stubPresets, nil }

func liveServer(t *testing.T) (*server.Server, *pe.Client) {
	t.Helper()
	srv := server.New("office", newStub())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, pe.NewClient(ts.URL, "office")
}

func TestClientReadsStateOverTheRealContract(t *testing.T) {
	_, c := liveServer(t)

	st, err := c.State(context.Background())
	if err != nil {
		t.Fatalf("State returned error: %v", err)
	}

	if st.Volume != 45 || st.Input != "phono" || st.Seq != 3 {
		t.Errorf("state = %+v, want volume 45 input phono seq 3", st)
	}
	if st.Range.Max != 80 {
		t.Errorf("Range.Max = %d, want 80", st.Range.Max)
	}
}

func TestAdjustVolumeReturnsConfirmedState(t *testing.T) {
	_, c := liveServer(t)

	st, err := c.AdjustVolume(context.Background(), 4)
	if err != nil {
		t.Fatalf("AdjustVolume returned error: %v", err)
	}

	// The command's own response is the confirmation. A client that had to
	// wait for the event stream would stall on every keypress.
	if st.Volume != 49 {
		t.Errorf("Volume = %d, want 49", st.Volume)
	}
	if st.Seq != 4 {
		t.Errorf("Seq = %d, want 4", st.Seq)
	}
}

func TestSetInputReturnsConfirmedState(t *testing.T) {
	_, c := liveServer(t)

	st, err := c.SetInput(context.Background(), "spotify")
	if err != nil {
		t.Fatalf("SetInput returned error: %v", err)
	}
	if st.Input != "spotify" {
		t.Errorf("Input = %q, want %q", st.Input, "spotify")
	}
}

func TestEventsDeliversSnapshotThenUpdates(t *testing.T) {
	srv, c := liveServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	states := c.Events(ctx)

	// Snapshot on connect.
	select {
	case st := <-states:
		if st.Volume != 45 {
			t.Errorf("snapshot Volume = %d, want 45", st.Volume)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the connect snapshot")
	}

	srv.Publish(pe.State{Seq: 9, Volume: 52, Power: pe.PowerOn, Input: "phono"})

	select {
	case st := <-states:
		if st.Volume != 52 || st.Seq != 9 {
			t.Errorf("update = %+v, want volume 52 seq 9", st)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a published update")
	}
}

func TestEventsChannelClosesOnCancel(t *testing.T) {
	_, c := liveServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	states := c.Events(ctx)
	<-states // connect snapshot
	cancel()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case _, open := <-states:
			if !open {
				return // closed, as required
			}
		case <-deadline:
			t.Fatal("event channel stayed open after cancellation")
		}
	}
}

func TestRecallPresetReturnsConfirmedState(t *testing.T) {
	_, c := liveServer(t)

	st, err := c.RecallPreset(context.Background(), 2)
	if err != nil {
		t.Fatalf("RecallPreset returned error: %v", err)
	}

	if st.Input != "siriusxm" {
		t.Errorf("Input = %q, want %q", st.Input, "siriusxm")
	}
	// Playing rides along on State, so a UI knows what it landed on without a
	// second request.
	if st.Playing.Album != "35 : SiriusXMU / Indie & Beyond" {
		t.Errorf("Playing.Album = %q, want the recalled station", st.Playing.Album)
	}
}

func TestPresetsRoundTripOverTheRealContract(t *testing.T) {
	_, c := liveServer(t)

	got, err := c.Presets(context.Background())
	if err != nil {
		t.Fatalf("Presets returned error: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d presets, want 2", len(got))
	}
	if got[0].Num != 1 || got[0].Text != "36 : Alt Nation / New Alternative Rock" {
		t.Errorf("preset = %+v, want slot 1 with the receiver's label", got[0])
	}
}
