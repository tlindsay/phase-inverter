package musiccast

import "testing"

// Payload captured verbatim from the office R-N303 (device 00A0DE000000) while
// nudging the volume via setVolume. Events are partial: the device sends only
// what changed, keyed by zone, so every field has to be optional.
const realVolumeEvent = `{"main":{"volume":46},"device_id":"00A0DE000000"}`

func TestParseEventReadsVolumeFromRealPayload(t *testing.T) {
	ev, err := ParseEvent([]byte(realVolumeEvent))
	if err != nil {
		t.Fatalf("ParseEvent returned error: %v", err)
	}

	if ev.DeviceID != "00A0DE000000" {
		t.Errorf("DeviceID = %q, want %q", ev.DeviceID, "00A0DE000000")
	}
	if ev.Volume == nil {
		t.Fatal("Volume is nil, want a value of 46")
	}
	if *ev.Volume != 46 {
		t.Errorf("Volume = %d, want 46", *ev.Volume)
	}
}

func TestParseEventKeepsRawPayloadForDiagnostics(t *testing.T) {
	// The receiver pushes plenty this daemon does not model — netusb
	// play-info updates arrive constantly while streaming. Keeping the raw
	// payload is what lets an operator tell "the subscription is dead" apart
	// from "the subscription is fine and this datagram is about something
	// else", which are indistinguishable from a parsed-to-nothing struct.
	const netusbEvent = `{"netusb":{"play_info_updated":true},"device_id":"00A0DE000000"}`

	ev, err := ParseEvent([]byte(netusbEvent))
	if err != nil {
		t.Fatalf("ParseEvent returned error: %v", err)
	}

	if ev.Volume != nil || ev.Mute != nil || ev.Power != nil || ev.Input != nil {
		t.Error("a netusb-only event must not set any main-zone field")
	}
	if string(ev.Raw) != netusbEvent {
		t.Errorf("Raw = %q, want the original payload", string(ev.Raw))
	}
}

func TestParseEventLeavesUnchangedFieldsNil(t *testing.T) {
	ev, err := ParseEvent([]byte(realVolumeEvent))
	if err != nil {
		t.Fatalf("ParseEvent returned error: %v", err)
	}

	// A volume-only event must not imply anything about the other fields.
	// Treating "absent" as "zero" is what makes a merge clobber good state.
	if ev.Mute != nil {
		t.Errorf("Mute = %v, want nil for a volume-only event", *ev.Mute)
	}
	if ev.Power != nil {
		t.Errorf("Power = %v, want nil for a volume-only event", *ev.Power)
	}
	if ev.Input != nil {
		t.Errorf("Input = %v, want nil for a volume-only event", *ev.Input)
	}
}

func TestParseEventFlagsPlayInfoUpdates(t *testing.T) {
	// Captured verbatim from the office R-N303 when the station changed.
	const netusbEvent = `{"netusb":{"play_info_updated":true},"device_id":"00A0DE000000"}`

	ev, err := ParseEvent([]byte(netusbEvent))
	if err != nil {
		t.Fatalf("ParseEvent returned error: %v", err)
	}

	if !ev.PlayInfoUpdated {
		t.Error("PlayInfoUpdated = false for a netusb play-info push")
	}
	// The push says only that something changed; the zone fields stay untouched
	// because the datagram carries nothing about them.
	if ev.Volume != nil || ev.Power != nil || ev.Input != nil {
		t.Error("a netusb push must not set any main-zone field")
	}
}

func TestParseEventLeavesPlayInfoUnflaggedForZoneEvents(t *testing.T) {
	ev, err := ParseEvent([]byte(realVolumeEvent))
	if err != nil {
		t.Fatalf("ParseEvent returned error: %v", err)
	}

	if ev.PlayInfoUpdated {
		t.Error("PlayInfoUpdated = true for a volume event")
	}
}
