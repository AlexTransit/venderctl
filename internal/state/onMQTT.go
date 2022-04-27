package state

import (
	"github.com/AlexTransit/vender/tele"
	tele_api "github.com/AlexTransit/venderctl/internal/tele/api"
)

// OLD фиксирует подсоединение робота или парсит пакет на команду.
// fixes the connection of the robot or parses the packet on the command.
func (g *Global) ParseMqttPacket(p tele_api.Packet) *tele.Response {
	vmcid := p.VmId
	r := g.Vmc[vmcid]

	switch p.Kind {
	case tele_api.PacketConnect:
		c := false
		if p.Payload[0] == 1 {
			c = true
		}
		r.Connect = c
		g.Vmc[vmcid] = r
	case tele_api.PacketState:
		s, err := p.State()
		if err != nil {
			return nil
		}
		r.State = s
		g.Vmc[vmcid] = r
	case tele_api.PacketCommandReply:
		return p.CommandResponseUnmarshal()

	default:
		return nil
	}
	return nil
}

func (g *Global) ItsWaitPayment(p tele_api.Packet) (bool, *tele.Response) {

	rp := g.ParseMqttPacket(p)
	if rp == nil {
		return false, nil
	}
	if rp.CookReplay == tele.CookReplay_waitPay {
		return true, rp
	}
	return false, nil
}

//---------------------

// фиксирует подсоединение робота или парсит пакет на команду.
// fixes the connection of the robot or parses the packet on the command.
func (g *Global) ParseFromRobo(p tele_api.Packet) *tele.FromRoboMessage {
	switch p.Kind {
	case tele_api.PacketConnect:
		r := g.Vmc[p.VmId]
		if p.Payload[0] == 0 {
			r.Connect = false
		} else {
			r.Connect = true
		}
		g.Vmc[p.VmId] = r
		return nil
	case tele_api.FromRobo:
		rm := p.MessageFromRobotseUnmarshal()
		return rm
	}
	return nil
}
