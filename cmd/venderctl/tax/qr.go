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
	Paymentid     uint64
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
	if CashLess.g.Config.CashLess.QRValidTimeSec == 0 {
		CashLess.g.Config.CashLess.QRValidTimeSec = 300
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
	CashLess.g.Log.Debug("cashless system stoped ")
}

func CashLessErrorDB(format string, args ...interface{}) {
	s := fmt.Sprintf(format, args...)
	CashLess.g.Log.Errorf(s)
	CashLess.g.VMCErrorWriteDB(0, 0, 0, s)
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
	qro.Vmid = vmid
	qro.Amount = uint64(rm.Order.Amount + persentAmount)
	od := time.Now()
	qro.Order_id = fmt.Sprintf("%d-%s-%s", vmid, od.Format("060102150405"), rm.Order.MenuCode)
	qro.Create_date = od
	qro.Description = menuGetName(vmid, rm.Order.MenuCode)
	if ok := qro.initPaySession(); !ok {
		return
	}
	if ok := qro.getQRdata(); !ok {
		return
	}
	qro.ToRoboMessage.ShowQR.QrType = tele.ShowQR_order
	qro.ToRoboMessage.ShowQR.DataInt = int32(qro.Amount)
	qro.ToRoboMessage.ShowQR.DataStr = qro.Payment_id

	go waitingForPayment(qro.Order_id)

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
func (o *CashLessOrderStruct) getQRdata() (valid bool) {
	getQrRequest := tinkoff.GetQRRequest{PaymentID: o.Payment_id}
	newQrResponse, err := terminalClient.GetQR(&getQrRequest)
	if err != nil {
		CashLessErrorDB("bank get QR error:%v order:%v", err, o)
		return
	}
	if newQrResponse.PaymentID != int(o.Paymentid) || newQrResponse.OrderID != o.Order_id {
		CashLessErrorDB("bank QR paymentID mismatch response(%v) order(%v) ", newQrResponse, o)
		return
	}
	if err = o.orderCreate(); err != nil {
		// CashLess.g.Log.Errorf("bank orger create. write db error:%v", err)
		CashLessErrorDB("write db error:%v order(%v)", err, o)
		return
	}
	o.ToRoboMessage.ShowQR.QrText = newQrResponse.Data
	return true
}

func (o *CashLessOrderStruct) initPaySession() (valid bool) {
	ir := tinkoff.InitRequest{
		BaseRequest:     tinkoff.BaseRequest{TerminalKey: CashLess.g.Config.CashLess.TerminalKey, Token: "random"},
		Amount:          o.Amount,
		OrderID:         o.Order_id,
		Description:     o.Description,
		Data:            map[string]string{"Vmc": fmt.Sprintf("%d", o.Vmid)},
		RedirectDueDate: tinkoff.Time(time.Now().Local().Add(time.Minute * 5)),
	}
	bankResponse, err := terminalClient.Init(&ir)
	if err != nil {
		CashLessErrorDB("bank pay init error:%v orderId:%s. resend init", err, o.Order_id)
		if bankResponse, err = terminalClient.Init(&ir); err != nil {
			CashLessErrorDB("two time init error:%+v", ir)
			return false
		}
	}
	if bankResponse.Status != tinkoff.StatusNew {
		CashLessErrorDB("bank pay error init response:%v init request:%+v", bankResponse, ir)
		return false
	}
	o.Payment_id = bankResponse.PaymentID
	o.Paymentid, err = str2uint64(bankResponse.PaymentID)
	if err != nil {
		CashLess.g.Log.Errorf("bank pay paymentid(%s) not number:%v", bankResponse.PaymentID, err)
		return false
	}
	return true
}

func str2uint64(str string) (uint64, error) {
	i, err := strconv.ParseInt(str, 10, 64)
	return uint64(i), err
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

func getOrder(orderId string) CashLessOrderStruct {
	var o CashLessOrderStruct
	_, _ = CashLess.g.DB.QueryOne(&o, `select order_state, vmid, order_id, amount, payment_id, paymentid from cashless where cashless.order_id = ?0;`, orderId)
	return o
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

func (o *CashLessOrderStruct) refundOrder() {
	m := fmt.Sprintf("return money. order:%v ", o)
	// FIXME Payment_id string Paymentid int
	CashLess.g.Log.WarningF("o.Payment_id:%v, o.Paymentid:%v ", o.Payment_id, o.Paymentid)
	if o.Payment_id == "" {
		o.Payment_id = fmt.Sprintf("%v", o.Paymentid)
	}
	CashLess.g.Log.Debugf(m)
	CashLess.g.VMCErrorWriteDB(o.Vmid, o.Create_date.Unix(), 0, m)
	o.sendCanselToBank()
}

func (o *CashLessOrderStruct) sendCanselToBank() {
	paymentidStr := strconv.Itoa(int(o.Paymentid))
	cReq := &tinkoff.CancelRequest{
		PaymentID: paymentidStr,
		Amount:    o.Amount,
	}
	cRes, err := terminalClient.Cancel(cReq)
	if err != nil {
		CashLess.g.VMCErrorWriteDB(o.Vmid, 0, 0, fmt.Sprintf("cancel order request error. (%v) orger %v", err, o))
	}
	switch cRes.Status {
	case tinkoff.StatusQRRefunding:
		o.cancel()
	default:
		const q = `UPDATE cashless SET order_state = ?1, finish_date = now() WHERE order_id = ?0`
		_ = dbUpdate(q, o.Order_id, order_cancel)
	}
}

// write paid data and send command to robot for make
func (o *CashLessOrderStruct) paid() {
	if o.Order_state >= order_prepay {
		return
	}
	q := `UPDATE cashless SET order_state = ?2, credit_date = now(), credited = ?1 WHERE order_id = ?0`
	err := dbUpdate(q, o.Order_id, o.Amount, order_prepay)
	if err != nil {
		// CashLess.g.Log.Errorf("db update paid order error order(%v)\n error(%v)", o, err)
		CashLessErrorDB("db update paid order error order(%v)\n error(%v)", o, err)
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
	CashLess.g.Log.NoticeF("send to robot. confirmed pay order vm%v ", o.Vmid)
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
		CashLess.g.Log.Errorf("fail db update sql(%v) parameters (%v) error(%v)", query, params, err)
	}
	return err
}

func waitingForPayment(orderID string) {
	CashLess.Alive.Add(1)
	tmr := time.NewTimer(time.Second * time.Duration(CashLess.g.Config.CashLess.QRValidTimeSec))
	refreshTime := time.Duration(time.Second * time.Duration(CashLess.g.Config.CashLess.TerminalQRPayRefreshSec))
	refreshTimer := time.NewTimer(refreshTime)
	defer func() {
		tmr.Stop()
		refreshTimer.Stop()
		CashLess.Alive.Done()
	}()

	for {
		select {
		case <-CashLess.Alive.StopChan():
			return
		case <-tmr.C:
			order := getOrder(orderID)
			switch order.Order_state {
			case order_invalid, order_start:
				order.sendCanselToBank()
			case order_prepay, order_execute:
				CashLessErrorDB("time out worked orderID (%v)", order)
			default:
			}
			return
		case <-refreshTimer.C:
			order := getOrder(orderID)
			if s, err := terminalClient.GetState(&tinkoff.GetStateRequest{PaymentID: order.Payment_id}); err == nil {
				switch s.Status {
				case tinkoff.StatusConfirmed:
					if order.Order_state <= order_start {
						CashLess.g.Log.Infof("refresh bank timer StatusConfirmed(%v)", order)
						order.paid()
						return
					}
				case tinkoff.StatusRejected:
					CashLess.g.Log.WarningF("refresh bank timer StatusRejected(%v)", order)
					order.reject()
					return
				case tinkoff.StatusCanceled:
					CashLess.g.Log.WarningF("refresh bank timer StatusCanceled(%v)", order)
					order.cancel()
					return
				default:
				}
				refreshTimer.Reset(refreshTime)
			}
		}
	}
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
		var n *tinkoff.Notification
		n, err = terminalClient.ParseNotification(c.Request.Body)
		CashLess.g.Log.Debugf("notification from bank (%v)", n)
		if err != nil {
			CashLess.g.Log.Errorf("notification(%v) parse error(%v)", n, err)
			return
		}
		c.String(http.StatusOK, terminalClient.GetNotificationSuccessResponse())
		order := getOrder(n.OrderID)
		if order.Paymentid != n.PaymentID && order.Amount != n.Amount {
			CashLessErrorDB("notification from bank, doesn't match paymentid or amount\n order(%v)\n notification(%v)", order, n)
			return
		}
		switch n.Status {
		case tinkoff.StatusConfirmed:
			if order.Order_state <= order_start {
				CashLess.g.Log.WarningF("event from bank. order payed. vmid:%d order:%s amount:%d paymentId:%d", order.Vmid, order.Order_id, order.Amount, order.Paymentid)
				order.paid()
				return
			}
		case tinkoff.StatusCanceled:
			CashLess.g.Log.Infof("event from bank. cancel vmid:%d order:%s", order.Vmid, order.Order_id)
			if order.Order_state == order_cancel {
				return
			}
			if order.Order_state >= order_execute {
				CashLessErrorDB("cancel paid or completed order! (%v) ", order)
			}
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
