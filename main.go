package main

import (
	"embed"
	"os"

	"github.com/charmbracelet/log"
	"github.com/getlantern/systray"
	t "github.com/tlindsay/phase-inverter/transmitter"
	"golang.design/x/hotkey"
	"golang.design/x/hotkey/mainthread"
)

//go:embed assets/*
var fs embed.FS

type PhaseInverter struct {
	Log         *log.Logger
	Transmitter *t.Transmitter
	Keymap      map[t.Transmission]*hotkey.Hotkey
}

func main() {
	// Have to shim Syslog manually because the stdlib package is broken
	// https://github.com/golang/go/issues/59229
	logwriter := Syslog{LOG_INFO | LOG_DAEMON}
	pi := &PhaseInverter{
		Log: log.NewWithOptions(
			logwriter,
			log.Options{
				Prefix:          "PhaseInverter",
				ReportCaller:    true,
				ReportTimestamp: true,
			},
		),
	}

	t, err := t.NewTransmitter(pi.Log)
	if err != nil {
		pi.Log.Fatalf("failed to initialize Transmitter: %v", err)
		os.Exit(1)
	}
	pi.Transmitter = t

	systray.Run(pi.onScreen, pi.endTransmission)
}

func (pi *PhaseInverter) registerHotkeys() {
	for _, k := range pi.Keymap {
		if err := k.Register(); err != nil {
			pi.Log.Fatalf("failed to register hotkey: %v", err)
		}
		pi.Log.Infof("%v is registered", k)
	}
}

func (pi *PhaseInverter) onScreen() {
	icon, err := fs.ReadFile("assets/icon.png")
	if err != nil {
		pi.Log.Fatalf("error reading icon: %v", err)
	}
	systray.SetTemplateIcon(icon, icon)
	quitItem := systray.AddMenuItem("Quit", "Quit Phase Inverter")

	go mainthread.Init(func() {
		pi.registerHotkeys()
		for {
			select {
			case <-quitItem.ClickedCh:
				mainthread.Call(systray.Quit)
			case <-pi.Keymap[t.TransmissionToggleMute].Keydown():
				mainthread.Call(func() {
					pi.Transmitter.Transmit(t.TransmissionToggleMute)
				})
			case <-pi.Keymap[t.TransmissionTogglePower].Keydown():
				mainthread.Call(func() {
					pi.Transmitter.Transmit(t.TransmissionTogglePower)
				})
			case <-pi.Keymap[t.TransmissionVolDown].Keydown():
				mainthread.Call(func() {
					pi.Transmitter.Transmit(t.TransmissionVolDown)
				})
			case <-pi.Keymap[t.TransmissionVolDownFine].Keydown():
				mainthread.Call(func() {
					pi.Transmitter.Transmit(t.TransmissionVolDownFine)
				})
			case <-pi.Keymap[t.TransmissionVolUp].Keydown():
				mainthread.Call(func() {
					pi.Transmitter.Transmit(t.TransmissionVolUp)
				})
			case <-pi.Keymap[t.TransmissionVolUpFine].Keydown():
				mainthread.Call(func() {
					pi.Transmitter.Transmit(t.TransmissionVolUpFine)
				})
			}
		}
	})

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
