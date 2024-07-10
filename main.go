package main

import (
	"os"

	"github.com/charmbracelet/log"
	"github.com/getlantern/systray"
	"golang.design/x/hotkey"
	"golang.design/x/hotkey/mainthread"
)

type PhaseInverter struct {
	VolDown *hotkey.Hotkey
	VolUp   *hotkey.Hotkey
}

func main() {
	pi := &PhaseInverter{}
	systray.Run(pi.onScreen, pi.endTransmission)
}

func (pi *PhaseInverter) RegisterHotkeys() {
	hkVolDown := hotkey.New([]hotkey.Modifier{hotkey.ModCmd, hotkey.ModShift}, hotkey.KeyDown)
	if err := hkVolDown.Register(); err != nil {
		log.Fatalf("hotkey: failed to register hotkey: %v", err)
	}
	log.Infof("hotkey: %v is registered", hkVolDown)

	hkVolUp := hotkey.New([]hotkey.Modifier{hotkey.ModCmd, hotkey.ModShift}, hotkey.KeyUp)
	if err := hkVolUp.Register(); err != nil {
		log.Fatalf("hotkey: failed to register hotkey: %v", err)
	}
	log.Infof("hotkey: %v is registered", hkVolUp)

	pi.VolDown = hkVolDown
	pi.VolUp = hkVolUp
}

func (pi *PhaseInverter) onScreen() {
	icon, err := os.ReadFile("./assets/icon.png")
	if err != nil {
		log.Fatalf("viewer: error reading icon: %v", err)
	}
	systray.SetTemplateIcon(icon, icon)
	quitItem := systray.AddMenuItem("Quit", "Quit")

	go mainthread.Init(func() {
		pi.RegisterHotkeys()
		for {
			select {
			case <-quitItem.ClickedCh:
				systray.Quit()
			case <-pi.VolDown.Keydown():
				log.Infof("phase inverter: VolDown keypress")
			case <-pi.VolUp.Keydown():
				log.Infof("phase inverter: VolUp keypress")
			}
		}
	})

}

func (pi *PhaseInverter) endTransmission() {
	log.Infof("viewer: Shutting down...")
	pi.VolDown.Unregister()
	log.Infof("hotkey: %v is unregistered", pi.VolDown)
	pi.VolUp.Unregister()
	log.Infof("hotkey: %v is unregistered", pi.VolUp)
}
