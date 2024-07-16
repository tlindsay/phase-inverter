package transmitter

import (
	"fmt"
	"strconv"

	"github.com/charmbracelet/log"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

const (
	brokerURL = "tcp://broker.local:1883"
	qos       = 2
)

type Topic string

const (
	topicStatusDebug Topic = "musiccast/status/Office/#"

	topicStatusPlayer = "musiccast/status/Office/player"
	topicStatusVolume = "musiccast/status/Office/volume"
	topicStatusMute   = "musiccast/status/Office/mute"
	topicStatusInput  = "musiccast/status/Office/input"
	topicStatusPower  = "musiccast/status/Office/power"

	topicSetVolume = "musiccast/set/Office/volume"
	topicSetMute   = "musiccast/set/Office/mute"
	topicSetInput  = "musiccast/set/Office/input"
	topicSetPower  = "musiccast/set/Office/power"
)

type Transmission int

const (
	TransmissionVolDown Transmission = iota
	TransmissionVolDownFine
	TransmissionVolUp
	TransmissionVolUpFine
	TransmissionToggleMute
	TransmissionTogglePower

	TransmissionChangeInputSpotify
	TransmissionChangeInputPhono
	TransmissionChangeInputSirius
	TransmissionChangeInputAirplay
)

const (
	InputSpotify = "Spotify"
	InputPhono   = "Phono"
	InputSirius  = "SiriusXM"
	InputAirplay = "Airplay Receiver"
)

const VOLUME_DELTA int = 5

type playerState struct {
	currentVolume int
	currentInput  string
	isMuted       bool
	isPoweredOn   bool
}

type Transmitter struct {
	Log    *log.Logger
	client mqtt.Client
	state  playerState
}

func NewTransmitter(l *log.Logger) (*Transmitter, error) {
	c, err := newClient()
	if err != nil {
		return nil, err
	}

	t := &Transmitter{Log: l, client: c}

	if err := t.setupSubscriptions(); err != nil {
		t.Log.Errorf("failed to establish subscriptions: %v", err)
		return nil, err
	}

	return t, nil
}

func newClient() (mqtt.Client, error) {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(brokerURL)
	opts.SetClientID("phase-inverter")

	mc := mqtt.NewClient(opts)

	if token := mc.Connect(); token.Wait() && token.Error() != nil {
		return nil, token.Error()
	}

	return mc, nil
}

func (t *Transmitter) setupSubscriptions() error {
	if t.client == nil {
		return fmt.Errorf("tried to setup subscriptions before MQTT client initialization")
	}

	token := t.client.SubscribeMultiple(
		map[string]byte{topicStatusVolume: qos, topicStatusMute: qos},
		func(c mqtt.Client, m mqtt.Message) {
			t.Log.Infof("Subscription event: %#v", m)
			switch m.Topic() {
			case topicStatusInput:
				input := m.Payload()
				t.Log.Infof("Input received: %s", input)
				t.state.currentInput = string(input)
			case topicStatusMute:
				mute, err := strconv.ParseBool(string(m.Payload()))
				if err != nil {
					t.Log.Errorf("Could not parse payload from Mute event: %v", err)
					return
				}

				t.Log.Infof("Mute received: %t", mute)
				t.state.isMuted = mute
			case topicStatusPower:
				var pwr bool
				if payload := string(m.Payload()); payload == "on" {
					pwr = true
				} else if payload == "standby" {
					pwr = false
				} else {
					t.Log.Errorf("Could not parse payload from Power event: %v", payload)
					return
				}

				t.Log.Infof("Power received: %t", pwr)
				t.state.isPoweredOn = pwr
			case topicStatusVolume:
				vol, err := strconv.Atoi(string(m.Payload()))
				if err != nil {
					t.Log.Errorf("Error parsing volume: %v", err)
					break
				}

				t.Log.Infof("Volume received: %d", vol)
				t.state.currentVolume = vol
			}
		},
	)
	go func() {
		<-token.Done()
		if err := token.Error(); err != nil {
			t.Log.Errorf("subscription error: %v", err)
		}
	}()

	return nil
}

func (t *Transmitter) Transmit(tr Transmission) {
	if t.client == nil {
		t.Log.Fatal("tried to transmit before MQTT client initialization")
	}

	var topic string
	var msg []byte

	switch tr {
	case TransmissionChangeInputSpotify:
		topic = string(topicSetInput)
		t.Log.Infof("Current input: %s => %s", t.state.currentInput, InputSpotify)
		msg = []byte(InputSpotify)
	case TransmissionChangeInputPhono:
		topic = string(topicSetInput)
		t.Log.Infof("Current input: %s => %s", t.state.currentInput, InputPhono)
		msg = []byte(InputPhono)
	case TransmissionChangeInputSirius:
		topic = string(topicSetInput)
		t.Log.Infof("Current input: %s => %s", t.state.currentInput, InputSirius)
		msg = []byte(InputSirius)
	case TransmissionChangeInputAirplay:
		topic = string(topicSetInput)
		t.Log.Infof("Current input: %s => %s", t.state.currentInput, InputAirplay)
		msg = []byte(InputAirplay)
	case TransmissionToggleMute:
		topic = string(topicSetMute)
		t.Log.Infof("Current mute: %t => %t", t.state.isMuted, !t.state.isMuted)
		msg = []byte(strconv.FormatBool(!t.state.isMuted))
	case TransmissionTogglePower:
		topic = string(topicSetPower)
		t.Log.Infof("Current power: %t => %t", t.state.isMuted, !t.state.isMuted)
		msg = []byte(strconv.FormatBool(!t.state.isPoweredOn))
	case TransmissionVolDown:
		newVol, err := clamp(t.state.currentVolume-VOLUME_DELTA, 0, 100)
		if err != nil {
			t.Log.Errorf("error calculating new volume: %v", err)
			return
		}
		t.Log.Infof("Current volume: %d, -%d => %d", t.state.currentVolume, VOLUME_DELTA, newVol)
		msg = []byte(strconv.Itoa(newVol))
		topic = string(topicSetVolume)
	case TransmissionVolDownFine:
		newVol, err := clamp(t.state.currentVolume-(VOLUME_DELTA/2), 0, 100)
		if err != nil {
			t.Log.Errorf("error calculating new volume: %v", err)
			return
		}
		t.Log.Infof("Current volume: %d, -%d => %d", t.state.currentVolume, (VOLUME_DELTA / 2), newVol)
		msg = []byte(strconv.Itoa(newVol))
		topic = string(topicSetVolume)
	case TransmissionVolUp:
		newVol, err := clamp(t.state.currentVolume+VOLUME_DELTA, 0, 100)
		if err != nil {
			t.Log.Errorf("error calculating new volume: %v", err)
			return
		}
		t.Log.Infof("Current volume: %d, +%d => %d", t.state.currentVolume, VOLUME_DELTA, newVol)
		msg = []byte(strconv.Itoa(newVol))
		topic = string(topicSetVolume)
	case TransmissionVolUpFine:
		newVol, err := clamp(t.state.currentVolume+(VOLUME_DELTA/2), 0, 100)
		if err != nil {
			t.Log.Errorf("error calculating new volume: %v", err)
			return
		}
		t.Log.Infof("Current volume: %d, +%d => %d", t.state.currentVolume, (VOLUME_DELTA / 2), newVol)
		msg = []byte(strconv.Itoa(newVol))
		topic = string(topicSetVolume)
	}

	t.Log.Debugf("Attempting to publish to %s: %s", topic, msg)
	token := t.client.Publish(topic, qos, false, msg)
	go func() {
		<-token.Done()
		if err := token.Error(); err != nil {
			t.Log.Fatal("could not publish: ", err)
		} else {
			t.Log.Infof("published message %d", token.(*mqtt.PublishToken).MessageID())
		}
	}()
}

func clamp(n, min, max int) (int, error) {
	if min > max {
		return 0, fmt.Errorf("clamp max must be greater than min")
	}
	if n < min {
		return min, nil
	}
	if n > max {
		return max, nil
	}
	return n, nil
}
