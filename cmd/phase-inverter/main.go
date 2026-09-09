package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/adrg/xdg"
	"github.com/charmbracelet/log"
	"github.com/getlantern/systray"
	"github.com/muesli/termenv"
	hk "golang.design/x/hotkey"
	"golang.design/x/hotkey/mainthread"

	"github.com/tlindsay/phase-inverter/commbadge"
	pe "github.com/tlindsay/phase-inverter/pattern-enhancer"
)

//go:embed assets/*
var fs embed.FS

// Transmission is a thing the user asked for, decoupled from how it is
// delivered. The hotkey table and the menu both produce these.
type Transmission int

const (
	TransmissionVolDown Transmission = iota
	TransmissionVolDownFine
	TransmissionVolUp
	TransmissionVolUpFine
	TransmissionToggleMute
	TransmissionTogglePower
	TransmissionPowerOn

	TransmissionChangeInputSpotify
	TransmissionChangeInputPhono
	TransmissionChangeInputSirius
	TransmissionChangeInputAirplay
)

// MusicCast input ids, as the receiver advertises them via getFeatures. These
// are the wire values, not display labels.
const (
	InputSpotify = "spotify"
	InputPhono   = "phono"
	InputSirius  = "siriusxm"
	InputAirplay = "airplay"
)

// inputLabels are what the menu shows for each input id.
var inputLabels = map[string]string{
	InputSpotify: "Spotify",
	InputPhono:   "Phono",
	InputSirius:  "SiriusXM",
	InputAirplay: "AirPlay",
}

// menuOrder fixes the order inputs appear in, since map iteration does not.
var menuOrder = []string{InputSpotify, InputPhono, InputSirius, InputAirplay}

// Volume steps are fractions of whatever range the daemon reports, not
// absolute numbers. The R-N303 runs 0-80, so coarse lands on 4 and fine on 2 —
// but nothing here depends on that, and a different receiver would scale.
const (
	coarseFraction = 0.05
	fineFraction   = 0.025
)

// Station menu slots are created empty and filled once the daemon answers.
//
// systray can only append items, so anything added after the menu is built
// lands below Quit. The stations are therefore reserved up front and stay
// hidden until there is something to put in them — which is not at launch,
// because the daemon is routinely unreachable then. Forty is what the receiver
// stores, so no real preset can outrun the slots.
const stationSlots = 40

// stationRetryDelay paces retries of the station list. Slow: this is a menu
// that fills in, not a control path anyone is waiting on.
const stationRetryDelay = 5 * time.Second

type PhaseInverter struct {
	commbadge *commbadge.CommBadge
	Log       *log.Logger
	Conduit   *pe.Conduit
	Keymap    map[Transmission]*hk.Hotkey
	Menu      map[string]*systray.MenuItem
	Stations  []*systray.MenuItem
	wg        sync.WaitGroup

	// stationNums maps a station slot to the preset it currently shows. Guarded
	// because it is written by the goroutine that loads the list and read by
	// the one handling clicks.
	stationMu   sync.Mutex
	stationNums []int
}

