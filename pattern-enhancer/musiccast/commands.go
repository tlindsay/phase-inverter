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

// RecallPreset selects a stored netusb preset — a SiriusXM channel, a net radio
// station, whatever was saved in that slot.
//
// Presets are the only station selection this daemon offers, deliberately. The
// alternative is walking the SiriusXM menu with getListInfo and setListControl,
// which is stateful, shared by every client of the receiver, and reorders
// underneath you as the service rearranges its categories. A recall is one
// stateless call that also switches the input, so it lands the same way from
// any starting point.
func (c *Client) RecallPreset(ctx context.Context, num int) error {
	return c.get(ctx, "/netusb/recallPreset", url.Values{
		"zone": {"main"},
		"num":  {strconv.Itoa(num)},
	}, nil)
}

// GetPlayInfo reads what the current network source is playing.
func (c *Client) GetPlayInfo(ctx context.Context) (PlayInfo, error) {
	var p PlayInfo
	err := c.get(ctx, "/netusb/getPlayInfo", nil, &p)
	return p, err
}

// GetPresetInfo lists the stored presets, numbered from one.
//
// Empty slots are dropped rather than reported: the R-N303 answers with all
// forty regardless, the unused ones carrying input "unknown" and no text, and
// a client rendering a station menu wants the seven that exist rather than
// thirty-three blanks it has to filter itself.
//
// The slot number is the array position, which is why the empties are filtered
// here rather than by the caller — the position is only knowable before the
// list is compacted.
func (c *Client) GetPresetInfo(ctx context.Context) ([]pe.Preset, error) {
	var reply struct {
		PresetInfo []struct {
			Input string `json:"input"`
			Text  string `json:"text"`
		} `json:"preset_info"`
	}
	if err := c.get(ctx, "/netusb/getPresetInfo", nil, &reply); err != nil {
		return nil, err
	}

	presets := make([]pe.Preset, 0, len(reply.PresetInfo))
	for i, p := range reply.PresetInfo {
		if p.Input == "" || p.Input == "unknown" {
			continue
		}
		presets = append(presets, pe.Preset{Num: i + 1, Input: p.Input, Text: p.Text})
	}
	return presets, nil
}
