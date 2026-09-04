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
	log.Debug("reconcile", "seq", next.Seq, "volume", next.Volume,
		"power", next.Power, "input", next.Input, "mute", next.Mute)
	d.emit(next)
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
