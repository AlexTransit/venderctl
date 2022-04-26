// Sponge job is to listen network for incoming telemetry and save into database.
package sponge

import (
	"context"
	"flag"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/AlexTransit/vender/helpers"
	vender_api "github.com/AlexTransit/vender/tele"
	"github.com/AlexTransit/venderctl/cmd/internal/cli"
	"github.com/AlexTransit/venderctl/internal/state"
	tele_api "github.com/AlexTransit/venderctl/internal/tele/api"
	"github.com/coreos/go-systemd/daemon"
	"github.com/go-pg/pg/v9"
	pg_types "github.com/go-pg/pg/v9/types"
	"github.com/golang/protobuf/proto"
	"github.com/juju/errors"
)

const CmdName = "sponge"

var Cmd = cli.Cmd{
	Name:   CmdName,
	Desc:   "telemetry network via external broker -> save to database",
	Action: spongeMain,
}

func spongeMain(ctx context.Context, flags *flag.FlagSet) error {
	g := state.GetGlobal(ctx)
	g.InitVMC()
	configPath := flags.Lookup("config").Value.String()
	g.Config = state.MustReadConfig(g.Log, state.NewOsFullReader(), configPath)
	g.Config.Tele.SetMode("sponge")

	//	g.Config.Tele.SetMode("sponge")
	// if err := g.Config.Tele.EnableClient(tele_config.RoleControl); err != nil {
	// 	return err
	// }

	// config.Tele.Enable = true
	// config.Tele.MqttSubscribe = []string{"+/w/+"}
	// g.MustInit(ctx, config)
	g.Log.Debugf("config=%+v", g.Config)

	// if err := app.init(ctx); err != nil {
	// 	return errors.Annotate(err, "app.init")
	// }

	if err := spongeInit(ctx); err != nil {
		return errors.Annotate(err, "spongeInit")
	}
	return spongeLoop(ctx)
}

// runtime irrelevant in Global
// type appSponge struct {
// 	g *state.Global
// }

func spongeInit(ctx context.Context) error {
	g := state.GetGlobal(ctx)
	if err := g.InitDB(CmdName); err != nil {
		return errors.Annotate(err, "sponge init")
	}

	if err := g.Tele.Init(ctx, g.Log, g.Config.Tele); err != nil {
		return errors.Annotate(err, "Tele.Init")
	}

	cli.SdNotify(daemon.SdNotifyReady)
	g.Log.Debugf("sponge init complete")
	return nil
}

func spongeLoop(ctx context.Context) error {
	g := state.GetGlobal(ctx)
	ch := g.Tele.Chan()
	stopch := g.Alive.StopChan()

	// ll := g.DB.Listen("trans")
	// defer ll.Close()

	for {
		select {
		case p := <-ch:
			// g.Log.Debugf("sponge packet=%s", p.String())
			g.Alive.Add(1)
			var err error
			// старый и новый обработчик
			if p.Kind == tele_api.FromRobo || p.Kind == tele_api.PacketConnect {
				packetFromRobo(ctx, p)
			} else {
				err = onPacket(ctx, p)
			}
			defer g.Alive.Done()
			if err != nil {
				g.Log.Error(errors.ErrorStack(err))
			}

		case <-stopch:
			return nil
		}
	}
}

func onPacket(ctx context.Context, p tele_api.Packet) error {
	// ignore some packets
	switch p.Kind {
	case tele_api.PacketCommandReply:
		return nil
	}

	g := state.GetGlobal(ctx)
	dbConn := g.DB.Conn()
	defer dbConn.Close()

	switch p.Kind {

	case tele_api.PacketConnect:
		r := g.Vmc[p.VmId]
		c := true
		if p.Payload[0] == 0 {
			c = false
		}
		r.Connect = c
		g.Vmc[p.VmId] = r
		return onConnect(ctx, dbConn, p.VmId)

	case tele_api.PacketState:
		s, err := p.State()
		if err != nil {
			return err
		}
		// r := g.Vmc[p.VmId]
		// r.State = s
		// g.Vmc[p.VmId] = r
		return onState(ctx, dbConn, p.VmId, s)

	case tele_api.PacketTelemetry:
		t, err := p.Telemetry()
		if err != nil {
			return err
		}
		g.Log.Debugf("sponge packet PacketTelemetry = %s", t.String())
		return onTelemetry(ctx, dbConn, p.VmId, t)

	default:
		// return errors.Errorf("code error invalid packet=%v", p)
		return nil
	}
}

