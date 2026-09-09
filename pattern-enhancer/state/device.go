package state

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"

	pe "github.com/tlindsay/phase-inverter/pattern-enhancer"
	"github.com/tlindsay/phase-inverter/pattern-enhancer/musiccast"
)

// DeviceClient is the receiver-facing API the Device needs. Declared here
// rather than reusing *musiccast.Client directly so the loop can be tested
// without hardware.
type DeviceClient interface {
	GetStatus(ctx context.Context) (musiccast.Status, error)
	GetVolumeRange(ctx context.Context) (pe.Range, error)
	GetPlayInfo(ctx context.Context) (musiccast.PlayInfo, error)
	GetPresetInfo(ctx context.Context) ([]pe.Preset, error)
	RecallPreset(ctx context.Context, num int) error
	SetVolumeRelative(ctx context.Context, steps int) error
	SetInput(ctx context.Context, input string) error
	SetPower(ctx context.Context, on bool) error
	TogglePower(ctx context.Context) error
	SetMute(ctx context.Context, on bool) error
}

// Device is one receiver as the daemon manages it: authoritative state, a
// serialised write path, and the loop that keeps the two honest.
type Device struct {
	store   *Store
	client  DeviceClient
	ctl     *Controller
	publish func(pe.State)

	mu            sync.Mutex
	lastPublished uint64
	stats         Stats
}

// Stats is what the daemon knows about its own health.
//
// EventsReceived is the important one. The UDP subscription is the only part
// of this system that can fail completely silently: if it lapses, state still
// converges through polling, so every visible symptom disappears and the only
// evidence is that pushed events stopped arriving. Separating the two counters
// makes that visible instead of invisible.
type Stats struct {
	EventsReceived uint64    `json:"events_received"`
	LastEventAt    time.Time `json:"last_event_at"`
	Polls          uint64    `json:"polls"`
	LastPollAt     time.Time `json:"last_poll_at"`
	PollErrors     uint64    `json:"poll_errors"`
	LastError      string    `json:"last_error,omitempty"`
}

// Stats returns a snapshot of daemon health counters.
func (d *Device) Stats() Stats {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stats
}

func NewDevice(s *Store, c DeviceClient, publish func(pe.State)) *Device {
	d := &Device{
		store:   s,
		client:  c,
		ctl:     NewController(s, c),
		publish: publish,
	}
	// Refresh state after each write so command handlers can answer with the
	// post-command value rather than the cache.
	d.ctl.AfterWrite = d.reconcile
	return d
}

func (d *Device) State() pe.State { return d.store.State() }

// AdjustVolume moves volume by steps, coalescing bursts. See Controller.
func (d *Device) AdjustVolume(ctx context.Context, steps int) error {
	return d.ctl.AdjustVolume(ctx, steps)
}

func (d *Device) SetInput(ctx context.Context, input string) error {
	return d.afterWrite(ctx, d.client.SetInput(ctx, input))
}

func (d *Device) SetPower(ctx context.Context, on bool) error {
	return d.afterWrite(ctx, d.client.SetPower(ctx, on))
}

func (d *Device) TogglePower(ctx context.Context) error {
	return d.afterWrite(ctx, d.client.TogglePower(ctx))
}

func (d *Device) SetMute(ctx context.Context, on bool) error {
	return d.afterWrite(ctx, d.client.SetMute(ctx, on))
}

// RecallPreset selects a stored station. The recall switches the input too, so
// this works from phono as well as from another station.
func (d *Device) RecallPreset(ctx context.Context, num int) error {
	return d.afterWrite(ctx, d.client.RecallPreset(ctx, num))
}

// Presets lists the stations stored on the receiver.
//
// Read straight through to the device rather than cached. The list changes only
// when someone saves a preset from the remote, and a cache would have to watch
// for that to avoid going stale — bookkeeping that costs more than the one
// 20ms call a client makes when it builds its menu.
func (d *Device) Presets(ctx context.Context) ([]pe.Preset, error) {
	return d.client.GetPresetInfo(ctx)
}

// afterWrite refreshes state following a successful command, so the caller can
// report the post-command value instead of the pre-command cache.
//
// The receiver answers getStatus in about 20ms, so this is cheap next to the
// alternative of waiting on a UDP event that may never arrive.
func (d *Device) afterWrite(ctx context.Context, err error) error {
	if err != nil {
		return err
	}
	d.reconcile(ctx)
	return nil
}

// ToggleMute inverts the cached mute value.
//
// The MusicCast API has no mute toggle, so unlike power and volume this cannot
// be delegated to the receiver. That is tolerable: mute is binary, so a wrong
// guess is immediately visible and undone by pressing the key again, whereas a
// wrong volume target lands somewhere arbitrary and stays.
func (d *Device) ToggleMute(ctx context.Context) error {
	return d.afterWrite(ctx, d.client.SetMute(ctx, !d.store.State().Mute))
}

