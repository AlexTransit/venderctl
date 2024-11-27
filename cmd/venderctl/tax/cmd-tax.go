// Sends tax information to state. Executes tax_job queue in DB.
package tax

// TODO maybe extract into separate Go module and/or code repository

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/AlexTransit/vender/log2"
	vender_api "github.com/AlexTransit/vender/tele"
	"github.com/AlexTransit/venderctl/cmd/internal/cli"
	"github.com/AlexTransit/venderctl/cmd/venderctl/tax/cashless_tinkoff"
	"github.com/AlexTransit/venderctl/internal/state"
	"github.com/coreos/go-systemd/daemon"
	"github.com/go-pg/pg/v9"
	"github.com/juju/errors"
	// tele_config "github.com/AlexTransit/venderctl/internal/tele/config"
)

const CmdName = "tax"

var Cmd = cli.Cmd{
	Name:   CmdName,
	Desc:   "execute tax jobs",
	Action: taxMain,
}

type taxStruct struct {
	*state.Global
}

var tax taxStruct

func taxMain(ctx context.Context, flags *flag.FlagSet) error {
	tax.Global = state.GetGlobal(ctx)

	configPath := flags.Lookup("config").Value.String()
	tax.Config = state.MustReadConfig(tax.Log, state.NewOsFullReader(), configPath)
	if tax.Config.Tax.DebugLevel < 1 || tax.Config.Tax.DebugLevel > 7 {
		tax.Log.SetLevel(log2.LOG_INFO)
	} else {
		tax.Log.SetLevel(log2.Level(tax.Config.Tax.DebugLevel))
	}
	tax.Config.Tele.SetMode("tax")
	if err := tax.Tele.Init(ctx, tax.Log, tax.Config.Tele); err != nil {
		return err
	}

	if err := tax.taxInit(); err != nil {
		return errors.Annotate(err, "taxInit")
	}
	return tax.taxLoop(ctx)
}

func (tax *taxStruct) taxInit() error {
	tax.InitVMC()

	if err := tax.InitDB(CmdName); err != nil {
		return errors.Annotate(err, "InitDB")
	}

	cli.SdNotify(daemon.SdNotifyReady)
	tax.Log.WarningF("taxInit complete")
	return nil
}

func (tax *taxStruct) taxLoop(ctx context.Context) error {
	tax.Log.Info("start cash receipt notifiner")
	defer tax.Log.Info("cash receipt notifiner stoped")
	const pollInterval = 53 * time.Second
	hostname, _ := os.Hostname()
	randomString := time.Now().Format("20060102-150405")
	worker := fmt.Sprintf("%s:%d:%s", hostname, os.Getpid(), randomString)

	llSched := tax.DB.Listen("tax_job_sched")
	defer llSched.Close()
	chSched := llSched.Channel()
	cashless_tinkoff.QrInit(ctx)

	db := tax.DB.Conn()
	try, err := tax.taxStep(ctx, db, worker)
	tax.Log.Debugf("taxStep try=%t err=%v", try, err)
	_ = db.Close()
	if err != nil {
		tax.Log.Error("taxStep try")
	}

	for {
		if !try {
			select {
			case <-chSched:
				tax.Log.Debugf("notified tax_job_sched")
			case <-time.After(pollInterval):
			case <-ctx.Done():
				return nil
			}
		}
		db = tax.DB.Conn()
		try, err = tax.taxStep(ctx, db, worker)
		tax.Log.Debugf("taxStep try=%t err=%v", try, err)
		_ = db.Close()
		if err != nil {
			tax.Log.Error("taxStep try")
			// g.Error(err)
		}
	}
}

type MTaxJob struct {
	Id        int64 `pg:",pk"`
	State     string
	Created   time.Time
	Modified  time.Time
	Scheduled time.Time
	Worker    string
	Processor string
	ExtId     string
	Ops       []TaxJobOp `pg:"ops"`
	Data      *TaxJobData
	Notes     []string `pg:",array"`
	Gross     int32
}

