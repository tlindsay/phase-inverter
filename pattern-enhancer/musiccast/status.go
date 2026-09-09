package musiccast

// Status is the receiver's reply to main/getStatus — a complete snapshot,
// unlike Event, which carries only what changed.
//
// Field set is deliberately narrow: this daemon controls power, volume, mute
// and input, and decoding the rest (tone_control, balance, link_*) would mean
// maintaining a model of settings nothing here touches.
type Status struct {
	ResponseCode int    `json:"response_code"`
	Power        string `json:"power"`
	Volume       int    `json:"volume"`
	Mute         bool   `json:"mute"`
	Input        string `json:"input"`
	MaxVolume    int    `json:"max_volume"`
}

// PlayInfo is the receiver's reply to netusb/getPlayInfo: what the current
// network source is playing.
//
// Input is not decoration. The receiver answers this call with the last netusb
// session no matter what the zone is actually listening to, so with the
// turntable playing it still reports the SiriusXM channel from an hour ago.
// Callers must check that this describes the input that is actually selected.
type PlayInfo struct {
	Input  string `json:"input"`
	Artist string `json:"artist"`
	Album  string `json:"album"`
	Track  string `json:"track"`
}
