package tax

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/AlexTransit/vender/tele"
	"github.com/AlexTransit/venderctl/internal/state"
	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v9"
	"github.com/nikita-vanyasin/tinkoff"
	"github.com/temoto/alive/v2"
)

var CashLess struct {
	Alive *alive.Alive
	g     *state.Global
}

var CashLessPay map[int32]*CashLessOrderStruct

type cashlessState uint8

const (
	CreatePaimentID cashlessState = iota
	CreateQR
	Paid
	Cooking
	Complete
)

type CashLessOrderStruct struct {
	Vmid          int32
	ClState       cashlessState
	PaymentID     string
	OrderID       string
	Amount        uint64
	Description   string
	Payer         string
	Date          time.Time
	ToRoboMessage *tele.ToRoboMessage
}

var terminalClient *tinkoff.Client
var terminalKey string
var terminalBankCommission, terminalMinimalAmount uint32

func CashLessInit(ctx context.Context) bool {
	CashLess.g = state.GetGlobal(ctx)
	CashLessPay = make(map[int32]*CashLessOrderStruct)
	if CashLess.g.Config.CashLess.TerminalTimeOutSec == 0 {
		CashLess.g.Config.CashLess.TerminalTimeOutSec = 30
	}
	terminalBankCommission = uint32(CashLess.g.Config.CashLess.TerminalBankCommission)
	if CashLess.g.Config.CashLess.TerminalMinimalAmount == 0 {
		terminalMinimalAmount = 1000
	} else {
		terminalMinimalAmount = uint32(CashLess.g.Config.CashLess.TerminalMinimalAmount)
	}
	if CashLess.g.Config.CashLess.TerminalQRPayRefreshSec == 0 {
		CashLess.g.Config.CashLess.TerminalQRPayRefreshSec = 3
	}
	if terminalKey = CashLess.g.Config.CashLess.TerminalKey; terminalKey == "" {
		CashLess.g.Log.Info("\033[41mtekminal key not foud. cashless system not start\033[0m")
		return false
	}
	if tp := CashLess.g.Config.CashLess.TerminalPass; tp == "" {
		CashLess.g.Log.Info("\033[41mtekminal password not foud. cashless system not start\033[0m")
		return false
	}
	terminalClient = tinkoff.NewClient(terminalKey, CashLess.g.Config.CashLess.TerminalPass)
	CashLess.Alive = alive.NewAlive()
	go startNotificationsReader()
	return true
}

func CashLessStop() {
	CashLess.Alive.Stop()
	CashLess.Alive.Wait()
	CashLess.g.Log.Debug("cashless system stoped ")
	CashLessPay = nil
}

func MakeQr(ctx context.Context, vmid int32, rm *tele.FromRoboMessage) {
	if o, ok := CashLessPay[vmid]; ok {
		if CashLessPay[vmid].PaymentID != "" {
			CashLess.g.Log.Errorf("new qr order, before old order clouse. vmid %d\nold order (%#v)", vmid, CashLessPay[vmid])
			o.cancelOrder()
			// return
		}
	}
	qro := CashLessOrderStruct{
		ToRoboMessage: &tele.ToRoboMessage{
			ShowQR: &tele.ShowQR{},
		},
	}
	qro.ToRoboMessage.Cmd = tele.MessageType_showQR
	defer func() {
		CashLess.g.Log.Infof("send message to robo(%d) message(%v)", vmid, qro.ToRoboMessage)
		CashLess.g.Tele.SendToRobo(vmid, qro.ToRoboMessage)
	}()
	if rm.Order.Amount < terminalMinimalAmount { // minimal bank amount
		CashLess.g.Log.Errorf("bank pay imposible. the amount is less than the minimum\n%#v", rm.Order.Amount)
		qro.ToRoboMessage.ShowQR.QrType = tele.ShowQR_error
		return
	}
	persentAmount := (rm.Order.Amount * terminalBankCommission) / 10000
	od := time.Now()
	qro.Vmid = vmid
	qro.OrderID = fmt.Sprintf("%d-%s-%s", vmid, od.Format("060102150405"), rm.Order.MenuCode)
	qro.Amount = uint64(rm.Order.Amount + persentAmount)
	qro.Date = od
	qro.Description = menuGetName(vmid, rm.Order.MenuCode)
	// 4 test -----------------------------------
	/*
		res := tinkoff.InitResponse{
			Amount:     qro.Amount,
			OrderID:    qro.OrderID,
			Status:     tinkoff.StatusNew,
			PaymentID:  od.Format("060102150405"),
			PaymentURL: "https://get.lost/world",
		}
		var err error
		// err := fmt.Errorf("AAA")
		/*/
	res, err := terminalClient.Init(&tinkoff.InitRequest{
		BaseRequest: tinkoff.BaseRequest{TerminalKey: CashLess.g.Config.CashLess.TerminalKey, Token: "random"},
		Amount:      qro.Amount,
		OrderID:     qro.OrderID,
		Description: qro.Description,
		Data:        map[string]string{"Vmc": fmt.Sprintf("%d", vmid)},
	})
	//*/
	if err != nil || res.Status != tinkoff.StatusNew {
		CashLess.g.Log.Errorf("bank pay init error:%v", err)
		qro.ToRoboMessage.ShowQR.QrType = tele.ShowQR_error
		return
	}
	qro.PaymentID = res.PaymentID
	// 4 test -----------------------------------
	/*
		pidi, _ := strconv.Atoi(res.PaymentID)
		qrr := tinkoff.GetQRResponse{
			OrderID:   qro.OrderID,
			Data:      "TEST qr code for pay",
			PaymentID: pidi,
		}
		/*/
	qrr, err := terminalClient.GetQR(&tinkoff.GetQRRequest{
		PaymentID: qro.PaymentID,
	})
	//*/
	if err != nil {
		CashLess.g.Log.Errorf("bank get QR error:%v", err)
		qro.ToRoboMessage.ShowQR.QrType = tele.ShowQR_error
		return
	}
	qro.ToRoboMessage.ShowQR.QrText = qrr.Data
	qro.ToRoboMessage.ShowQR.QrType = tele.ShowQR_order
	qro.ToRoboMessage.ShowQR.DataInt = int32(qro.Amount)
	qro.ClState = CreateQR
	// CashLessPay[vmid] = new(CashLessOrderStruct)
	CashLessPay[vmid] = &qro
	qro.qrWrite()
	go qro.waitingForPayment()
}