func main() {
	daemon := flag.String("daemon", "http://pattern-enhancer",
		"base URL of the pattern-enhancer daemon on the tailnet")
	device := flag.String("device", "office", "device id to control")
	flag.Parse()

	logPath, err := xdg.DataFile("Phase Inverter/phase_inverter.log")
	if err != nil {
		log.Fatalf("failed to create log file: %v", err)
	}
	log.Infof("Logging to file %s...", logPath)

	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		log.Fatalf("failed to open log file: %v", err)
	}
	logwriter := io.MultiWriter(os.Stderr, logFile)

	iconBytes, err := fs.ReadFile("assets/icon.png")
	if err != nil {
		panic(err)
	}

	pi := &PhaseInverter{
		commbadge: commbadge.New(iconBytes),
		Log: log.NewWithOptions(logwriter, log.Options{
			Prefix:          "PhaseInverter",
			ReportCaller:    true,
			ReportTimestamp: true,
		}),
		Keymap: map[Transmission]*hk.Hotkey{
			TransmissionToggleMute:  hk.New([]hk.Modifier{}, hk.KeyF18),
			TransmissionTogglePower: hk.New([]hk.Modifier{hk.ModShift}, hk.KeyF18),

			TransmissionVolDown:     hk.New([]hk.Modifier{}, hk.KeyF19),
			TransmissionVolDownFine: hk.New([]hk.Modifier{hk.ModShift}, hk.KeyF19),

			TransmissionVolUp:     hk.New([]hk.Modifier{}, hk.KeyF20),
			TransmissionVolUpFine: hk.New([]hk.Modifier{hk.ModShift}, hk.KeyF20),
		},
		Menu: make(map[string]*systray.MenuItem),
	}
	pi.Log.SetColorProfile(termenv.TrueColor)

	// No connection is established here. The daemon may be unreachable at
	// launch — a laptop that booted away from the tailnet — and the app has to
	// come up anyway, reconnecting on its own when the network appears.
	pi.Conduit = pe.NewConduit(pe.NewClient(*daemon, *device), 0)
	pi.Log.Infof("driving daemon %s, device %q", *daemon, *device)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pi.Conduit.Run(ctx)

	mainthread.Init(func() {
		go pi.registerHotkeys()
		systray.Register(func() { pi.onScreen(ctx) }, pi.endTransmission)
		pi.wg.Wait()
	})
}

// transmit turns a user intent into daemon traffic.
//
// Volume goes through Conduit.Nudge, which returns immediately and batches:
// the volume keys are a rotary encoder, so this is called in dense bursts and
// must never block on the network.
func (pi *PhaseInverter) transmit(tr Transmission) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var err error
	switch tr {
	case TransmissionVolUp:
		pi.Conduit.Nudge(pi.step(coarseFraction))
	case TransmissionVolUpFine:
		pi.Conduit.Nudge(pi.step(fineFraction))
	case TransmissionVolDown:
		pi.Conduit.Nudge(-pi.step(coarseFraction))
	case TransmissionVolDownFine:
		pi.Conduit.Nudge(-pi.step(fineFraction))

	case TransmissionToggleMute:
		err = pi.Conduit.ToggleMute(ctx)
	case TransmissionTogglePower:
		err = pi.Conduit.TogglePower(ctx)
	case TransmissionPowerOn:
		err = pi.Conduit.SetPower(ctx, true)

	case TransmissionChangeInputSpotify:
		err = pi.Conduit.SetInput(ctx, InputSpotify)
	case TransmissionChangeInputPhono:
		err = pi.Conduit.SetInput(ctx, InputPhono)
	case TransmissionChangeInputSirius:
		err = pi.Conduit.SetInput(ctx, InputSirius)
	case TransmissionChangeInputAirplay:
		err = pi.Conduit.SetInput(ctx, InputAirplay)
	}

	if err != nil {
		pi.Log.Errorf("transmission %v failed: %v", tr, err)
	}
}

// step sizes a volume change against the range the daemon advertises, so the
// keys feel the same regardless of the receiver's scale.
func (pi *PhaseInverter) step(fraction float64) int {
	r := pi.Conduit.Display().Range
	span := r.Max - r.Min
	if span <= 0 {
		// No range known yet — the daemon has not answered. One step is the
		// smallest honest guess and self-corrects once state arrives.
		return 1
	}
	if s := int(float64(span) * fraction); s > 0 {
		return s
	}
	return 1
}

func (pi *PhaseInverter) registerHotkeys() {
	pi.wg.Add(len(pi.Keymap))

	for tr, k := range pi.Keymap {
		go func(tr Transmission, k *hk.Hotkey) {
			defer pi.wg.Done()

			pi.Log.Infof("registering key: %v", tr)
			if err := k.Register(); err != nil {
				pi.Log.Fatalf("failed to register hotkey: %v", err)
			}
			pi.Log.Infof("%v is registered", k)

			for range k.Keydown() {
				pi.Log.Infof("KEYDOWN %s", k)
				// Deliberately not on the main thread: a rotary encoder can
				// outrun the UI, and volume must not queue behind redraws.
				pi.transmit(tr)
			}
		}(tr, k)
	}
}

