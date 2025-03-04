package main

import (
	"embed"
	"os"
	"sync"

	"github.com/adrg/xdg"
	"github.com/charmbracelet/log"
	"github.com/getlantern/systray"
	t "github.com/tlindsay/phase-inverter/transmitter"
	hk "golang.design/x/hotkey"
	"golang.design/x/hotkey/mainthread"
)

//go:embed assets/*
var fs embed.FS

type PhaseInverter struct {
	Log         *log.Logger
	Transmitter *t.Transmitter
	Keymap      map[t.Transmission]*hk.Hotkey
	wg          sync.WaitGroup
}

func main() {
	logPath, err := xdg.DataFile("Phase Inverter/phase_inverter.log")
	log.Infof("Logging to file %s...", logPath)
	if err != nil {
		log.Fatalf("failed to create log file: %v", err)
		os.Exit(1)
	}
	logwriter, err := os.OpenFile(logPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		log.Fatalf("failed to open log file: %v", err)
		os.Exit(1)
	}
	pi := &PhaseInverter{
		Log: log.NewWithOptions(
			logwriter,
			log.Options{
				Prefix:          "PhaseInverter",
				ReportCaller:    true,
				ReportTimestamp: true,
			},
		),
		Keymap: map[t.Transmission]*hk.Hotkey{
			t.TransmissionToggleMute:  hk.New([]hk.Modifier{hk.ModCmd}, hk.KeyF18),
			t.TransmissionTogglePower: hk.New([]hk.Modifier{hk.ModCmd, hk.ModShift}, hk.KeyF18),

			t.TransmissionVolDown:     hk.New([]hk.Modifier{hk.ModCmd}, hk.KeyF19),
			t.TransmissionVolDownFine: hk.New([]hk.Modifier{hk.ModCmd, hk.ModShift}, hk.KeyF19),

			t.TransmissionVolUp:     hk.New([]hk.Modifier{hk.ModCmd}, hk.KeyF20),
			t.TransmissionVolUpFine: hk.New([]hk.Modifier{hk.ModCmd, hk.ModShift}, hk.KeyF20),
		},
		wg: sync.WaitGroup{},
	}

	t, err := t.NewTransmitter(pi.Log)
	if err != nil {
		pi.Log.Fatalf("failed to initialize Transmitter: %v", err)
		os.Exit(1)
	}
	pi.Transmitter = t

	mainthread.Init(func() {
		go pi.registerHotkeys()
		systray.Register(pi.onScreen, pi.endTransmission)

		pi.wg.Wait()
	})
}

func (pi *PhaseInverter) registerHotkeys() {
	pi.wg.Add(len(pi.Keymap))

	for tr, k := range pi.Keymap {
		go func(tr t.Transmission, k *hk.Hotkey) {
			defer pi.wg.Done()

			pi.Log.Infof("registering key: %v", tr)
			if err := k.Register(); err != nil {
				pi.Log.Fatalf("failed to register hotkey: %v", err)
			}
			pi.Log.Infof("%v is registered", k)

			for {
				<-k.Keydown()
				mainthread.Call(func() {
					pi.Transmitter.Transmit(tr)
				})
			}
		}(tr, k)
	}
}

func (pi *PhaseInverter) onScreen() {
	pi.Log.Infof("Bootstrapping system tray...")
	icon, err := fs.ReadFile("assets/icon.png")
	if err != nil {
		pi.Log.Fatalf("error reading icon: %v", err)
	}
	systray.SetTemplateIcon(icon, icon)
	spotifyItem := systray.AddMenuItem("Spotify", "Set input to Spotify")
	phonoItem := systray.AddMenuItem("Phono", "Set input to Phono")
	siriusItem := systray.AddMenuItem("SiriusXM", "Set input to SiriusXM radio")
	airplayItem := systray.AddMenuItem("AirPlay", "Set input to AirPlay Receiver")
	systray.AddSeparator()
	quitItem := systray.AddMenuItem("Quit", "Quit Phase Inverter")

	go func() {
		for {
			select {
			case <-spotifyItem.ClickedCh:
				mainthread.Call(func() {
					pi.Transmitter.Transmit(t.TransmissionChangeInputSpotify)
				})
			case <-phonoItem.ClickedCh:
				mainthread.Call(func() {
					pi.Transmitter.Transmit(t.TransmissionChangeInputPhono)
				})
			case <-siriusItem.ClickedCh:
				mainthread.Call(func() {
					pi.Transmitter.Transmit(t.TransmissionChangeInputSirius)
				})
			case <-airplayItem.ClickedCh:
				mainthread.Call(func() {
					pi.Transmitter.Transmit(t.TransmissionChangeInputAirplay)
				})
			case <-quitItem.ClickedCh:
				mainthread.Call(systray.Quit)
			}
		}
	}()
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