func menuGetName(vmid int32, code string) string {
	var cname string
	_, err := CashLess.g.DB.QueryOne(pg.Scan(&cname),
		`SELECT name from CATALOG WHERE vmid= ?0 and code = ?1 limit 1;`,
		vmid, code)
	if err == nil && cname != "" {
		return cname
	}
	return fmt.Sprintf("#%s", code)
}

func (o *CashLessOrderStruct) qrWrite() {
	const q = `INSERT INTO cashless (vmid, create_date, payment_id, order_id, amount, terminal) VALUES ( ?0, ?1, ?2, ?3, ?4, ?5 );`
	_, err := CashLess.g.DB.Exec(q, o.Vmid, o.Date, o.PaymentID, o.OrderID, o.Amount, terminalKey)
	if err != nil {
		CashLess.g.Log.Errorf("qr db write:%v", err)
	}
}

func (o *CashLessOrderStruct) waitingForPayment() {
	CashLess.Alive.Add(1)
	tmr := time.NewTimer(time.Second * time.Duration(CashLess.g.Config.CashLess.TerminalTimeOutSec))
	refreshTime := time.Duration(time.Second * time.Duration(CashLess.g.Config.CashLess.TerminalQRPayRefreshSec))
	refreshTimer := time.NewTimer(refreshTime)
	defer func() {
		tmr.Stop()
		refreshTimer.Stop()
		CashLess.Alive.Done()
	}()
	for {
		select {
		case <-tmr.C:
			CashLess.g.Log.Infof("order cancel by timeout ")
			o.cancelOrder()
			return
		case <-CashLess.Alive.StopChan():
			o.cancelOrder()
		case <-refreshTimer.C:
			if o.ClState >= Paid {
				return
			}
			// 4 test -----------------------------------
			/*
				if true {
					var s tinkoff.GetStateResponse
					s.Status = tinkoff.StatusConfirmed
					var err error
					/*/
			if s, err := terminalClient.GetState(&tinkoff.GetStateRequest{PaymentID: o.PaymentID}); err == nil {
				//*/
				if err != nil {
					o.cancelOrder()
					CashLess.g.Log.Errorf("cashless get status:", err)
					return
				}
				switch s.Status {
				case tinkoff.StatusConfirmed:
					if o.ClState >= Paid {
						return
					}
					CashLess.g.Log.Infof("write db confirmed wait pay loop. order(%v) ", o)
					o.writeDBOrderPaid()
					return
				case tinkoff.StatusRejected:
					o.bankQRReject()
					return
				case tinkoff.StatusCanceled:
					return
				default:
				}

				refreshTimer.Reset(refreshTime)
			}
		}
	}
}

func (o *CashLessOrderStruct) bankQRReject() {
	sm := tele.ToRoboMessage{
		Cmd: tele.MessageType_showQR,
		ShowQR: &tele.ShowQR{
			QrType: tele.ShowQR_errorOverdraft,
		},
	}
	CashLess.g.Tele.SendToRobo(o.Vmid, &sm)
}

