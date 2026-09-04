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
