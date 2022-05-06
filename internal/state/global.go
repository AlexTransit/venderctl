package state

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/AlexTransit/vender/helpers"
	"github.com/go-pg/pg/v9"
	"github.com/juju/errors"
)

func (g *Global) InitVMC() {
	g.Vmc = make(map[int32]vmcStruct)
}

func GetGlobal(ctx context.Context) *Global {
	v := ctx.Value(ContextKey)
	if v == nil {
		panic(fmt.Sprintf("context['%s'] is nil", ContextKey))
	}
	if g, ok := v.(*Global); ok {
		return g
	}
	panic(fmt.Sprintf("context['%s'] expected type *Global actual=%#v", ContextKey, v))
}

func (g *Global) CtlStop(ctx context.Context) {
	g.Tele.Close()
	g.Log.Infof("venderctl stop")
	os.Exit(0)
}

func (g *Global) InitDB(cmdName string) error {
	pingTimeout := helpers.IntMillisecondDefault(g.Config.DB.PingTimeoutMs, 5*time.Second)

	dbOpt, err := pg.ParseURL(g.Config.DB.URL)
	if err != nil {
		cleanUrl, _ := url.Parse(g.Config.DB.URL)
		if cleanUrl.User != nil {
			cleanUrl.User = url.UserPassword("_hidden_", "_hidden_")
		}
		return errors.Annotatef(err, "config db.url=%s", cleanUrl.String())
	}
	dbOpt.MinIdleConns = 1
	dbOpt.IdleTimeout = -1
	dbOpt.IdleCheckFrequency = -1
	dbOpt.ApplicationName = "venderctl/" + cmdName
	// MaxRetries:1,
	// PoolSize:2,

	g.DB = pg.Connect(dbOpt)
	g.DB.AddQueryHook(queryHook{g})
	_, err = g.DB.WithTimeout(pingTimeout).Exec(`select 1`)
	return errors.Annotate(err, "db ping")
}

func (g *Global) VMCErrorWriteDB(vmid int32, vmtime int64, errCode uint32, message string){
	dbConn := g.DB.Conn().WithParam("vmid", vmid).WithParam("vmtime", vmtime)
	defer dbConn.Close()
	const q = `insert into error (vmid,vmtime,received,code,message) values (?vmid,to_timestamp(?vmtime),current_timestamp,?0,?1)`
	_, err := dbConn.Exec(q, errCode, message)
	if err != nil {
		g.Log.Errorf("error db query=%s \nerror=%v", q, err)
	}
}

func (g *Global) Error(err error, args ...interface{}) {
	if err != nil {
		if len(args) != 0 {
			msg := args[0].(string)
			args = args[1:]
			err = errors.Annotatef(err, msg, args...)
		}
		g.Log.Errorf(errors.ErrorStack(err))
	}
}

type queryHook struct{ g *Global }

func (q queryHook) BeforeQuery(ctx context.Context, e *pg.QueryEvent) (context.Context, error) {
	s, err := e.FormattedQuery()
	q.g.Log.Debugf("sql q=%s err=%v", s, err)
	return ctx, nil
}

func (queryHook) AfterQuery(context.Context, *pg.QueryEvent) error { return nil }

func ByteToInt8(b []byte) int8 {
	if len(b) != 0 {
		return int8(b[0])
	}
	return 0
}

func ByteToBool(b []byte) bool {
	if len(b) != 0 {
		if b[0] > 0 {
			return true
		}
	}
	return false
}
