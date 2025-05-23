package control

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	// "time"

	vender_api "github.com/AlexTransit/vender/tele"
	"github.com/AlexTransit/venderctl/cmd/internal/cli"
	"github.com/AlexTransit/venderctl/internal/state"
	"github.com/juju/errors"
	//	tele_config "github.com/AlexTransit/venderctl/internal/tele/config"
)

// const replyTimeout = 51 * time.Second

// TODO set-inventory
// TODO get-config
// TODO set-config
// const cmdUsage = "MACHINE-ID {report | ping | set-inventory | get-config | set-config | exec SCENARIO... | lock DURATION}"
const cmdUsage = "MACHINE-ID {report | ping | exec SCENARIO... | lock DURATION | qr }"

var Cmd = cli.Cmd{
	Name:   "control",
	Desc:   "send commands to vending machines",
	Usage:  cmdUsage,
	Action: controlMain,
}

func controlMain(ctx context.Context, flags *flag.FlagSet) error {
	const argOffset = 1 // Arg(0)=.Name
	var targetId int32
	target := flags.Arg(argOffset)
	cmd := flags.Arg(argOffset + 1)
	g := state.GetGlobal(ctx)
	g.Log.Debugf("target=%s cmd=%s", target, cmd)
	if target == "" {
		flags.Usage()
		os.Exit(1)
	}
	if x, err := strconv.ParseInt(target, 10, 32); err != nil {
		return errors.Annotatef(err, "invalid target=%s", target)
	} else {
		targetId = int32(x)
	}

	configPath := flags.Lookup("config").Value.String()
	g.Config = state.MustReadConfig(g.Log, state.NewOsFullReader(), configPath)
	g.Config.Tele.SetMode("command")
	if err := g.Tele.Init(ctx, g.Log, g.Config.Tele); err != nil {
		return err
	}
	defer g.Tele.Close()

	switch cmd {
	case "report":
		cmd := &vender_api.Command{
			Executer:             0,
			Lock:                 false,
			Task:                 &vender_api.Command_Report{Report: &vender_api.Command_ArgReport{}},
			XXX_NoUnkeyedLiteral: struct{}{},
			XXX_unrecognized:     []byte{},
			XXX_sizecache:        0,
		}
		g.Tele.CommandTx(targetId, cmd)
		return nil

	case "ping":
		cmd := &vender_api.Command{
			Task: &vender_api.Command_Exec{Exec: &vender_api.Command_ArgExec{
				Scenario: "",
				// Lock:     false,
			}},
		}
		g.Tele.CommandTx(targetId, cmd)
		return nil

	case "set-inventory":
		g.Log.Fatal("TODO send set-inventory, show response")
		return nil

	case "get-config":
		// cli.StringFlag{Name: "name", Required: true},
		return nil
	case "set-config":
		// cli.StringFlag{Name: "name", Required: true},
		// cli.StringFlag{Name: "file", Required: true},
		return nil

	case "exec":
		scenario := strings.Join(flags.Args()[argOffset+2:], " ")
		cmd := &vender_api.Command{
			Task: &vender_api.Command_Exec{Exec: &vender_api.Command_ArgExec{Scenario: scenario}},
		}
		g.Tele.CommandTx(targetId, cmd)
		return nil

	case "qr":
		qrText := flags.Arg(argOffset + 2)
		cmd := &vender_api.Command{
			// Deadline: time.Now().Add(2 * time.Minute).UnixNano(),
			Task: &vender_api.Command_Show_QR{Show_QR: &vender_api.Command_ArgShowQR{QrText: qrText}},
		}
		g.Tele.CommandTx(targetId, cmd)
		return nil

	default:
		flags.Usage()
		return fmt.Errorf("unknown control command=%s", cmd)
	}
}
