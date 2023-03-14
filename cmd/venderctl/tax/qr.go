package tax

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
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

type orderState int

const (
	order_invalid orderState = iota
	order_start
	order_prepay
	order_execute
	order_complete
	order_cancel
)

type CashLessOrderStruct struct {
	Order_state   orderState
	Vmid          int32
	Payment_id    string
	Paymentid     int64
	Order_id      string
	Amount        uint64
	Payer         string
	Create_date   time.Time
	Description   string
	ToRoboMessage *tele.ToRoboMessage
}

var terminalClient *tinkoff.Client
var terminalKey string
var terminalBankCommission, terminalMinimalAmount uint32

func CashLessInit(ctx context.Context) {
	CashLess.g = state.GetGlobal(ctx)
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
	go cashLessLoop(ctx)
	if terminalKey = CashLess.g.Config.CashLess.TerminalKey; terminalKey == "" {
		CashLess.g.Log.Info("tekminal key not foud. cashless system not start.")
		return
	}
	if tp := CashLess.g.Config.CashLess.TerminalPass; tp == "" {
		terminalKey = ""
		CashLess.g.Log.Info("tekminal password not foud. cashless system not start.")
		return
	}
	terminalClient = tinkoff.NewClient(terminalKey, CashLess.g.Config.CashLess.TerminalPass)
	CashLess.Alive = alive.NewAlive()
	go startNotificationsReader(CashLess.g.Config.CashLess.URLToListenToBankNotifications)
}

func CashLessStop() {
	CashLess.Alive.Stop()
	CashLess.Alive.Wait()
	CashLess.g.Log.Debug("cashless system stoped ")
}

func MakeQr(ctx context.Context, vmid int32, rm *tele.FromRoboMessage) {
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
	qro.ToRoboMessage.ShowQR.QrType = tele.ShowQR_error
	if terminalKey == "" {
		CashLess.g.Log.Info("cashless system not working. send robot message qrerror")
		return
	}
	if rm.Order.Amount < terminalMinimalAmount { // minimal bank amount
		CashLess.g.Log.Errorf("bank pay imposible. the amount is less than the minimum\n%#v", rm.Order.Amount)
		return
	}
	persentAmount := (rm.Order.Amount * terminalBankCommission) / 10000
	od := time.Now()
	qro.Vmid = vmid
	qro.Order_id = fmt.Sprintf("%d-%s-%s", vmid, od.Format("060102150405"), rm.Order.MenuCode)
	qro.Amount = uint64(rm.Order.Amount + persentAmount)
	qro.Create_date = od
	qro.Description = menuGetName(vmid, rm.Order.MenuCode)
	// 4 test -----------------------------------
	/*
		res := tinkoff.InitResponse{
			Amount:     qro.Amount,
			OrderID:    qro.Order_id,
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
		OrderID:     qro.Order_id,
		Description: qro.Description,
		Data:        map[string]string{"Vmc": fmt.Sprintf("%d", vmid)},
		// RedirectDueDate: tinkoff.Time{time.Now().Local().Add(time.Minute * 5)},
	})
	//*/
	if err != nil || res.Status != tinkoff.StatusNew {
		CashLess.g.Log.Errorf("bank pay init error:%v", err)
		return
	}
	qro.Payment_id = res.PaymentID
	qro.Paymentid, err = str2uint64(res.PaymentID)
	if err != nil {
		CashLess.g.Log.Errorf("bank pay payment id error:%v", err)
		return
	}
	// 4 test -----------------------------------
	/*
		pidi, _ := strconv.Atoi(res.PaymentID)
		qrr := tinkoff.GetQRResponse{
			OrderID:   qro.Order_id,
			Data:      "TEST qr code for pay",
			PaymentID: pidi,
		}
		/*/
	qrr, err := terminalClient.GetQR(&tinkoff.GetQRRequest{
		PaymentID: res.PaymentID,
	})
	//*/
	if err != nil {
		CashLess.g.Log.Errorf("bank get QR error:%v", err)
		return
	}
	err = qro.orderCreate()
	if err != nil {
		CashLess.g.Log.Errorf("bank orger create. write db error:%v", err)
		return
	}
	qro.ToRoboMessage.ShowQR.QrText = qrr.Data
	qro.ToRoboMessage.ShowQR.QrType = tele.ShowQR_order
	qro.ToRoboMessage.ShowQR.DataInt = int32(qro.Amount)
	qro.ToRoboMessage.ShowQR.DataStr = res.PaymentID

	// go qro.waitingForPayment()

	// 4 test -----------------------------------
	/*
		if qro.Vmid == 88 {
			go func() {
				time.Sleep(2 * time.Second)
				qro.paid()
			}()
		}
		//*/
}

