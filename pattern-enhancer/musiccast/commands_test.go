package musiccast

import (
	"context"
	"testing"
)

// realFeaturesFragment mirrors the shape of the office R-N303's getFeatures
// reply, trimmed to the parts this daemon reads. range_step is why volume is
// never hardcoded anywhere: the device states its own scale.
const realFeaturesFragment = `{"response_code":0,"system":{"zone_num":1},
"zone":[{"id":"main","range_step":[
  {"id":"volume","min":0,"max":80,"step":1},
  {"id":"tone_control","min":-5,"max":5,"step":1},
  {"id":"balance","min":-10,"max":10,"step":1}]}]}`

func TestSetInputSendsInputID(t *testing.T) {
	rec := newRecorder(t, `{"response_code":0}`)

	if err := rec.client().SetInput(context.Background(), "spotify"); err != nil {
		t.Fatalf("SetInput returned error: %v", err)
	}

	if got := rec.lastQuery(t).Get("input"); got != "spotify" {
		t.Errorf("input = %q, want %q", got, "spotify")
	}
}

func TestSetPowerOnSendsOn(t *testing.T) {
	rec := newRecorder(t, `{"response_code":0}`)

	if err := rec.client().SetPower(context.Background(), true); err != nil {
		t.Fatalf("SetPower returned error: %v", err)
	}

	if got := rec.lastQuery(t).Get("power"); got != "on" {
		t.Errorf("power = %q, want %q", got, "on")
	}
}

func TestSetPowerOffSendsStandby(t *testing.T) {
	rec := newRecorder(t, `{"response_code":0}`)

	if err := rec.client().SetPower(context.Background(), false); err != nil {
		t.Fatalf("SetPower returned error: %v", err)
	}

	// The device has no "off" — the API word is "standby".
	if got := rec.lastQuery(t).Get("power"); got != "standby" {
		t.Errorf("power = %q, want %q", got, "standby")
	}
}

func TestTogglePowerLetsTheDeviceDecide(t *testing.T) {
	rec := newRecorder(t, `{"response_code":0}`)

	if err := rec.client().TogglePower(context.Background()); err != nil {
		t.Fatalf("TogglePower returned error: %v", err)
	}

	// Same reasoning as relative volume: the receiver flips its own
	// authoritative value, so a stale cache here cannot invert the result.
	if got := rec.lastQuery(t).Get("power"); got != "toggle" {
		t.Errorf("power = %q, want %q", got, "toggle")
	}
}

func TestSetMuteSendsEnable(t *testing.T) {
	rec := newRecorder(t, `{"response_code":0}`)

	if err := rec.client().SetMute(context.Background(), true); err != nil {
		t.Fatalf("SetMute returned error: %v", err)
	}

	if got := rec.lastQuery(t).Get("enable"); got != "true" {
		t.Errorf("enable = %q, want %q", got, "true")
	}
}

func TestGetVolumeRangeReadsAdvertisedScale(t *testing.T) {
	rec := newRecorder(t, realFeaturesFragment)

	got, err := rec.client().GetVolumeRange(context.Background())
	if err != nil {
		t.Fatalf("GetVolumeRange returned error: %v", err)
	}

	if got.Min != 0 || got.Max != 80 || got.Step != 1 {
		t.Errorf("range = %+v, want {Min:0 Max:80 Step:1}", got)
	}
}

func TestGetVolumeRangeErrorsWhenVolumeEntryMissing(t *testing.T) {
	rec := newRecorder(t, `{"response_code":0,"zone":[{"id":"main","range_step":[
	  {"id":"balance","min":-10,"max":10,"step":1}]}]}`)

	// Silently defaulting to 0-100 here would reintroduce exactly the unit
	// confusion the old MQTT bridge had.
	if _, err := rec.client().GetVolumeRange(context.Background()); err == nil {
		t.Fatal("expected an error when no volume range is advertised, got nil")
	}
}
