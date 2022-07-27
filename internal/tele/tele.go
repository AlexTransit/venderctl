// Server side of vender tele.
// Goals:
// - receive telemetry
// - receive state
// - send command
// - while hiding transport protocol (MQTT)
package tele

import (
	"context"
	"fmt"

	"sync"

	"github.com/AlexTransit/vender/log2"
	vender_api "github.com/AlexTransit/vender/tele"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/golang/protobuf/proto"
	"github.com/juju/errors"
	"github.com/temoto/alive/v2"

	tele_api "github.com/AlexTransit/venderctl/internal/tele/api"
	tele_config "github.com/AlexTransit/venderctl/internal/tele/config"
)

type tele struct { //nolint:maligned
	sync.RWMutex
	alive *alive.Alive
	conf  tele_config.Config
	log   *log2.Log
	pch   chan tele_api.Packet
	m     mqtt.Client
	mopt  *mqtt.ClientOptions
	// mqttcom interface {
	// 	Close() error
	// 	Publish(context.Context, *packet.Message) error
	// }
	// secrets Secrets
	clientId        string
	clientPasword   string
	clientSubscribe string
}

func NewTele() tele_api.Teler { return &tele{} }

func (self *tele) Init(ctx context.Context, log *log2.Log, teleConfig tele_config.Config) error {
	self.Lock()
	defer self.Unlock()

	self.alive = alive.NewAlive()
	self.conf = teleConfig
	// fix AlexM move this to config
	switch self.conf.Mode {
	case tele_config.ModeDisabled: // disabled
		panic(self.msgInvalidMode())
	case tele_config.ModeCommand:
		self.clientId = "command"
		self.clientPasword = "commandpass"
	case tele_config.ModeTax:
		self.clientId = "tax"
		self.clientPasword = "taxpass"
	case tele_config.ModeSponge:
		self.clientId = "ctl"
		self.clientPasword = "ctlpass"
	case tele_config.ModeTelegram:
		self.clientId = "telegram"
		self.clientPasword = "telegrampass"
	default:
		panic(self.msgInvalidMode())
	}
	self.clientSubscribe = "+/#"
	self.log = log.Clone(log2.LOG_INFO)
	if self.conf.LogDebug {
		self.log.SetLevel(log2.LOG_DEBUG)
	}
	self.pch = make(chan tele_api.Packet, 1)
	// self.Vmc = make(map[int32]vmcStruct)
	err := self.mqttInit(ctx, log)
	return errors.Annotate(err, "tele.Init")
}

func (self *tele) Close() error {
	self.log.Infof("mqtt unsubscribe")
	if token := self.m.Unsubscribe("#"); token.Wait() && token.Error() != nil {
		self.log.Infof("mqtt unsubscribe error")
		return token.Error()
	}
	return nil
}

func (self *tele) Addrs() []string {
	switch self.conf.Mode {
	case tele_config.ModeDisabled, tele_config.ModeClient:
		return nil
	case tele_config.ModeServer:
		self.RLock()
		defer self.RUnlock()
		// return self.mqttsrv.Addrs()
		return nil
	default:
		panic(self.msgInvalidMode())
	}
}

func (self *tele) Chan() <-chan tele_api.Packet { return self.pch }

func (self *tele) SendCommand(vmid int32, c *vender_api.Command) {
	payload, err := proto.Marshal(c)
	if err != nil {
		self.log.Errorf("tele.SendCommand marshal error(%v)", err)
		return
	}
	p := tele_api.Packet{Kind: tele_api.PacketCommand, VmId: vmid, Payload: payload}
	if err = self.mqttSend(p); err != nil {
		self.log.Errorf("tele.SendCommand mqtt send error(%v)", err)
		return
	}
	return
}

func (self *tele) CommandTx(vmid int32, c *vender_api.Command) {
	self.SendCommand(vmid, c)
}

func (self *tele) msgInvalidMode() string {
	return fmt.Sprintf("code error tele Config.Mode='%s'", self.conf.Mode)
}

func (t *tele) SendToRobo(vmid int32, m *vender_api.ToRoboMessage) {
	payload, err := proto.Marshal(m)
	if err != nil {
		t.log.Errorf("tele.SendCommand marshal error(%v)", err)
		return
	}
	t.mqttSendToRobo(vmid, payload)
}