type TaxJobData struct {
	Ru2019 struct {
		OpTime  string `json:"optime,omitempty"`
		DocNum  uint32 `json:"docnum,omitempty"`
		DocType uint16 `json:"doctype,omitempty"`

		FSStatus struct {
			FSNum            string `json:"fsnum,omitempty"`
			CycleOpen        bool   `json:"cycle_open,omitempty"`
			LastDocNumber    uint32 `json:"last_doc,omitempty"`
			UnsentDocNumber  uint32 `json:"unsent_doc,omitempty"`
			OfflineDocsCount uint32 `json:"offline_docs,omitempty"`
		} `json:"fss,omitempty"`
	} `json:"ru2019,omitempty"`
}

type TaxJobOp struct {
	Time   string  `json:"time"`
	Name   string  `json:"name"`
	Code   string  `json:"code"`
	Amount float64 `json:"amount"`
	Price  uint32  `json:"price"`
	Vmid   int32   `json:"vmid"`

	Method vender_api.PaymentMethod `json:"method"`
}

func (d *TaxJobData) String() string {
	b, err := json.Marshal(d)
	if err != nil {
		return fmt.Sprintf("(TaxJobData.String err=%v)", err)
	}
	return string(b)
}

func (tj *MTaxJob) OpKeysString() string {
	if tj == nil {
		return ""
	}
	b := strings.Builder{}
	for _, op := range tj.Ops {
		b.WriteString(op.KeyString())
	}
	return b.String()
}

func (tj *MTaxJob) Update(conn *pg.Conn, assign string, params ...interface{}) error {
	conn = conn.
		WithParam("tj_id", tj.Id).
		WithParam("tj_state", tj.State).
		WithParam("tj_data", tj.Data).
		WithParam("tj_extid", tj.ExtId)
	const assignDefault = "state=?tj_state,data=?tj_data,ext_id=?tj_extid"
	if assign != "" {
		assign = "," + assign
	}
	q := fmt.Sprintf("update tax_job set %s%s where id=?tj_id and worker=?worker", assignDefault, assign)
	_, err := conn.ExecOne(q, params...)
	return err
}

func (tj *MTaxJob) UpdateFinal(conn *pg.Conn, note string) error {
	tj.State = "final"
	assign := ""
	var params []interface{}
	if note != "" {
		assign = "notes=array_append(notes,?0)"
		params = append(params, note)
	}
	return tj.Update(conn, assign, params...)
}

func (tj *MTaxJob) UpdateScheduleLater(conn *pg.Conn) error {
	tj.State = "sched"
	return tj.Update(conn, "scheduled=(current_timestamp + '5 second'::interval)")
}

func (o *TaxJobOp) KeyString() string {
	return fmt.Sprintf("vm=%d,time=%s,code=%s,name=%s", o.Vmid, o.Time, o.Code, o.Name)
}

// try to take next job in queue and process it
// error during taxProcess() changes state=help and logs error into tax_job.notes
func (tax *taxStruct) taxStep(ctx context.Context, db *pg.Conn, worker string) (bool, error) {
	db = db.WithParam("worker", worker)

	var tj MTaxJob
	// _, err := db.QueryOne(&tj, `select * from tax_job_take(?worker)`)
	_, err := db.QueryOneContext(ctx, &tj, `select * from tax_job_take(?worker)`)
	if err == pg.ErrNoRows {
		return false, nil
	} else if err != nil {
		return false, errors.Annotate(err, "tax_job_take")
	}
	tax.Log.Debugf("tj=%#v data=%s", tj, tj.Data.String())
	if err = taxProcess(ctx, db, &tj); err != nil {
		tj.State = "help"
		e := tj.Update(db, "notes=array_append(notes,?0)", "error:"+errors.Details(err))
		tax.Log.Error(e)
		return true, err
	}
	return true, nil
}

func taxProcess(ctx context.Context, db *pg.Conn, tj *MTaxJob) error {
	// Tax processor incapsulates actions required by some local law.
	switch tj.Processor {
	case procRu2019:
		return processRu2019(ctx, db, tj)
	default:
		return fmt.Errorf("unknown processor")
	}
}