func (pi *PhaseInverter) onScreen(ctx context.Context) {
	pi.Log.Infof("Bootstrapping system tray...")

	icon := pi.commbadge.Icon
	systray.SetTemplateIcon(icon, icon)

	volumeItem := systray.AddMenuItem("Volume: --", "Current volume")
	volumeItem.Disable()

	playingItem := systray.AddMenuItem("", "What the current source is playing")
	playingItem.Disable()
	playingItem.Hide()

	systray.AddSeparator()

	for _, id := range menuOrder {
		pi.Menu[id] = systray.AddMenuItem(inputLabels[id],
			fmt.Sprintf("Set input to %s", inputLabels[id]))
	}

	systray.AddSeparator()
	for range stationSlots {
		item := systray.AddMenuItem("", "")
		item.Hide()
		pi.Stations = append(pi.Stations, item)
	}

	systray.AddSeparator()
	pwrItem := systray.AddMenuItem("Power On", "Send Power On signal")
	quitItem := systray.AddMenuItem("Quit", "Quit Phase Inverter")

	go pi.watchMenu(volumeItem, pwrItem, quitItem)
	go pi.render(volumeItem, playingItem)
	go pi.loadStations(ctx)
}

// loadStations fills the station slots from whatever the daemon reports.
//
// Retried rather than fetched once, for the same reason the app establishes no
// connection at launch: it routinely starts while the daemon is unreachable,
// and a station menu that stayed empty until the next launch would be worse
// than one that appears a few seconds late.
//
// The stations come from the receiver, never from a list compiled here. What is
// stored in those slots is whatever was saved from the remote, and this app has
// no business having an opinion about it.
func (pi *PhaseInverter) loadStations(ctx context.Context) {
	for {
		fetchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		presets, err := pi.Conduit.Presets(fetchCtx)
		cancel()

		if err == nil {
			pi.showStations(presets)
			return
		}
		pi.Log.Debugf("station list unavailable, retrying: %v", err)

		select {
		case <-time.After(stationRetryDelay):
		case <-ctx.Done():
			return
		}
	}
}

// showStations paints the preset list into the reserved slots.
func (pi *PhaseInverter) showStations(presets []pe.Preset) {
	if len(presets) > len(pi.Stations) {
		presets = presets[:len(pi.Stations)]
	}

	nums := make([]int, len(presets))
	for i, p := range presets {
		nums[i] = p.Num
	}

	// Recorded before the items appear, so a click cannot arrive against a slot
	// whose preset number is not known yet.
	pi.stationMu.Lock()
	pi.stationNums = nums
	pi.stationMu.Unlock()

	mainthread.Call(func() {
		for i, item := range pi.Stations {
			if i >= len(presets) {
				item.Hide()
				continue
			}
			item.SetTitle(presets[i].Text)
			item.SetTooltip(fmt.Sprintf("Recall preset %d", presets[i].Num))
			item.Show()
		}
	})
	pi.Log.Infof("station menu loaded: %d presets", len(presets))
}