func onState(ctx context.Context, dbConn *pg.Conn, vmid int32, s vender_api.State) error {
	g := state.GetGlobal(ctx)
	g.Log.Infof("vm=%d state=%s", vmid, s.String())

	dbConn = dbConn.WithParam("vmid", vmid).WithParam("state", s)
	var oldState vender_api.State
	_, err := dbConn.Query(pg.Scan(&oldState), `select state_update(?vmid, ?state)`)
	err = errors.Annotatef(err, "db state_update")
	// g.Log.Infof("vm=%d old_state=%s", vmid, old_state.String())

	if g.Config.Tele.ExecOnState != "" {
		// Exec user supplied program is potential security issue.
		// TODO explore hardening options like sudo
		cmd := exec.Command(g.Config.Tele.ExecOnState) //nolint:gosec
		cmd.Env = []string{
			fmt.Sprintf("db_updated=%t", err == nil),
			fmt.Sprintf("vmid=%d", vmid),
			fmt.Sprintf("new=%d", s),
			fmt.Sprintf("prev=%d", oldState),
		}
		g.Alive.Add(1)
		go func() {
			defer g.Alive.Done()
			execOutput, execErr := cmd.CombinedOutput()
			prettyEnv := strings.Join(cmd.Env, " ")
			if execErr != nil {
				execErr = errors.Annotatef(execErr, "exec_on_state %s %s output=%s", prettyEnv, cmd.Path, execOutput)
				g.Log.Error(execErr)
			}
		}()
	}

	return err
}

func onTelemetry(ctx context.Context, dbConn *pg.Conn, vmid int32, t *vender_api.Telemetry) error {
	g := state.GetGlobal(ctx)
	g.Log.Infof("vm=%d telemetry=%s", vmid, t.String())

	dbConn = dbConn.WithParam("vmid", vmid).WithParam("vmtime", t.Time)

	errs := make([]error, 0)
	if t.Error != nil {
		const q = `insert into error (vmid,vmtime,received,app_version,code,message,count) values (?vmid,to_timestamp(?vmtime/1e9),current_timestamp,?0,?1,?2,?3)`
		// AlexM set BuildVersion after connect
		// _, err := dbConn.Exec(q, t.BuildVersion, t.Error.Code, t.Error.Message, t.Error.Count)

		_, err := dbConn.Exec(q, "", t.Error.Code, t.Error.Message, t.Error.Count)
		if err != nil {
			errs = append(errs, errors.Annotatef(err, "db query=%s t=%s", q, proto.CompactTextString(t)))
		}
	}

	if t.Transaction != nil {
		const q = `insert into trans (vmid,vmtime,received,menu_code,options,price,method,executer) values (?vmid,to_timestamp(?vmtime/1e9),current_timestamp,?0,?1,?2,?3,?4)
on conflict (vmid,vmtime) do nothing`
		_, err := dbConn.Exec(q, t.Transaction.Code, pg.Array(t.Transaction.Options), t.Transaction.Price, t.Transaction.PaymentMethod, t.Transaction.Executer)
		if err != nil {
			errs = append(errs, errors.Annotatef(err, "db query=%s t=%s", q, proto.CompactTextString(t)))
		}
	}

	if t.Inventory != nil || t.MoneyCashbox != nil {
		const q = `insert into inventory (vmid,at_service,vmtime,received,inventory,cashbox_bill,cashbox_coin,change_bill,change_coin) values (?vmid,?0,to_timestamp(?vmtime/1e9),current_timestamp,?1,?2,?3,?4,?5)
on conflict (vmid) where at_service=?0 do update set
  vmtime=excluded.vmtime,received=excluded.received,inventory=excluded.inventory,
	cashbox_bill=excluded.cashbox_bill,cashbox_coin=excluded.cashbox_coin,
	change_bill=excluded.change_bill,change_coin=excluded.change_coin`
		invMap := make(map[string]string)
		var cashboxBillMap map[uint32]uint32
		var cashboxCoinMap map[uint32]uint32
		var changeBillMap map[uint32]uint32
		var changeCoinMap map[uint32]uint32
		if t.MoneyCashbox != nil {
			cashboxBillMap = t.MoneyCashbox.Bills
			cashboxCoinMap = t.MoneyCashbox.Coins
		}
		if t.MoneyChange != nil {
			changeBillMap = t.MoneyChange.Bills
			changeCoinMap = t.MoneyChange.Coins
		}
		if t.Inventory != nil {
			for _, item := range t.Inventory.Stocks {
				invMap[item.Name] = strconv.FormatFloat(float64(item.Valuef), 'f', -1, 32)
			}
		}
		_, err := dbConn.Exec(q, t.GetAtService(), pg.Hstore(invMap),
			mapUint32ToHstore(cashboxBillMap),
			mapUint32ToHstore(cashboxCoinMap),
			mapUint32ToHstore(changeBillMap),
			mapUint32ToHstore(changeCoinMap),
		)
		if err != nil {
			errs = append(errs, errors.Annotatef(err, "db query=%s t=%s", q, proto.CompactTextString(t)))
		}
	}

	const q = `insert into ingest (received,vmid,done,raw) values (current_timestamp,?vmid,?0,?1)`
	raw, _ := proto.Marshal(t)
	_, err := dbConn.Exec(q, len(errs) == 0, raw)
	if err != nil {
		errs = append(errs, errors.Annotatef(err, "db query=%s t=%s", q, proto.CompactTextString(t)))
	}
	return helpers.FoldErrors(errs)
}

