package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"

	"github.com/karloygard/xcomfortd-go/pkg/xc"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type MqttRelay struct {
	xc.Interface

	client mqtt.Client
	ctx    context.Context
}

func (r *MqttRelay) dimmerCallback(c mqtt.Client, msg mqtt.Message) {
	var dp, value int

	if _, err := fmt.Sscanf(msg.Topic(), "xcomfort/%d/set/dimmer", &dp); err != nil {
		log.Println(err)
		return
	}
	if _, err := fmt.Sscanf(string(msg.Payload()), "%d", &value); err != nil {
		log.Println(err)
		return
	}

	if datapoint := r.Datapoint(dp); datapoint != nil {
		log.Printf("topic: %s, message: %s\n", msg.Topic(), string(msg.Payload()))

		if _, err := datapoint.Dim(r.ctx, value); err != nil {
			log.Println(err)
		} else {
			r.StatusValue(datapoint, value)
			// Send bool as well, to appease HA
			r.StatusBool(datapoint, value > 0)
		}
	} else {
		log.Printf("unknown datapoint %d\n", dp)
	}
}

func (r *MqttRelay) switchCallback(c mqtt.Client, msg mqtt.Message) {
	var dp int

	if _, err := fmt.Sscanf(msg.Topic(), "xcomfort/%d/set/switch", &dp); err != nil {
		log.Println(err)
		return
	}

	if datapoint := r.Datapoint(dp); datapoint != nil {
		log.Printf("topic: %s, message: %s\n", msg.Topic(), string(msg.Payload()))

		on := string(msg.Payload()) == "true"

		if _, err := datapoint.Switch(r.ctx, on); err != nil {
			log.Println(err)
		} else {
			r.StatusBool(datapoint, on)
		}
	} else {
		log.Printf("unknown datapoint %d\n", dp)
	}
}

func (r *MqttRelay) StatusValue(datapoint *xc.Datapoint, value int) {
	topic := fmt.Sprintf("xcomfort/%d/get/dimmer", datapoint.Number())
	r.client.Publish(topic, 1, true, fmt.Sprint(value))
	r.StatusBool(datapoint, value > 0)
}

func (r *MqttRelay) StatusBool(datapoint *xc.Datapoint, on bool) {
	topic := fmt.Sprintf("xcomfort/%d/get/switch", datapoint.Number())
	r.client.Publish(topic, 1, true, fmt.Sprint(on))
}

func (r *MqttRelay) connected(c mqtt.Client) {
	log.Println("Connected to broker")
}

func (r *MqttRelay) connectionLost(c mqtt.Client, err error) {
	log.Printf("Lost connection with broker: %s", err)
}

func (r *MqttRelay) Connect(ctx context.Context, clientId string, uri *url.URL) error {
	opts := mqtt.NewClientOptions()
	broker := fmt.Sprintf("tcp://%s", uri.Host)

	log.Printf("Connecting to MQTT broker '%s' with id '%s'", broker, clientId)

	opts.AddBroker(broker)
	opts.SetUsername(uri.User.Username())
	if password, set := uri.User.Password(); set {
		opts.SetPassword(password)
	}
	opts.SetClientID(clientId)
	opts.SetOnConnectHandler(r.connected)
	opts.SetConnectionLostHandler(r.connectionLost)

	r.client = mqtt.NewClient(opts)
	token := r.client.Connect()
	token.Wait()
	if err := token.Error(); err != nil {
		return err
	}

	r.client.Subscribe("xcomfort/+/set/dimmer", 0, r.dimmerCallback)
	r.client.Subscribe("xcomfort/+/set/switch", 0, r.switchCallback)

	r.ctx = ctx

	return nil
}

// HADiscoveryAdd will send a discovery message to Home Assistant with the provided discoveryPrefix
// that will add the devices to Home Assistant.
func (r *MqttRelay) HADiscoveryAdd(discoveryPrefix string) error {
	return r.ForEachDevice(func(dev *xc.Device) error {
		topic, addMsg, _, err := createDiscoveryMessages(discoveryPrefix, dev)
		if err != nil {
			return err
		}
		if topic != "" {
			log.Printf("Sending HA discovery add message: %s\n", topic)
			r.client.Publish(topic, 1, true, addMsg)
		}
		return nil
	})
}

// HADiscoveryRemove will send a discovery message to Home Assistant with the provided discoveryPrefix
// that will remove the devices from Home Assistant.
func (r *MqttRelay) HADiscoveryRemove(discoveryPrefix string) error {
	return r.ForEachDevice(func(dev *xc.Device) error {
		topic, _, removeMsg, err := createDiscoveryMessages(discoveryPrefix, dev)
		if err != nil {
			return err
		}
		if topic != "" {
			log.Printf("Sending HA discovery remove message: %s\n", topic)
			r.client.Publish(topic, 1, true, removeMsg)
		}
		return nil
	})
}

func createDiscoveryMessages(discoveryPrefix string, dev *xc.Device) (string, string, string, error) {
	var isLight bool
	var isDimmable bool
	switch dev.Type() {
	case xc.DT_CSAU_0101:
		isLight = true
	case xc.DT_CDAx_01NG:
		isLight = true
		isDimmable = true
	}

	if !isLight {
		return "", "", "", nil
	}

	lightID := fmt.Sprintf("xcomfort_%d", dev.SerialNumber())

	dataPoint := dev.Datapoints[0].Number() // TODO: Is this correct

	config := make(map[string]string)

	config["name"] = lightID
	config["command_topic"] = fmt.Sprintf("xcomfort/%d/set/switch", dataPoint)
	config["state_topic"] = fmt.Sprintf("xcomfort/%d/get/switch", dataPoint)
	config["payload_on"] = "true"
	config["payload_off"] = "false"
	config["optimistic"] = "false"

	if isDimmable {
		config["brightness_command_topic"] = fmt.Sprintf("xcomfort/%d/set/dimmer", dataPoint)
		config["brightness_scale"] = "100"
		config["brightness_state_topic"] = fmt.Sprintf("xcomfort/%d/get/dimmer", dataPoint)
	}

	addMsg, err := json.Marshal(config)
	if err != nil {
		return "", "", "", err
	}

	return fmt.Sprintf("%s/light/%s/config", discoveryPrefix, lightID), string(addMsg), "", nil
}
