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
	TransmissionVolUp
	TransmissionToggleMute
)

const VOLUME_DELTA int = 5

type playerState struct {
	currentVolume int
	isMuted       bool
}

type Transmitter struct {
	Log    *log.Logger
	client mqtt.Client
	state  playerState
}

func NewTransmitter(l *log.Logger) (*Transmitter, error) {
	t := &Transmitter{Log: l.WithPrefix("Transmitter")}

	c, err := newClient()
	if err != nil {
		return nil, err
	}
	t.client = c

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
	case TransmissionVolDown:
		t.Log.Infof("Current volume: %d, Decrease: %d", t.state.currentVolume, VOLUME_DELTA)
		newVol, err := clamp(t.state.currentVolume-VOLUME_DELTA, 0, 100)
		if err != nil {
			t.Log.Errorf("error calculating new volume: %v", err)
			return
		}
		msg = []byte(strconv.Itoa(newVol))
		topic = string(topicSetVolume)
	case TransmissionVolUp:
		t.Log.Infof("Current volume: %d, Increase: %d", t.state.currentVolume, VOLUME_DELTA)
		newVol, err := clamp(t.state.currentVolume+VOLUME_DELTA, 0, 100)
		if err != nil {
			t.Log.Errorf("error calculating new volume: %v", err)
			return
		}
		msg = []byte(strconv.Itoa(newVol))
		topic = string(topicSetVolume)
	case TransmissionToggleMute:
		topic = string(topicSetMute)
	}

	t.Log.Infof("Attempting to publish to %s: %s", topic, msg)
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
