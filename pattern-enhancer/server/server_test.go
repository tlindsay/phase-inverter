package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	pe "github.com/tlindsay/phase-inverter/pattern-enhancer"
)

// fakeBackend stands in for the real device, recording what the handlers ask of
// it so each route can be checked without hardware.
type fakeBackend struct {
	mu         sync.Mutex
	state      pe.State
	recalled   int
	presets    []pe.Preset
	volSteps   int
	input      string
	powerOn    *bool
	powerTogd  bool
	muteTogd   bool
	failWithIt error
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{state: pe.State{
		Seq: 7, Power: pe.PowerOn, Volume: 45, Input: "phono",
		Range: pe.Range{Min: 0, Max: 80, Step: 1},
	}}
}

func (f *fakeBackend) State() pe.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

func (f *fakeBackend) AdjustVolume(_ context.Context, steps int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWithIt != nil {
		return f.failWithIt
	}
	f.volSteps += steps
	f.state.Volume += steps
	f.state.Seq++
	return nil
}

func (f *fakeBackend) SetInput(_ context.Context, in string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.input = in
	f.state.Input = in
	f.state.Seq++
	return nil
}

func (f *fakeBackend) SetPower(_ context.Context, on bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.powerOn = &on
	return nil
}

func (f *fakeBackend) TogglePower(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.powerTogd = true
	return nil
}

func (f *fakeBackend) SetMute(_ context.Context, on bool) error { return nil }

func (f *fakeBackend) ToggleMute(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.muteTogd = true
	return nil
}

// RecallPreset moves the input the way a real recall does.
func (f *fakeBackend) RecallPreset(_ context.Context, num int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWithIt != nil {
		return f.failWithIt
	}
	f.recalled = num
	f.state.Input = "siriusxm"
	f.state.Seq++
	return nil
}

func (f *fakeBackend) Presets(context.Context) ([]pe.Preset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWithIt != nil {
		return nil, f.failWithIt
	}
	return f.presets, nil
}

func testServer(t *testing.T, b Backend) (*Server, *httptest.Server) {
	t.Helper()
	s := New("office", b)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

func decodeState(t *testing.T, body []byte) pe.State {
	t.Helper()
	var st pe.State
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatalf("decoding state %q: %v", body, err)
	}
	return st
}