func (o *CashLessOrderStruct) sendStartCook() {
	sm := tele.ToRoboMessage{
		ServerTime: time.Now().Unix(),
		Cmd:        tele.MessageType_makeOrder,
		MakeOrder: &tele.Order{
			Amount:        uint32(o.Amount),
			OrderStatus:   tele.OrderStatus_doSelected,
			PaymentMethod: tele.PaymentMethod_Cashless,
			OwnerStr:      o.PaymentID,
			OwnerType:     tele.OwnerType_qrCashLessUser,
		},
	}
	CashLess.g.Tele.SendToRobo(o.Vmid, &sm)
}

func (o *CashLessOrderStruct) cancelOrder() {
	CashLess.g.Log.Debugf("cancel order:%v ", o)
	cReq := &tinkoff.CancelRequest{
		PaymentID: o.PaymentID,
		Amount:    o.Amount,
	}
	cRes, err := terminalClient.Cancel(cReq)
	q := `UPDATE cashless SET state = 'order_cancel', finish_date = now() WHERE payment_id = ?0 and vmid = ?1;`
	switch cRes.Status {
	case tinkoff.StatusQRRefunding:
		q = `UPDATE cashless SET state = 'order_cancel', finish_date = now(), credited = 0 WHERE payment_id = ?0 and vmid = ?1;`
	default:
		errm := fmt.Sprintf("tinkoff fail cancel order (%v) error:%v", o, err)
		if o.ClState >= Paid {
			CashLess.g.VMCErrorWriteDB(o.Vmid, time.Now().Unix(), 0, errm)
		}
	}

	r, err := CashLess.g.DB.Exec(q, o.PaymentID, o.Vmid)
	if err != nil || r.RowsAffected() != 1 {
		CashLess.g.Log.Errorf("fail db update:%v", err)
	}
	if CashLess.g.Vmc[o.Vmid].State == tele.State_WaitingForExternalPayment {
		o.ToRoboMessage.ShowQR = &tele.ShowQR{}
		o.ToRoboMessage.ShowQR.QrType = tele.ShowQR_error
		CashLess.g.Tele.SendToRobo(o.Vmid, o.ToRoboMessage)
	}
	delete(CashLessPay, o.Vmid)
}

func (o *CashLessOrderStruct) writeDBOrderPaid() {
	if o.ClState >= Paid {
		CashLess.g.Log.Errorf("error: order paided.")
		return
	}
	o.ClState = Paid
	const q = `UPDATE cashless SET state = 'order_prepay', credit_date = now(), credited = ?2, payer = ?3 WHERE payment_id = ?0 and order_id = ?1;`
	r, err := CashLess.g.DB.Exec(q, o.PaymentID, o.OrderID, o.Amount, o.Payer)
	if err != nil || r.RowsAffected() != 1 {
		CashLess.g.Log.Errorf("fail db update:%v", err)
	}
	o.sendStartCook()
}

func (o *CashLessOrderStruct) writeDBOrderComplete() {
	CashLess.g.Log.Infof("order complete (%v)", o)
	const q = `UPDATE cashless SET state = 'order_complete', finish_date = now() WHERE payment_id = ?0 and vmid = ?1;`
	r, err := CashLess.g.DB.Exec(q, o.PaymentID, o.Vmid)
	rn := r.RowsAffected()
	if err != nil || rn != 1 {
		CashLess.g.Log.Errorf("fail db update/ records(%d)  error:%v", rn, err)
	}
	delete(CashLessPay, o.Vmid)
}

func startNotificationsReader() {
	r := gin.Default()
	// gin.SetMode(gin.ReleaseMode)

	r.POST("/payment/notification/tinkoff", func(c *gin.Context) {
		/*
			n := tinkoff.Notification{
				TerminalKey: terminalKey,
				OrderID:     "88-22042715000-10",
				Success:     true,
				Status:      "CONFIRMED",
				PaymentID:   123,
				Amount:      1004,
				PAN:         "+7 999 999 99 99",
			}
			var err error
			/*/
		n, err := terminalClient.ParseNotification(c.Request.Body)
		CashLess.g.Log.Infof("notification from bank(%v)", n)
		//*/
		if err != nil {
			CashLess.g.Log.Errorf("notification(%v) parse error(%v)", n, err)
			return
		}
		c.String(http.StatusOK, terminalClient.GetNotificationSuccessResponse())
		if n.Status == tinkoff.StatusConfirmed {
			oid := n.OrderID
			tvmid := oid[:strings.Index(oid, "-")]
			vm, _ := strconv.ParseInt(tvmid, 10, 32)
			o := CashLessPay[int32(vm)]
			// if o == nil || o.OrderID != n.OrderID || o.Amount != n.Amount {
			if o == nil || o.Amount != n.Amount {
				CashLess.g.Log.Errorf("bank notification complete order(%v) notification:%n", o, n)
				return
			}
			o.Payer = n.PAN
			CashLess.g.Log.Infof("write db confirmed notification from bank(%v), order(%v) ", n, o)
			o.writeDBOrderPaid()
		}
	})
	err := r.Run(":8080")
	CashLess.g.Log.Errorf("error start notification server. error:%v", err)
}
