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

const (
	VOLUME_DELTA      int = 5
	VOLUME_DELTA_FINE int = VOLUME_DELTA / 2
	VOLUME_MIN        int = 0
	VOLUME_MAX        int = 100
)

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
			t.Log.Debugf("Subscription event: %#v", m)
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
	var err error

	switch tr {
	case TransmissionChangeInputSpotify:
		t.Log.Infof("Changing input: %s => %s", t.state.currentInput, InputSpotify)
		topic = string(topicSetInput)
		msg = []byte(InputSpotify)
	case TransmissionChangeInputPhono:
		t.Log.Infof("Changing input: %s => %s", t.state.currentInput, InputPhono)
		topic = string(topicSetInput)
		msg = []byte(InputPhono)
	case TransmissionChangeInputSirius:
		t.Log.Infof("Changing input: %s => %s", t.state.currentInput, InputSirius)
		topic = string(topicSetInput)
		msg = []byte(InputSirius)
	case TransmissionChangeInputAirplay:
		t.Log.Infof("Changing input: %s => %s", t.state.currentInput, InputAirplay)
		topic = string(topicSetInput)
		msg = []byte(InputAirplay)
	case TransmissionToggleMute:
		t.Log.Infof("Changing mute: %t => %t", t.state.isMuted, !t.state.isMuted)
		topic = string(topicSetMute)
		msg = []byte(strconv.FormatBool(!t.state.isMuted))
	case TransmissionTogglePower:
		t.Log.Infof("Changing power: %t => %t", t.state.isPoweredOn, !t.state.isPoweredOn)
		topic = string(topicSetPower)
		msg = []byte(strconv.FormatBool(!t.state.isPoweredOn))
	case TransmissionVolDown:
		topic, msg, err = t.changeVolume(VOLUME_DELTA * -1)
		if err != nil {
			return
		}
	case TransmissionVolDownFine:
		topic, msg, err = t.changeVolume(VOLUME_DELTA_FINE * -1)
		if err != nil {
			return
		}
	case TransmissionVolUp:
		topic, msg, err = t.changeVolume(VOLUME_DELTA)
		if err != nil {
			return
		}
	case TransmissionVolUpFine:
		topic, msg, err = t.changeVolume(VOLUME_DELTA_FINE)
		if err != nil {
			return
		}
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

func (t *Transmitter) changeVolume(delta int) (topic string, msg []byte, err error) {
	newVol, err := clamp(t.state.currentVolume+(delta), VOLUME_MIN, VOLUME_MAX)
	if err != nil {
		t.Log.Errorf("error calculating new volume: %v", err)
		return "", nil, err
	}
	t.Log.Infof("Changing volume: %d, +%d => %d", t.state.currentVolume, (delta), newVol)
	return string(topicSetVolume), []byte(strconv.Itoa(newVol)), nil
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
