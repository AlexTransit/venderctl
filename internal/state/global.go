package state

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"time"

	"github.com/AlexTransit/vender/helpers"
	vender_api "github.com/AlexTransit/vender/tele"
	"github.com/go-pg/pg/v9"
	"github.com/juju/errors"
)

func (g *Global) InitVMC() {
	g.Vmc = make(map[int32]*vmcStruct)
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

func (g *Global) GetRoboState(vmid int32) vender_api.State {
	if g.Vmc[vmid].State == vender_api.State_Invalid {
		g.SetRoboState(vmid, g.ReadRoboStateFromDB(vmid))
	}
	return g.Vmc[vmid].State
}

func (g *Global) ReadRoboStateFromDB(vmid int32) vender_api.State {
	var roboState int32
	_, _ = g.DB.QueryOne(pg.Scan(&roboState),
		`SELECT state FROM state WHERE state.vmid = ?0 and state.connected = true limit 1;`,
		vmid)
	return vender_api.State(roboState)
}

func (g *Global) SetRoboState(vmid int32, st vender_api.State) {
	g.Vmc[vmid].State = st
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

	g.DB = pg.Connect(dbOpt).WithTimeout(pingTimeout)
	g.DB.AddQueryHook(queryHook{g})
	_, err = g.DB.Exec(`select 1`)
	return errors.Annotate(err, "db ping")
}

// сохраняет ошибку в базу с маркировкой не просмотрено.
// saves the error to the base marked not viewed.
// level - указать какая функция вызвала эту функцию.
func (g *Global) VMCErrorWriteDb(vmid int32, message string, level ...int) {
	skip := 1
	if level != nil {
		skip = level[0]
	}
	_, file, no, ok := runtime.Caller(skip)
	if ok {
		file = fmt.Sprintf("caller %s:%d", filepath.Base(file), no)
	}
	g.Log.Errorf("%s vm(%d) write error to db. err(%v)", file, vmid, message)
	dbConn := g.DB.Conn().WithParam("vmid", vmid)
	defer dbConn.Close()
	var ver string
	if g.Vmc[vmid] != nil {
		ver = g.Vmc[vmid].Version
	}
	const q = `insert into error (vmid,received,message,app_version) values (?vmid,current_timestamp,?0,?1)`
	_, err := dbConn.Exec(q, message, ver)
	if err != nil {
		g.Log.Errorf("error db query=%s error=%v", q, err)
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
	// q.g.Log.DebugLevelf(5, "sql q=%s err=%v", s, err)
	// q.g.Log.DebugLevelf(2, "2sql q=%s err=%v", s, err)
	// q.g.Log.DebugLevelf(3, "3sql q=%s err=%v", s, err)
	// q.g.Log.DebugLevelf(4, "4sql q=%s err=%v", s, err)
	// q.g.Log.DebugLevelf(5, "5sql q=%s err=%v", s, err)
	// q.g.Log.DebugLevelf(6, "6sql q=%s err=%v", s, err)
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
