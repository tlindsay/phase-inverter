package main

import (
	"embed"
	"os"

	"github.com/charmbracelet/log"
	"github.com/getlantern/systray"
	"github.com/tlindsay/phase-inverter/transmitter"
	"golang.design/x/hotkey"
	"golang.design/x/hotkey/mainthread"
)

//go:embed assets/*
var fs embed.FS

type PhaseInverter struct {
	Log         *log.Logger
	Transmitter *transmitter.Transmitter
	VolDown     *hotkey.Hotkey
	VolUp       *hotkey.Hotkey
}

func main() {
	pi := &PhaseInverter{
		Log: log.WithPrefix("PhaseInverter"),
	}

	t, err := transmitter.NewTransmitter(pi.Log)
	if err != nil {
		pi.Log.Fatalf("failed to initialize Transmitter: %v", err)
		os.Exit(1)
	}
	pi.Transmitter = t

	systray.Run(pi.onScreen, pi.endTransmission)
}

func (pi *PhaseInverter) registerHotkeys() {
	hkVolDown := hotkey.New([]hotkey.Modifier{hotkey.ModCmd, hotkey.ModShift}, hotkey.KeyF19)
	if err := hkVolDown.Register(); err != nil {
		log.Fatalf("PhaseInverter: failed to register hotkey: %v", err)
	}
	log.Infof("PhaseInverter: %v is registered", hkVolDown)

	hkVolUp := hotkey.New([]hotkey.Modifier{hotkey.ModCmd, hotkey.ModShift}, hotkey.KeyF20)
	if err := hkVolUp.Register(); err != nil {
		log.Fatalf("PhaseInverter: failed to register hotkey: %v", err)
	}
	log.Infof("PhaseInverter: %v is registered", hkVolUp)

	pi.VolDown = hkVolDown
	pi.VolUp = hkVolUp
}

func (pi *PhaseInverter) onScreen() {
	icon, err := fs.ReadFile("assets/icon.png")
	if err != nil {
		log.Fatalf("PhaseInverter: error reading icon: %v", err)
	}
	systray.SetTemplateIcon(icon, icon)
	quitItem := systray.AddMenuItem("Quit", "Quit Phase Inverter")

	go mainthread.Init(func() {
		pi.registerHotkeys()
		for {
			select {
			case <-quitItem.ClickedCh:
				mainthread.Call(systray.Quit)
			case <-pi.VolDown.Keydown():
				mainthread.Call(func() {
					pi.Transmitter.Transmit(transmitter.TransmissionVolDown)
				})
			case <-pi.VolUp.Keydown():
				mainthread.Call(func() {
					pi.Transmitter.Transmit(transmitter.TransmissionVolUp)
				})
			}
		}
	})

}

func (pi *PhaseInverter) endTransmission() {
	log.Infof("PhaseInverter: Shutting down...")
	pi.VolDown.Unregister()
	log.Infof("PhaseInverter: hotkey %v is unregistered", pi.VolDown)
	pi.VolUp.Unregister()
	log.Infof("PhaseInverter: hotkey %v is unregistered", pi.VolUp)
}
