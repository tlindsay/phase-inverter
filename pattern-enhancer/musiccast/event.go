package musiccast

import "encoding/json"

// Event is one UDP notification from the receiver. The device sends only the
// fields that changed, so every value is a pointer: nil means "this event says
// nothing about that field", which is distinct from "this field is now zero".
type Event struct {
	DeviceID string
	Volume   *int
	Mute     *bool
	Power    *string
	Input    *string

	// Raw is the original datagram, kept for diagnostics.
	//
	// The receiver pushes a great deal this daemon does not model — netusb
	// play-info updates stream continuously while something is playing. Those
	// parse to an Event with every field nil, which is indistinguishable from
	// a subscription that has gone quiet unless the payload is preserved.
	Raw []byte
}

// zonePayload is the per-zone object inside an event. The R-N303 has a single
// zone, "main", but the wire format nests under the zone name regardless.
type zonePayload struct {
	Volume *int    `json:"volume"`
	Mute   *bool   `json:"mute"`
	Power  *string `json:"power"`
	Input  *string `json:"input"`
}

type eventPayload struct {
	DeviceID string       `json:"device_id"`
	Main     *zonePayload `json:"main"`
}

// ParseEvent decodes a single UDP datagram from the receiver.
func ParseEvent(b []byte) (Event, error) {
	var p eventPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return Event{}, err
	}

	// Copied, not aliased: the listener reads into one reusable buffer, so
	// retaining b directly would leave Raw pointing at whatever datagram
	// happens to arrive next.
	raw := make([]byte, len(b))
	copy(raw, b)

	ev := Event{DeviceID: p.DeviceID, Raw: raw}
	if p.Main != nil {
		ev.Volume = p.Main.Volume
		ev.Mute = p.Main.Mute
		ev.Power = p.Main.Power
		ev.Input = p.Main.Input
	}
	return ev, nil
}
