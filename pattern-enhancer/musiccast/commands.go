package musiccast

import (
	"context"
	"errors"
	"net/url"
	"strconv"

	pe "github.com/tlindsay/phase-inverter/pattern-enhancer"
)

// SetInput switches the main zone to an input id, as advertised by getFeatures
// (for the R-N303: spotify, phono, siriusxm, airplay, and others).
func (c *Client) SetInput(ctx context.Context, input string) error {
	return c.get(ctx, "/main/setInput", url.Values{"input": {input}}, nil)
}

// SetPower drives the main zone to an explicit state.
func (c *Client) SetPower(ctx context.Context, on bool) error {
	power := "standby"
	if on {
		power = "on"
	}
	return c.get(ctx, "/main/setPower", url.Values{"power": {power}}, nil)
}

// TogglePower flips power using the device's own current value.
//
// Preferred over reading state and calling SetPower with its inverse: the
// receiver is authoritative, so this cannot invert the wrong way when the
// daemon's cache is a moment behind.
func (c *Client) TogglePower(ctx context.Context) error {
	return c.get(ctx, "/main/setPower", url.Values{"power": {"toggle"}}, nil)
}

// SetMute drives mute to an explicit state.
//
// There is no device-side toggle for mute, so callers wanting one must compute
// it from current state. That is tolerable where relative volume was not: mute
// is binary and self-corrects on the next event, whereas a mistaken volume
// target lands somewhere arbitrary and stays there.
func (c *Client) SetMute(ctx context.Context, on bool) error {
	return c.get(ctx, "/main/setMute", url.Values{
		"enable": {strconv.FormatBool(on)},
	}, nil)
}

// ErrNoVolumeRange means getFeatures did not advertise a volume scale.
var ErrNoVolumeRange = errors.New("musiccast: device advertises no volume range")

type featuresReply struct {
	Zone []struct {
		ID        string `json:"id"`
		RangeStep []struct {
			ID   string `json:"id"`
			Min  int    `json:"min"`
			Max  int    `json:"max"`
			Step int    `json:"step"`
		} `json:"range_step"`
	} `json:"zone"`
}

// GetVolumeRange reads the volume scale the device advertises.
//
// Nothing in this system hardcodes 0-80. The previous MQTT bridge normalised to
// a 0-100 percentage, which meant every round trip rounded and no caller could
// tell device units from percent. Carrying the real range end to end removes
// the ambiguity entirely.
func (c *Client) GetVolumeRange(ctx context.Context) (pe.Range, error) {
	var f featuresReply
	if err := c.get(ctx, "/system/getFeatures", nil, &f); err != nil {
		return pe.Range{}, err
	}

	for _, z := range f.Zone {
		if z.ID != "main" {
			continue
		}
		for _, rs := range z.RangeStep {
			if rs.ID == "volume" {
				return pe.Range{Min: rs.Min, Max: rs.Max, Step: rs.Step}, nil
			}
		}
	}
	return pe.Range{}, ErrNoVolumeRange
}