func TestGetStateReturnsCurrentSnapshot(t *testing.T) {
	_, ts := testServer(t, newFakeBackend())

	resp, err := http.Get(ts.URL + "/v1/devices/office/state")
	if err != nil {
		t.Fatalf("GET state: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	st := decodeState(t, body)

	if st.Volume != 45 || st.Seq != 7 || st.Input != "phono" {
		t.Errorf("state = %+v, want volume 45 seq 7 input phono", st)
	}
	if st.Range.Max != 80 {
		t.Errorf("Range.Max = %d, want 80 — clients need the advertised scale", st.Range.Max)
	}
}

func TestUnknownDeviceIs404(t *testing.T) {
	_, ts := testServer(t, newFakeBackend())

	resp, err := http.Get(ts.URL + "/v1/devices/kitchen/state")
	if err != nil {
		t.Fatalf("GET state: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestPostVolumeAppliesStepAndReturnsState(t *testing.T) {
	b := newFakeBackend()
	_, ts := testServer(t, b)

	resp, err := http.Post(ts.URL+"/v1/devices/office/volume",
		"application/json", strings.NewReader(`{"step":4}`))
	if err != nil {
		t.Fatalf("POST volume: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	b.mu.Lock()
	got := b.volSteps
	b.mu.Unlock()
	if got != 4 {
		t.Errorf("backend saw %d steps, want 4", got)
	}

	// The command response must carry authoritative post-command state, so a
	// keypress is confirmed without waiting on the event stream.
	st := decodeState(t, readBody(t, resp))
	if st.Volume != 49 {
		t.Errorf("returned Volume = %d, want 49", st.Volume)
	}
	if st.Seq != 8 {
		t.Errorf("returned Seq = %d, want 8", st.Seq)
	}
}

func TestPostPowerToggleUsesDeviceSideToggle(t *testing.T) {
	b := newFakeBackend()
	_, ts := testServer(t, b)

	resp, err := http.Post(ts.URL+"/v1/devices/office/power",
		"application/json", strings.NewReader(`{"toggle":true}`))
	if err != nil {
		t.Fatalf("POST power: %v", err)
	}
	defer resp.Body.Close()

	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.powerTogd {
		t.Error("backend did not receive a toggle")
	}
	if b.powerOn != nil {
		t.Error("backend received an explicit power value; toggle must not be computed here")
	}
}

func TestPostInputSwitchesInput(t *testing.T) {
	b := newFakeBackend()
	_, ts := testServer(t, b)

	resp, err := http.Post(ts.URL+"/v1/devices/office/input",
		"application/json", strings.NewReader(`{"input":"spotify"}`))
	if err != nil {
		t.Fatalf("POST input: %v", err)
	}
	defer resp.Body.Close()

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.input != "spotify" {
		t.Errorf("input = %q, want %q", b.input, "spotify")
	}
}

func TestPostRejectsMalformedBody(t *testing.T) {
	_, ts := testServer(t, newFakeBackend())

	resp, err := http.Post(ts.URL+"/v1/devices/office/volume",
		"application/json", strings.NewReader(`{"step":`))
	if err != nil {
		t.Fatalf("POST volume: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestEventsSendsSnapshotOnConnect(t *testing.T) {
	_, ts := testServer(t, newFakeBackend())

	resp, err := http.Get(ts.URL + "/v1/devices/office/events")
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	// Snapshot-on-connect is what makes reconnect correct: a client that just
	// woke up gets the truth immediately rather than waiting for a change.
	ev := readSSEEvent(t, resp)
	st := decodeState(t, []byte(ev.data))
	if st.Volume != 45 || st.Seq != 7 {
		t.Errorf("snapshot = %+v, want volume 45 seq 7", st)
	}
}

func TestEventsStreamsPublishedUpdates(t *testing.T) {
	s, ts := testServer(t, newFakeBackend())

	resp, err := http.Get(ts.URL + "/v1/devices/office/events")
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer resp.Body.Close()

	readSSEEvent(t, resp) // discard the connect snapshot

	s.Publish(pe.State{Seq: 8, Volume: 46, Power: pe.PowerOn, Input: "phono"})

	ev := readSSEEvent(t, resp)
	st := decodeState(t, []byte(ev.data))
	if st.Volume != 46 || st.Seq != 8 {
		t.Errorf("streamed state = %+v, want volume 46 seq 8", st)
	}
	// The SSE id carries seq so a client can resolve interleaving against
	// command responses without parsing the body first.
	if ev.id != "8" {
		t.Errorf("SSE id = %q, want %q", ev.id, "8")
	}
}

func TestHealthzReportsOK(t *testing.T) {
	_, ts := testServer(t, newFakeBackend())

	resp, err := http.Get(ts.URL + "/v1/healthz")
	if err != nil {
		t.Fatalf("GET healthz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// --- helpers ---

func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	buf := make([]byte, 0, 1024)
	tmp := make([]byte, 512)
	for {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return buf
}

type sseEvent struct{ id, data string }

// readSSEEvent reads one complete SSE frame (fields until a blank line).
func readSSEEvent(t *testing.T, resp *http.Response) sseEvent {
	t.Helper()
	type result struct {
		ev  sseEvent
		err error
	}
	done := make(chan result, 1)

	go func() {
		var ev sseEvent
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if ev.data != "" {
					done <- result{ev: ev}
					return
				}
			case strings.HasPrefix(line, "id:"):
				ev.id = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
			case strings.HasPrefix(line, "data:"):
				ev.data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
		done <- result{err: sc.Err()}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("reading SSE: %v", r.err)
		}
		return r.ev
	case <-time.After(3 * time.Second):
		t.Fatal("timed out reading an SSE event")
		return sseEvent{}
	}
}

func TestPostPresetRecallsSlotAndReturnsState(t *testing.T) {
	b := newFakeBackend()
	_, ts := testServer(t, b)

	resp, err := http.Post(ts.URL+"/v1/devices/office/preset",
		"application/json", strings.NewReader(`{"num":2}`))
	if err != nil {
		t.Fatalf("POST preset: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	b.mu.Lock()
	got := b.recalled
	b.mu.Unlock()
	if got != 2 {
		t.Errorf("backend recalled preset %d, want 2", got)
	}

	// A recall switches the input, and the response carries that rather than
	// making the client wait for the stream to say so.
	st := decodeState(t, readBody(t, resp))
	if st.Input != "siriusxm" {
		t.Errorf("returned Input = %q, want %q", st.Input, "siriusxm")
	}
}

func TestPostPresetRejectsSlotsBelowOne(t *testing.T) {
	b := newFakeBackend()
	_, ts := testServer(t, b)

	resp, err := http.Post(ts.URL+"/v1/devices/office/preset",
		"application/json", strings.NewReader(`{"num":0}`))
	if err != nil {
		t.Fatalf("POST preset: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d for preset 0, want 400", resp.StatusCode)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.recalled != 0 {
		t.Error("a rejected request still reached the receiver")
	}
}

func TestGetPresetsListsStoredStations(t *testing.T) {
	b := newFakeBackend()
	b.presets = []pe.Preset{
		{Num: 1, Input: "siriusxm", Text: "36 : Alt Nation / New Alternative Rock"},
		{Num: 2, Input: "siriusxm", Text: "35 : SiriusXMU / Indie & Beyond"},
	}
	_, ts := testServer(t, b)

	resp, err := http.Get(ts.URL + "/v1/devices/office/presets")
	if err != nil {
		t.Fatalf("GET presets: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got []pe.Preset
	if err := json.Unmarshal(readBody(t, resp), &got); err != nil {
		t.Fatalf("decoding presets: %v", err)
	}
	if len(got) != 2 || got[1].Num != 2 {
		t.Fatalf("presets = %+v, want the backend's two stations", got)
	}
	if got[0].Text != "36 : Alt Nation / New Alternative Rock" {
		t.Errorf("Text = %q, want the receiver's own label", got[0].Text)
	}
}

func TestGetPresetsAnswersWithAnArrayWhenNoneAreStored(t *testing.T) {
	_, ts := testServer(t, newFakeBackend())

	resp, err := http.Get(ts.URL + "/v1/devices/office/presets")
	if err != nil {
		t.Fatalf("GET presets: %v", err)
	}
	defer resp.Body.Close()

	// null would make every client special-case a receiver with nothing saved.
	if body := strings.TrimSpace(string(readBody(t, resp))); body != "[]" {
		t.Errorf("body = %q for an empty list, want %q", body, "[]")
	}
}
