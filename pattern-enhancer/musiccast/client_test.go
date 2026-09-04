package musiccast

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// realStatusJSON is the office R-N303's actual reply to main/getStatus.
const realStatusJSON = `{"response_code":0,"power":"on","sleep":0,"volume":45,` +
	`"mute":false,"max_volume":80,"input":"phono","distribution_enable":true,` +
	`"tone_control":{"mode":"manual","bass":4,"treble":2},"balance":-1,` +
	`"link_control":"standard","link_audio_delay":"audio_sync_on",` +
	`"link_audio_quality":"uncompressed","disable_flags":0}`

// recorder captures the requests a Client makes and replies with canned JSON.
type recorder struct {
	srv      *httptest.Server
	requests []*http.Request
	reply    string
}

func newRecorder(t *testing.T, reply string) *recorder {
	t.Helper()
	r := &recorder{reply: reply}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.requests = append(r.requests, req.Clone(req.Context()))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(r.reply))
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *recorder) client() *Client {
	host := strings.TrimPrefix(r.srv.URL, "http://")
	return NewClient(host, 41100)
}

func (r *recorder) lastQuery(t *testing.T) url.Values {
	t.Helper()
	if len(r.requests) == 0 {
		t.Fatal("no request was made")
	}
	return r.requests[len(r.requests)-1].URL.Query()
}

func TestGetStatusDecodesRealPayload(t *testing.T) {
	rec := newRecorder(t, realStatusJSON)

	got, err := rec.client().GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus returned error: %v", err)
	}

	if got.Volume != 45 {
		t.Errorf("Volume = %d, want 45", got.Volume)
	}
	if got.Input != "phono" {
		t.Errorf("Input = %q, want %q", got.Input, "phono")
	}
	if got.Power != "on" {
		t.Errorf("Power = %q, want %q", got.Power, "on")
	}
	if got.MaxVolume != 80 {
		t.Errorf("MaxVolume = %d, want 80", got.MaxVolume)
	}
}

func TestSetVolumeRelativeSendsUpWithStep(t *testing.T) {
	rec := newRecorder(t, `{"response_code":0}`)

	if err := rec.client().SetVolumeRelative(context.Background(), 7); err != nil {
		t.Fatalf("SetVolumeRelative returned error: %v", err)
	}

	q := rec.lastQuery(t)
	if q.Get("volume") != "up" {
		t.Errorf("volume = %q, want %q", q.Get("volume"), "up")
	}
	if q.Get("step") != "7" {
		t.Errorf("step = %q, want %q", q.Get("step"), "7")
	}
}

func TestSetVolumeRelativeSendsDownWithPositiveStep(t *testing.T) {
	rec := newRecorder(t, `{"response_code":0}`)

	if err := rec.client().SetVolumeRelative(context.Background(), -3); err != nil {
		t.Fatalf("SetVolumeRelative returned error: %v", err)
	}

	q := rec.lastQuery(t)
	if q.Get("volume") != "down" {
		t.Errorf("volume = %q, want %q", q.Get("volume"), "down")
	}
	// The device takes a direction plus a magnitude; a negative step is not
	// something it understands.
	if q.Get("step") != "3" {
		t.Errorf("step = %q, want %q", q.Get("step"), "3")
	}
}

func TestSetVolumeRelativeZeroMakesNoRequest(t *testing.T) {
	rec := newRecorder(t, `{"response_code":0}`)

	if err := rec.client().SetVolumeRelative(context.Background(), 0); err != nil {
		t.Fatalf("SetVolumeRelative returned error: %v", err)
	}

	// A coalescing window that nets out to zero must not generate traffic.
	if len(rec.requests) != 0 {
		t.Errorf("made %d requests for a zero-step change, want 0", len(rec.requests))
	}
}

func TestNonZeroResponseCodeIsAnError(t *testing.T) {
	rec := newRecorder(t, `{"response_code":5}`)

	err := rec.client().SetVolumeRelative(context.Background(), 1)
	if err == nil {
		t.Fatal("expected an error for response_code 5, got nil")
	}
	if !strings.Contains(err.Error(), "5") {
		t.Errorf("error %q does not mention the response code", err.Error())
	}
}

func TestEveryRequestCarriesSubscriptionHeaders(t *testing.T) {
	rec := newRecorder(t, realStatusJSON)

	if _, err := rec.client().GetStatus(context.Background()); err != nil {
		t.Fatalf("GetStatus returned error: %v", err)
	}

	// The UDP subscription is renewed by these headers riding along on
	// ordinary requests. If they ever stop being sent, event push dies
	// silently ten minutes later — so this is asserted on every request,
	// not just on a dedicated subscribe call.
	req := rec.requests[0]
	if got := req.Header.Get("X-AppPort"); got != "41100" {
		t.Errorf("X-AppPort = %q, want %q", got, "41100")
	}
	if got := req.Header.Get("X-AppName"); !strings.Contains(got, "MusicCast") {
		t.Errorf("X-AppName = %q, want it to contain %q", got, "MusicCast")
	}
}