func mapUint32ToHstore(from map[uint32]uint32) *pg_types.Hstore {
	m := make(map[string]string, len(from))
	for k, v := range from {
		m[strconv.FormatInt(int64(k), 10)] = strconv.FormatInt(int64(v), 10)
	}
	return pg.Hstore(m)
}

//---------------------

func packetFromRobo(ctx context.Context, p tele_api.Packet) {
	g := state.GetGlobal(ctx)
	rm := g.ParseFromRobo(p)
	dbConn := g.DB.Conn()
	defer dbConn.Close()
	if p.Kind == tele_api.PacketConnect {
		_ = onConnect(ctx, dbConn, p.VmId)
		return
	}
	rTime := time.Now().Unix()
	// rTime := time.Now()
	if rm.RoboTime != 0 {
		rTime = rm.RoboTime
	}
	if rm.State != 0 {
		s := rm.State
		onStateN(ctx, dbConn, p.VmId, s)
	}

	dbConn = dbConn.WithParam("vmid", p.VmId).WithParam("vmtime", rTime)
	if rm.Order == nil {
		return
	}
	if rm.Order.OrderStatus == vender_api.OrderStatus_complete {
		o := rm.Order
		const q = `insert into trans (vmid,vmtime,received,menu_code,options,price,method,executer,executer_str) 
		values (?vmid,to_timestamp(?vmtime),current_timestamp,?0,?1,?2,?3,?4,?5)
		on conflict (vmid,vmtime) do nothing`

		_, err := dbConn.Exec(q, o.MenuCode, pg.Array([]int8{state.ByteToInt8(o.Cream), state.ByteToInt8(o.Sugar)}), o.Amount, o.PaymentMethod, o.OwnerInt, o.OwnerStr)
		if err != nil {
			g.Log.Errorf("error db query=%s \nerror=%v", q, err)
		}
	}
}

func onStateN(ctx context.Context, dbConn *pg.Conn, vmid int32, s vender_api.State) {
	g := state.GetGlobal(ctx)
	g.Log.Infof("vm=%d state=%s", vmid, s.String())

	dbConn = dbConn.WithParam("vmid", vmid).WithParam("state", s)
	var oldState vender_api.State
	_, _ = dbConn.Query(pg.Scan(&oldState), `select state_update(?vmid, ?state)`)
}

func onConnect(ctx context.Context, dbConn *pg.Conn, vmid int32) error {
	g := state.GetGlobal(ctx)
	connect := g.Vmc[vmid].Connect
	g.Log.Infof("vm=%d connect=%t", vmid, connect)
	dbConn = dbConn.WithParam("vmid", vmid).WithParam("connect", connect)
	var nn bool
	_, err := dbConn.Query(pg.Scan(&nn), "select connect_update(?vmid, ?connect)")
	err = errors.Annotatef(err, "db connect_update")
	return err
}
