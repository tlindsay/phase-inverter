package transmitter

import (
	"fmt"
	"strconv"

	"github.com/charmbracelet/log"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

const (
	brokerURL   = "tcp://broker:1883"
	atMostOnce  = 0
	atLeastOnce = 1
	exactlyOnce = 2
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
	TransmissionPowerOn

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
	CurrentVolume int
	CurrentInput  string
	IsMuted       bool
	IsPoweredOn   bool
}

type Transmitter struct {
	Log      *log.Logger
	Receiver chan playerState
	client   mqtt.Client
	state    playerState
}

func NewTransmitter(l *log.Logger) (*Transmitter, error) {
	c, err := newClient()
	if err != nil {
		return nil, err
	}

	l.Infof("MQTT Client initialized: %#v", c)

	t := &Transmitter{Log: l, Receiver: make(chan playerState), client: c}

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
		map[string]byte{topicStatusVolume: exactlyOnce, topicStatusMute: exactlyOnce, topicStatusPower: atLeastOnce, topicStatusInput: atLeastOnce},
		func(c mqtt.Client, m mqtt.Message) {
			t.Log.Debugf("Subscription event: %#v", m)
			switch m.Topic() {
			case topicStatusInput:
				input := m.Payload()
				t.Log.Infof("Input received: %s", input)
				t.state.CurrentInput = string(input)
			case topicStatusMute:
				mute, err := strconv.ParseBool(string(m.Payload()))
				if err != nil {
					t.Log.Errorf("Could not parse payload from Mute event: %v", err)
					return
				}

				t.Log.Infof("Mute received: %t", mute)
				t.state.IsMuted = mute
			case topicStatusPower:
				var pwr bool
				payload := string(m.Payload())
				if payload == "on" {
					pwr = true
				} else if payload == "standby" {
					pwr = false
				} else {
					t.Log.Errorf("Could not parse payload from Power event: %v", payload)
					return
				}

				t.Log.Infof("Power received: %s", payload)
				t.state.IsPoweredOn = pwr
			case topicStatusVolume:
				vol, err := strconv.Atoi(string(m.Payload()))
				if err != nil {
					t.Log.Errorf("Error parsing volume: %v", err)
					break
				}

				t.Log.Infof("Volume received: %d", vol)
				t.state.CurrentVolume = vol
			}
			t.Receiver <- t.state
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
		t.Log.Infof("Changing input: %s => %s", t.state.CurrentInput, InputSpotify)
		topic = string(topicSetInput)
		msg = []byte(InputSpotify)
	case TransmissionChangeInputPhono:
		t.Log.Infof("Changing input: %s => %s", t.state.CurrentInput, InputPhono)
		topic = string(topicSetInput)
		msg = []byte(InputPhono)
	case TransmissionChangeInputSirius:
		t.Log.Infof("Changing input: %s => %s", t.state.CurrentInput, InputSirius)
		topic = string(topicSetInput)
		msg = []byte(InputSirius)
	case TransmissionChangeInputAirplay:
		t.Log.Infof("Changing input: %s => %s", t.state.CurrentInput, InputAirplay)
		topic = string(topicSetInput)
		msg = []byte(InputAirplay)
	case TransmissionToggleMute:
		t.Log.Infof("Changing mute: %t => %t", t.state.IsMuted, !t.state.IsMuted)
		topic = string(topicSetMute)
		msg = []byte(strconv.FormatBool(!t.state.IsMuted))
	case TransmissionTogglePower:
		t.Log.Infof("Changing power: %t => %t", t.state.IsPoweredOn, !t.state.IsPoweredOn)
		topic = string(topicSetPower)
		msg = []byte(strconv.FormatBool(!t.state.IsPoweredOn))
	case TransmissionPowerOn:
		t.Log.Infof("Forcing power: %t", true)
		topic = string(topicSetPower)
		msg = []byte(strconv.FormatBool(true))
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
	token := t.client.Publish(topic, exactlyOnce, false, msg)
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
	newVol, err := clamp(t.state.CurrentVolume+(delta), VOLUME_MIN, VOLUME_MAX)
	if err != nil {
		t.Log.Errorf("error calculating new volume: %v", err)
		return "", nil, err
	}
	t.Log.Infof("Changing volume: %d, +%d => %d", t.state.CurrentVolume, (delta), newVol)
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