func str2uint64(str string) (int64, error) {
	i, err := strconv.ParseInt(str, 10, 64)
	return int64(i), err
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

func (o *CashLessOrderStruct) orderCreate() error {
	const q = `INSERT INTO cashless (order_state, vmid, create_date, paymentid, order_id, amount, terminal_id) VALUES ( ?0, ?1, ?2, ?3, ?4, ?5, ?6 );`
	_, err := CashLess.g.DB.Exec(q, order_start, o.Vmid, o.Create_date, o.Paymentid, o.Order_id, o.Amount, 1)
	return err
}

func startNotificationsReader(s string) {
	u, err := url.Parse(s)
	if err != nil {
		CashLess.g.Log.Errorf("parce notification (%s) error(%v)", s, err)
	}
	if u.Host == "" {
		u.Host = ":8080"
	}
	if u.Path == "" {
		u.Path = "/payment/notification/tinkoff"
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	r.POST(u.Path, func(c *gin.Context) {
		n, errn := terminalClient.ParseNotification(c.Request.Body)
		if errn != nil {
			CashLess.g.Log.Errorf("notification(%v) parse error(%v)", n, err)
			return
		}
		c.String(http.StatusOK, terminalClient.GetNotificationSuccessResponse())
		order, err1 := getOrder(n.OrderID)
		if order.Paymentid != int64(n.PaymentID) && order.Amount != n.Amount {
			err = fmt.Errorf("%v; notification from bank, doesn't match paymentid or amount", err)
		}
		switch n.Status {
		case tinkoff.StatusConfirmed:
			CashLess.g.Log.Infof("confirmed pay order: %v amount %v ", n.OrderID, n.Amount)
			if err1 != nil {
				CashLess.g.Log.Errorf("unknown confifmed. retun money. (%v) error(%v)", n, err)
				// FIXME return money
				return
			}
			if order.Order_state >= order_complete {
				CashLess.g.Log.Errorf("coplete notification for confifmed or canceled order(%v) (%v) notification(%v)", order, n)
				return
			}
			order.paid()
		case tinkoff.StatusCanceled:
			if order.Order_state >= order_execute {
				CashLess.g.Log.Errorf("cancel paid or completed order! (%v) ", order)
			}
			CashLess.g.Log.Infof("cancel order (%v)", order)
			order.cancel()
		case tinkoff.StatusRejected:
			order.reject()
		case tinkoff.StatusAuthorized:
		case tinkoff.StatusRefunded:
		default:
			CashLess.g.Log.NoticeF("unknown notification from bank(%v)", n)
		}
	})
	CashLess.g.Log.Notice("start bank notification server.")
	err = r.Run(u.Host)
	CashLess.g.Log.Errorf("error start notification server. error:%v", err)
}
func getOrder(orderId string) (CashLessOrderStruct, error) {
	var o CashLessOrderStruct
	_, err := CashLess.g.DB.QueryOne(&o, `select order_state, vmid, order_id, amount, payment_id, paymentid from cashless where cashless.order_id = ?0;`, orderId)
	return o, err
}

func getOrderByOwner(pid int64) (CashLessOrderStruct, error) {
	var o CashLessOrderStruct
	_, err := CashLess.g.DB.QueryOne(&o, `select order_state, vmid, order_id, amount, payment_id, paymentid from cashless where paymentid = ?;`, pid)
	return o, err
}

func (o *CashLessOrderStruct) cancel() {
	const q = `UPDATE cashless SET order_state = ?1, finish_date = now(), credited = 0 WHERE order_id = ?0`
	_ = dbUpdate(q, o.Order_id, order_cancel)
}

func (o *CashLessOrderStruct) startExecution() {
	const q = `UPDATE cashless SET order_state = ?1 WHERE order_id = ?0`
	_ = dbUpdate(q, o.Order_id, order_execute)
}

func (o *CashLessOrderStruct) complete() {
	const q = `UPDATE cashless SET order_state = ?1, finish_date = now() WHERE order_id = ?0`
	_ = dbUpdate(q, o.Order_id, order_complete)
}

func (o *CashLessOrderStruct) error() {
	if o.Order_state == order_prepay || o.Order_state == order_execute {
		CashLess.g.Log.Errorf("error order:%v ", o)
		// return money
		o.refundOrder()
	}
}

func (o *CashLessOrderStruct) refundOrder() {
	m := fmt.Sprintf("return money. order:%v ", o)
	// FIXME Payment_id string Paymentid int
	CashLess.g.Log.WarningF("o.Payment_id:%v, o.Paymentid:%v ", o.Payment_id, o.Paymentid)
	if o.Payment_id == "" {
		o.Payment_id = fmt.Sprintf("%v", o.Paymentid)
	}
	CashLess.g.Log.Debugf(m)
	CashLess.g.VMCErrorWriteDB(o.Vmid, o.Create_date.Unix(), 0, m)
	cReq := &tinkoff.CancelRequest{
		PaymentID: o.Payment_id,
		Amount:    o.Amount,
	}
	cRes, err := terminalClient.Cancel(cReq)
	switch cRes.Status {
	case tinkoff.StatusQRRefunding:
		o.cancel()
	default:
		const q = `UPDATE cashless SET order_state = ?1, finish_date = now() WHERE order_id = ?0`
		_ = dbUpdate(q, o.Order_id, order_cancel)
	}
	if err != nil {
		CashLess.g.VMCErrorWriteDB(o.Vmid, 0, 0, "error."+m)
	}

}

// write paid data and send command to robot for make
func (o *CashLessOrderStruct) paid() {
	q := `UPDATE cashless SET order_state = ?2, credit_date = now(), credited = ?1 WHERE order_id = ?0`
	// _, err := CashLess.g.DB.Exec(q, o.Order_id, o.Amount, order_prepay)
	o.Order_state = order_prepay
	err := dbUpdate(q, o.Order_id, o.Amount, order_prepay)
	if err != nil {
		// return money
		return
	}
	sm := tele.ToRoboMessage{
		ServerTime: time.Now().Unix(),
		Cmd:        tele.MessageType_makeOrder,
		MakeOrder: &tele.Order{
			Amount:        uint32(o.Amount),
			OrderStatus:   tele.OrderStatus_doSelected,
			PaymentMethod: tele.PaymentMethod_Cashless,
			OwnerInt:      int64(o.Paymentid),
			OwnerStr:      o.Payment_id,
			OwnerType:     tele.OwnerType_qrCashLessUser,
		},
	}
	CashLess.g.Log.NoticeF("confirmed pay order vm%v ", o.Vmid)
	CashLess.g.Tele.SendToRobo(o.Vmid, &sm)
}

// write cancel order and send reject to robo
func (o *CashLessOrderStruct) reject() {
	o.cancel()
	sm := tele.ToRoboMessage{
		Cmd: tele.MessageType_showQR,
		ShowQR: &tele.ShowQR{
			QrType: tele.ShowQR_errorOverdraft,
		},
	}
	CashLess.g.Tele.SendToRobo(o.Vmid, &sm)

}
func dbUpdate(query interface{}, params ...interface{}) error {
	r, err := CashLess.g.DB.Exec(query, params...)
	if err != nil || r.RowsAffected() != 1 {
		CashLess.g.Log.Errorf("fail db update sql(%v) parameters (%v) error(%v)): %v", query, params, err)
	}
	return err
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
			return
		case <-CashLess.Alive.StopChan():
			return
		case <-refreshTimer.C:
			// 4 test -----------------------------------
			/*
				if true {
					var s tinkoff.GetStateResponse
					s.Status = tinkoff.StatusConfirmed
					// s.Status = tinkoff.StatusRejected
					var err error
					/*/
			if s, err := terminalClient.GetState(&tinkoff.GetStateRequest{PaymentID: o.Payment_id}); err == nil {
				//*/
				if err != nil {
					// o.cancelOrder()
					CashLess.g.Log.Errorf("cashless get status:", err)
					return
				}
				switch s.Status {
				case tinkoff.StatusConfirmed:
					if o.Order_state != order_prepay {
						o.paid()
					}
					// o.writeDBOrderPaid()
					// o.sendStartCook()
					return
				case tinkoff.StatusRejected:
					o.reject()
					// o.bankQRReject()
					return
				case tinkoff.StatusCanceled:
					o.cancel()
					return
				default:
				}

				refreshTimer.Reset(refreshTime)
			}
		}
	}
}
