// Package patternenhancer holds the wire contract shared by the pattern-enhancer
// daemon and the clients that drive it, plus the client itself.
//
// Both ends of the connection marshal these same types, so the contract has
// exactly one definition and cannot drift between them.
package patternenhancer

// Power is the receiver's power state. The MusicCast API spells "off" as
// "standby", and this mirrors the device rather than inventing a synonym.
type Power string

const (
	PowerOn      Power = "on"
	PowerStandby Power = "standby"
)

// Range is the volume range the device advertises via getFeatures. It travels
// with every State so clients can size their own steps without hardcoding a
// scale — the R-N303 is 0-80, but nothing here assumes that.
type Range struct {
	Min  int `json:"min"`
	Max  int `json:"max"`
	Step int `json:"step"`
}

// Playing is what the receiver's current network source is playing.
//
// Only the netusb inputs report this — SiriusXM, Spotify, net radio — so it is
// zero for phono and the line inputs, and zero means "nothing to say" rather
// than "unknown". The daemon clears it rather than letting a stale track linger
// from whatever was streaming before someone dropped the needle on a record.
//
// Comparable on purpose, like every other part of State: adding a slice or a
// map here would break the equality check that decides whether anything
// actually changed.
type Playing struct {
	Artist string `json:"artist,omitempty"`
	Album  string `json:"album,omitempty"`
	Track  string `json:"track,omitempty"`
}

// Preset is one station stored on the receiver.
//
// Num is the slot to recall, counted from one. Text is the receiver's own label
// for it — for SiriusXM that is the channel number, name and description in a
// single string ("35 : SiriusXMU / Indie & Beyond"), which is what a station
// menu should display rather than anything this system invents.
//
// Presets live outside State: the list is long, changes only when someone saves
// a preset, and is not something a client needs pushed to it.
type Preset struct {
	Num   int    `json:"num"`
	Input string `json:"input"`
	Text  string `json:"text"`
}

// State is an authoritative snapshot of one receiver, as the daemon sees it.
//
// Seq increases on every real change and never resets while the daemon runs.
// It exists because commands and the event stream arrive on separate
// connections: when both describe the same change, the higher Seq wins and the
// lower is discarded, which is the ordering guarantee a single socket would
// have given for free.
type State struct {
	// Epoch identifies the daemon instance that produced this state.
	//
	// Seq is only meaningful within one daemon lifetime — it restarts at zero
	// every time the process does. Without a way to notice that, a client
	// holding Seq 9 from a previous instance discards everything a restarted
	// daemon sends (1, 2, 3, all "older"), and its display freezes forever on
	// a value the receiver left behind. Epoch changing means "start over,
	// trust this".
	Epoch string `json:"epoch"`

	Seq    uint64 `json:"seq"`
	Power  Power  `json:"power"`
	Volume int    `json:"volume"`
	Mute   bool   `json:"mute"`
	Input  string `json:"input"`
	Range  Range  `json:"range"`

	// Playing is what the current source is playing, when it is a source that
	// reports such a thing. See Playing.
	Playing Playing `json:"playing"`

	// Online reports whether the daemon can currently reach the receiver.
	//
	// This is distinct from Power. A receiver in deep network standby answers
	// nothing at all — the R-N303 keeps its network stack alive for a while
	// after going to standby and then drops off entirely — so "off" and
	// "unreachable" are genuinely different states, and a UI that conflates
	// them will confidently show a stale volume for a receiver that has been
	// unplugged for a week.
	Online bool `json:"online"`
}

// HasRange reports whether the device's volume scale is known yet. It is false
// before the daemon has managed to reach the receiver even once.
func (s State) HasRange() bool { return s.Range.Max > s.Range.Min }
