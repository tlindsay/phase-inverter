package main

import (
	"time"

	"github.com/charmbracelet/log"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/tlindsay/subspace/subspace"
)

const (
	brokerURL = "tcp://broker.local:1883"
	topic     = "phaseinverter/test"
)

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

func main() {
	log.Info("Hello! Let's send some MQTT messages")

	client, err := newClient()
	if err != nil {
		log.Fatal("exit on broken setup: ", err)
	}
	log.Info("MQTT Client Initialized!")

	log.Infof("Subscribing to topic: %s", topic)
	if token := client.Subscribe(topic, 1, func(_ mqtt.Client, m mqtt.Message) {
		log.Infof("Topic: %s", m.Topic())
		log.Infof("Message: %s", m.Payload())
	}); token.Wait() && token.Error() != nil {
		log.Fatalf("Failed to establish subscription: %q", token.Error())
	}

	log.Infof("Attempting to publish to %s", topic)

	msgs, _ := subspace.MakeItSo(10, 1, "geordi")
	for _, msg := range msgs {
		if token := client.Publish(topic, 1, false, []byte(msg)); token.Wait() && token.Error() != nil {
			log.Fatal("could not publish: ", token.Error())
		}

		time.Sleep(2 * time.Second)
	}
}
