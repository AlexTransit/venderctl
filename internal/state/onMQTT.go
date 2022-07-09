package state

import (
	"github.com/AlexTransit/vender/tele"
	tele_api "github.com/AlexTransit/venderctl/internal/tele/api"
)

// OLD фиксирует подсоединение робота или парсит пакет на команду.
// fixes the connection of the robot or parses the packet on the command.
func (g *Global) ParseMqttPacket(p tele_api.Packet) *tele.Response {
	// vmcid := p.VmId
	// r := g.Vmc[vmcid]

	switch p.Kind {
	// case tele_api.PacketConnect:
	// 	r.Connect = ByteToBool(p.Payload)
	// 	g.Vmc[vmcid] = r
	// case tele_api.PacketState:
	// 	s, err := p.State()
	// 	if err != nil {
	// 		return nil
	// 	}
	// 	r.State = s
	// 	g.Vmc[vmcid] = r
	case tele_api.PacketCommandReply:
		return p.CommandResponseUnmarshal()

	default:
		return nil
	}
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
	if g.Vmc[p.VmId] == nil {
		g.Vmc[p.VmId] = &vmcStruct{}
	}
	switch p.Kind {
	case tele_api.PacketConnect:
		g.Vmc[p.VmId].Connect = ByteToBool(p.Payload)
		return nil
	case tele_api.FromRobo:
		rm := p.MessageFromRobotseUnmarshal()
		g.SetRoboState(p.VmId, rm.State)
		return rm
	default:
		return nil
	}
}

func (g *Global) RobotConnected(vmid int32) bool {
	if g.Vmc[vmid] == nil {
		g.Vmc[vmid] = &vmcStruct{}
	}
	return g.Vmc[vmid].Connect
}