// Run keeps state fresh until ctx is cancelled.
//
// Two overlapping sources, because neither alone is sufficient. Pushed UDP
// events are immediate but lossy — plain datagrams, and the subscription lapses
// after ten minutes if nothing renews it. The poll is slow but total. Together,
// losing an event costs freshness until the next poll, never correctness.
func (d *Device) Run(ctx context.Context, events <-chan musiccast.Event, pollInterval time.Duration) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	// Poll once immediately so the daemon starts from truth rather than from
	// whatever it was constructed with.
	d.reconcile(ctx)

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return
			}
			d.mu.Lock()
			d.stats.EventsReceived++
			d.stats.LastEventAt = time.Now()
			d.mu.Unlock()

			next := d.store.ApplyEvent(ev)
			// The datagram says only that the source changed what it is
			// playing, never what it changed to, so the detail has to be
			// fetched. Worth the round trip: without it a station change made
			// on the remote would not show up until the next poll.
			if ev.PlayInfoUpdated {
				next = d.refreshPlaying(ctx, next.Input, next)
			}
			// Debug rather than info: a busy evening of knob-turning would
			// otherwise fill the journal.
			log.Debug("udp event", "event", describeEvent(ev), "seq", next.Seq,
				"volume", next.Volume, "power", next.Power, "input", next.Input)
			d.emit(next)

		case <-ticker.C:
			d.reconcile(ctx)

		case <-ctx.Done():
			return
		}
	}
}

// reconcile pulls a complete snapshot and folds it in.
//
// Errors are recorded rather than raised: an unreachable receiver is ordinary
// here, not exceptional. The R-N303 keeps its network stack up for a while
// after going to standby and then drops off the network entirely, so any node
// that reboots overnight will find nothing at the other end. The daemon must
// keep serving through that and converge whenever the receiver comes back.
func (d *Device) reconcile(ctx context.Context) {
	// The volume scale is read once, but the first attempt may well happen
	// while the receiver is asleep, so keep trying until it answers.
	if !d.store.State().HasRange() {
		if r, err := d.client.GetVolumeRange(ctx); err == nil {
			d.emit(d.store.SetRange(r))
		}
	}

	status, err := d.client.GetStatus(ctx)

	d.mu.Lock()
	d.stats.Polls++
	d.stats.LastPollAt = time.Now()
	if err != nil {
		d.stats.PollErrors++
		d.stats.LastError = err.Error()
	} else {
		d.stats.LastError = ""
	}
	d.mu.Unlock()

	if err != nil {
		log.Debug("reconcile failed", "err", err)
		d.emit(d.store.SetOnline(false))
		return
	}

	next := d.store.ApplySnapshot(status)
	next = d.refreshPlaying(ctx, status.Input, next)
	log.Debug("reconcile", "seq", next.Seq, "volume", next.Volume,
		"power", next.Power, "input", next.Input, "mute", next.Mute,
		"track", next.Playing.Track)
	d.emit(next)
}

// refreshPlaying folds in what the current source is playing, returning the
// state to publish — current if the fetch failed, updated if it worked.
//
// A failure here must not mark the receiver offline. getStatus succeeding is
// what proves the receiver is reachable; this is a second call about a detail,
// and letting it override that would report the stereo as gone every time a
// netusb query happened to fail.
func (d *Device) refreshPlaying(ctx context.Context, input string, current pe.State) pe.State {
	info, err := d.client.GetPlayInfo(ctx)
	if err != nil {
		log.Debug("play info unavailable", "err", err)
		return current
	}
	return d.store.ApplyPlaying(playingFor(input, info))
}

// playingFor discards play info describing some other source.
//
// getPlayInfo answers with the last netusb session regardless of what the zone
// is listening to now, so while a record is playing it still reports the
// SiriusXM channel from an hour ago. Only info whose own input matches the
// selected one is true, and everything else is better shown as nothing at all
// than as a confident lie about what is coming out of the speakers.
func playingFor(input string, info musiccast.PlayInfo) pe.Playing {
	if info.Input != input {
		return pe.Playing{}
	}
	return pe.Playing{
		Artist: info.Artist,
		Album:  info.Album,
		Track:  info.Track,
	}
}

// describeEvent renders only the fields an event actually carried, so the log
// shows what the receiver reported rather than a struct full of zeroes.
func describeEvent(ev musiccast.Event) string {
	var parts []string
	if ev.Volume != nil {
		parts = append(parts, fmt.Sprintf("volume=%d", *ev.Volume))
	}
	if ev.Mute != nil {
		parts = append(parts, fmt.Sprintf("mute=%v", *ev.Mute))
	}
	if ev.Power != nil {
		parts = append(parts, "power="+*ev.Power)
	}
	if ev.Input != nil {
		parts = append(parts, "input="+*ev.Input)
	}
	if ev.PlayInfoUpdated {
		parts = append(parts, "play_info_updated")
	}
	if len(parts) == 0 {
		// Nothing this daemon models — almost always a netusb play-info
		// update. Show the payload so it reads as "a datagram about something
		// else" rather than as a broken subscription.
		raw := string(ev.Raw)
		if len(raw) > 160 {
			raw = raw[:160] + "…"
		}
		return "ignored " + raw
	}
	return strings.Join(parts, " ")
}

// emit publishes state, but only when it is genuinely new.
//
// Store holds Seq steady when nothing changed, so an idle receiver being
// polled every thirty seconds would otherwise wake every SSE subscriber
// forever for no reason.
func (d *Device) emit(s pe.State) {
	if d.publish == nil {
		return
	}

	d.mu.Lock()
	if s.Seq == d.lastPublished {
		d.mu.Unlock()
		return
	}
	d.lastPublished = s.Seq
	d.mu.Unlock()

	d.publish(s)
}
