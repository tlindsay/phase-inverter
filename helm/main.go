package helm

import (
	"github.com/charmbracelet/log"
	"golang.design/x/hotkey"
	"golang.design/x/hotkey/mainthread"
)

func SetCourse() {
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

	defer func() {
		hkVolDown.Unregister()
		log.Infof("hotkey: %v is unregistered", hkVolDown)
		hkVolUp.Unregister()
		log.Infof("hotkey: %v is unregistered", hkVolUp)
	}()

	for {
		select {
		case <-hkVolUp.Keydown():
			go func() {
				log.Infof("hotkey: %s keydown", hkVolUp.String())
			}()
		case <-hkVolDown.Keydown():
			go func() {
				log.Infof("hotkey: %s keydown", hkVolDown.String())
			}()
		}
	}
}

func Engage() {
	mainthread.Init(SetCourse)
}
