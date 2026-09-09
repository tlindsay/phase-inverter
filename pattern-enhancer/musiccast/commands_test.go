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

// realPresetInfoJSON is the office R-N303's actual reply to
// netusb/getPresetInfo, trimmed to the first stored slots and one empty one.
// The empties are the point: the device always answers with all forty.
const realPresetInfoJSON = `{"response_code":0,"preset_info":[
  {"input":"siriusxm","text":"36 : Alt Nation / New Alternative Rock","attribute":0},
  {"input":"siriusxm","text":"35 : SiriusXMU / Indie & Beyond","attribute":0},
  {"input":"unknown","text":""}]}`

// realPlayInfoJSON is the reply captured while SiriusXMU was playing.
const realPlayInfoJSON = `{"response_code":0,"input":"siriusxm","playback":"play",
"artist":"National","album":"35 : SiriusXMU / Indie & beyond",
"track":"Mistaken For Strangers","albumart_id":4417}`

func TestRecallPresetSendsZoneAndSlot(t *testing.T) {
	rec := newRecorder(t, `{"response_code":0}`)

	if err := rec.client().RecallPreset(context.Background(), 2); err != nil {
		t.Fatalf("RecallPreset returned error: %v", err)
	}

	q := rec.lastQuery(t)
	if got := q.Get("num"); got != "2" {
		t.Errorf("num = %q, want %q", got, "2")
	}
	// The zone is not optional: without it the receiver has no idea which
	// output to move, and answers with an error rather than a guess.
	if got := q.Get("zone"); got != "main" {
		t.Errorf("zone = %q, want %q", got, "main")
	}
}

func TestGetPresetInfoNumbersSlotsAndDropsEmpties(t *testing.T) {
	rec := newRecorder(t, realPresetInfoJSON)

	got, err := rec.client().GetPresetInfo(context.Background())
	if err != nil {
		t.Fatalf("GetPresetInfo returned error: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d presets, want the 2 that are stored", len(got))
	}
	// Numbered from one, because that is the slot recallPreset expects.
	if got[0].Num != 1 || got[1].Num != 2 {
		t.Errorf("slot numbers = %d, %d; want 1, 2", got[0].Num, got[1].Num)
	}
	if got[1].Text != "35 : SiriusXMU / Indie & Beyond" {
		t.Errorf("Text = %q, want the receiver's own label", got[1].Text)
	}
}

func TestGetPlayInfoDecodesRealPayload(t *testing.T) {
	rec := newRecorder(t, realPlayInfoJSON)

	got, err := rec.client().GetPlayInfo(context.Background())
	if err != nil {
		t.Fatalf("GetPlayInfo returned error: %v", err)
	}

	if got.Track != "Mistaken For Strangers" || got.Artist != "National" {
		t.Errorf("PlayInfo = %+v, want the captured track", got)
	}
	// Input is what lets a caller tell whether this describes the source the
	// zone is actually listening to.
	if got.Input != "siriusxm" {
		t.Errorf("Input = %q, want %q", got.Input, "siriusxm")
	}
}
