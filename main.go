package main

import (
	"embed"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/adrg/xdg"
	"github.com/charmbracelet/log"
	"github.com/getlantern/systray"
	"github.com/muesli/termenv"
	hk "golang.design/x/hotkey"
	"golang.design/x/hotkey/mainthread"

	"github.com/tlindsay/phase-inverter/commbadge"
	t "github.com/tlindsay/phase-inverter/transmitter"
)

//go:embed assets/*
var fs embed.FS

type PhaseInverter struct {
	commbadge   *commbadge.CommBadge
	Log         *log.Logger
	Transmitter *t.Transmitter
	Keymap      map[t.Transmission]*hk.Hotkey
	Menu        map[string]*systray.MenuItem
	wg          sync.WaitGroup
}

func main() {
	logPath, err := xdg.DataFile("Phase Inverter/phase_inverter.log")
	log.Infof("Logging to file %s...", logPath)
	if err != nil {
		log.Fatalf("failed to create log file: %v", err)
		os.Exit(1)
	}
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		log.Fatalf("failed to open log file: %v", err)
		os.Exit(1)
	}
	logwriter := io.MultiWriter(os.Stderr, logFile)
	iconBytes, err := fs.ReadFile("assets/icon.png")
	if err != nil {
		panic(err)
	}
	pi := &PhaseInverter{
		commbadge: commbadge.New(iconBytes),
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
		Menu: make(map[string]*systray.MenuItem),
		wg:   sync.WaitGroup{},
	}
	pi.Log.SetColorProfile(termenv.TrueColor)

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
	icon := pi.commbadge.Icon
	systray.SetTemplateIcon(icon, icon)
	volumeItem := systray.AddMenuItem("Volume: 0", "Current volume")
	volumeItem.Disable()
	systray.AddSeparator()
	pi.Menu[t.InputSpotify] = systray.AddMenuItem(t.InputSpotify, "Set input to Spotify")
	pi.Menu[t.InputPhono] = systray.AddMenuItem(t.InputPhono, "Set input to Phono")
	pi.Menu[t.InputSirius] = systray.AddMenuItem(t.InputSirius, "Set input to SiriusXM radio")
	pi.Menu[t.InputAirplay] = systray.AddMenuItem(t.InputAirplay, "Set input to AirPlay Receiver")
	systray.AddSeparator()
	pwrItem := systray.AddMenuItem("Power On", "Send Power On signal")
	quitItem := systray.AddMenuItem("Quit", "Quit Phase Inverter")

	go func() {
		for {
			select {
			case playerState := <-pi.Transmitter.Receiver:
				mainthread.Call(func() {
					// if playerState.IsPoweredOn {
					volumeItem.SetTitle(fmt.Sprintf("Volume: %d", playerState.CurrentVolume))
					volumeItem.Show()

					pi.commbadge.SetVolume(playerState.CurrentVolume)
					systray.SetTemplateIcon(pi.commbadge.Icon, pi.commbadge.Icon)

					if item, ok := pi.Menu[playerState.CurrentInput]; ok {
						item.Check()
					}
					// } else {
					// 	volumeItem.Hide()
					// 	for _, item := range pi.Menu {
					// 		item.Uncheck()
					// 	}
					// }
				})
			case <-pi.Menu[t.InputSpotify].ClickedCh:
				mainthread.Call(func() {
					pi.Transmitter.Transmit(t.TransmissionChangeInputSpotify)
				})
			case <-pi.Menu[t.InputPhono].ClickedCh:
				mainthread.Call(func() {
					pi.Transmitter.Transmit(t.TransmissionChangeInputPhono)
				})
			case <-pi.Menu[t.InputSirius].ClickedCh:
				mainthread.Call(func() {
					pi.Transmitter.Transmit(t.TransmissionChangeInputSirius)
				})
			case <-pi.Menu[t.InputAirplay].ClickedCh:
				mainthread.Call(func() {
					pi.Transmitter.Transmit(t.TransmissionChangeInputAirplay)
				})
			case <-pwrItem.ClickedCh:
				mainthread.Call(func() {
					pi.Transmitter.Transmit(t.TransmissionPowerOn)
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
