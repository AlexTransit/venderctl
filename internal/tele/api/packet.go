package tele_api

import (
	"fmt"

	vender_api "github.com/AlexTransit/vender/tele"
	"github.com/golang/protobuf/proto"
	"github.com/juju/errors"
)

//go:generate go run golang.org/x/tools/cmd/stringer -type=PacketKind -trimprefix=Packet
type PacketKind uint8

const (
	PacketInvalid PacketKind = iota
	PacketConnect
	PacketState
	PacketTelemetry
	PacketCommand
	PacketCommandReply
	PacketCommandAccepted
	FromRoboPacketCommandDone
	FromRobo
)

type Packet struct {
	Payload []byte
	VmId    int32
	Kind    PacketKind
}

func (p *Packet) String() string {
	return fmt.Sprintf("tele.Packet(Kind=%s VmId=%d Payload=%x)", p.Kind.String(), p.VmId, p.Payload)
}

func (p *Packet) CommandResponseUnmarshal() *vender_api.Response {
	r := vender_api.Response{}
	err := proto.Unmarshal(p.Payload, &r)
	if err != nil {
		fmt.Printf("CommandResponseUnmarshal error:(%v)", err)
		return nil
	}
	return &r
}

func (p *Packet) MessageFromRobotseUnmarshal() *vender_api.FromRoboMessage {
	r := vender_api.FromRoboMessage{}
	err := proto.Unmarshal(p.Payload, &r)
	if err != nil {
		fmt.Printf("CommandResponseUnmarshal error:(%v)", err)
		return nil
	}
	return &r
}

func (p *Packet) State() (vender_api.State, error) {
	if len(p.Payload) == 1 {
		s := vender_api.State(p.Payload[0])
		if _, ok := vender_api.State_name[int32(s)]; ok {
			return s, nil
		}
	}
	return vender_api.State_Invalid, errors.Errorf("tele invalid state payload='%x'", p.Payload)
}

func (p *Packet) Telemetry() (*vender_api.Telemetry, error) {
	t := vender_api.Telemetry{}
	err := proto.Unmarshal(p.Payload, &t)
	if err != nil {
		err = errors.Annotatef(err, "raw=%x", p.Payload)
		return nil, err
	}
	return &t, err
}
