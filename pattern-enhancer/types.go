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
