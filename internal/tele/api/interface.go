package tele_api

import (
	"context"

	"github.com/AlexTransit/vender/log2"
	vender_api "github.com/AlexTransit/vender/tele"
	tele_config "github.com/AlexTransit/venderctl/internal/tele/config"
)

type Teler interface {
	Init(context.Context, *log2.Log, tele_config.Config) error
	Close() error
	Addrs() []string
	Chan() <-chan Packet
	CommandTx(vmid int32, c *vender_api.Command)
	SendCommand(vmid int32, c *vender_api.Command)
}

type stub struct{}

func NewStub() Teler { return stub{} }

func (stub) Init(context.Context, *log2.Log, tele_config.Config) error { return nil }

func (stub) Close() error { return nil }

func (stub) Addrs() []string { return nil }

func (stub) Chan() <-chan Packet { return nil }

func (stub) CommandTx(vmid int32, c *vender_api.Command)   {}
func (stub) SendCommand(vmid int32, c *vender_api.Command) {}