// recallStation selects the station in a menu slot.
//
// Not a Transmission: those are the fixed, hotkey-shaped intents this app knows
// about at compile time, and stations are discovered from the daemon at
// runtime, so there is no set to enumerate.
func (pi *PhaseInverter) recallStation(slot int) {
	pi.stationMu.Lock()
	known := slot < len(pi.stationNums)
	var num int
	if known {
		num = pi.stationNums[slot]
	}
	pi.stationMu.Unlock()

	if !known {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := pi.Conduit.RecallPreset(ctx, num); err != nil {
		pi.Log.Errorf("recalling preset %d failed: %v", num, err)
	}
}

// render repaints the tray from the conduit's display.
//
// Polled rather than event-driven because the display changes for two separate
// reasons — local prediction and daemon updates — and a poll collapses both
// into one repaint path at a rate the UI can actually keep up with. At 60ms a
// spinning encoder still looks continuous.
func (pi *PhaseInverter) render(volumeItem, playingItem *systray.MenuItem) {
	ticker := time.NewTicker(60 * time.Millisecond)
	defer ticker.Stop()

	var last pe.Display
	for range ticker.C {
		d := pi.Conduit.Display()
		if d == last {
			continue
		}
		last = d

		mainthread.Call(func() {
			volumeItem.SetTitle(pi.volumeTitle(d))
			volumeItem.Show()

			// Hidden rather than blank when the source reports nothing: phono
			// and the line inputs never do, and an empty row would read as a
			// missing value rather than as an input that has none to give.
			if title := playingTitle(d.Playing); title != "" {
				playingItem.SetTitle(title)
				playingItem.SetTooltip(d.Playing.Album)
				playingItem.Show()
			} else {
				playingItem.Hide()
			}

			pi.commbadge.SetVolume(pi.percent(d))
			systray.SetTemplateIcon(pi.commbadge.Icon, pi.commbadge.Icon)

			for id, item := range pi.Menu {
				if id == d.Input {
					item.Check()
				} else {
					item.Uncheck()
				}
			}
		})
	}
}

// volumeTitle says what is known, and admits what is not. Showing a predicted
// number as though it were confirmed is what made the old setup untrustworthy.
func (pi *PhaseInverter) volumeTitle(d pe.Display) string {
	switch {
	case !d.Connected:
		// The daemon itself is unreachable — off the tailnet, or not running.
		return "Volume: -- (no daemon)"
	case !d.Online:
		// The daemon is reachable but the receiver is not answering it.
		//
		// Deliberately "unreachable" rather than "asleep": deep standby is the
		// usual cause, but a blocked network path looks identical from here,
		// and the label should state what was observed rather than guess why.
		return "Volume: -- (receiver unreachable)"
	case d.Power == pe.PowerStandby:
		return "Volume: -- (standby)"
	case d.Mute:
		return fmt.Sprintf("Volume: %d (muted)", d.Volume)
	case !d.Confirmed:
		return fmt.Sprintf("Volume: %d…", d.Volume)
	default:
		return fmt.Sprintf("Volume: %d", d.Volume)
	}
}

// playingTitle renders what is playing in one line.
//
// Falls back to the album because that is where SiriusXM puts the channel
// ("35 : SiriusXMU / Indie & Beyond"): between songs, or on a talk channel,
// the station is still worth showing when the track is not.
func playingTitle(p pe.Playing) string {
	switch {
	case p.Track != "" && p.Artist != "":
		return p.Track + " — " + p.Artist
	case p.Track != "":
		return p.Track
	default:
		return p.Album
	}
}

// percent converts device units to the 0-100 the icon's bar expects.
func (pi *PhaseInverter) percent(d pe.Display) int {
	span := d.Range.Max - d.Range.Min
	if span <= 0 {
		return 0
	}
	return (d.Volume - d.Range.Min) * 100 / span
}

func (pi *PhaseInverter) watchMenu(volumeItem, pwrItem, quitItem *systray.MenuItem) {
	inputClicks := make(chan string)
	for id, item := range pi.Menu {
		go func(id string, item *systray.MenuItem) {
			for range item.ClickedCh {
				inputClicks <- id
			}
		}(id, item)
	}

	// Watched from the moment the menu exists, not from when the stations
	// arrive: the slots are already there, and wiring them once avoids a second
	// set of goroutines racing the list load.
	stationClicks := make(chan int)
	for slot, item := range pi.Stations {
		go func(slot int, item *systray.MenuItem) {
			for range item.ClickedCh {
				stationClicks <- slot
			}
		}(slot, item)
	}

	for {
		select {
		case id := <-inputClicks:
			switch id {
			case InputSpotify:
				pi.transmit(TransmissionChangeInputSpotify)
			case InputPhono:
				pi.transmit(TransmissionChangeInputPhono)
			case InputSirius:
				pi.transmit(TransmissionChangeInputSirius)
			case InputAirplay:
				pi.transmit(TransmissionChangeInputAirplay)
			}
		case slot := <-stationClicks:
			pi.recallStation(slot)
		case <-pwrItem.ClickedCh:
			pi.transmit(TransmissionPowerOn)
		case <-quitItem.ClickedCh:
			mainthread.Call(systray.Quit)
			return
		}
	}
}

func (pi *PhaseInverter) endTransmission() {
	pi.Log.Infof("Shutting down...")
	for _, k := range pi.Keymap {
		if err := k.Unregister(); err != nil {
			pi.Log.Errorf("error unregistering hotkey %s: %v", k, err)
		}
		pi.Log.Infof("hotkey %s is unregistered", k)
	}
}
