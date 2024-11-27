package cashless_tinkoff

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/AlexTransit/vender/currency"
	"github.com/AlexTransit/vender/tele"
	"github.com/AlexTransit/venderctl/internal/state"
	tele_api "github.com/AlexTransit/venderctl/internal/tele/api"
	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v9"
	"github.com/nikita-vanyasin/tinkoff"
)

var QR struct {
	*state.Global
	orderValidTimeSec int // время валидности заказа
	terminalClient    *tinkoff.Client
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

func QrInit(ctx context.Context) {
	QR.Global = state.GetGlobal(ctx)
	QR.Log.Info("start init QR")
	QR.Config.CashLess.QRValidTimeSec = state.ConfigInt(QR.Config.CashLess.QRValidTimeSec, 300)
	QR.Config.CashLess.TerminalQRPayRefreshSec = state.ConfigInt(QR.Config.CashLess.TerminalQRPayRefreshSec, 3)
	QR.Config.CashLess.TerminalMinimalAmount = state.ConfigInt(QR.Config.CashLess.TerminalMinimalAmount, 1000)
	QR.Config.CashLess.TimeoutToStartManualPaymentVerificationSec = state.ConfigInt(QR.Config.CashLess.TimeoutToStartManualPaymentVerificationSec, 20)

	go readerMqtt(ctx) // обработчик событий от роботов
	if QR.Config.CashLess.TerminalKey == "" {
		QR.Log.Info("tekminal key not foud. cashless system not start.")
		return
	}
	if QR.Config.CashLess.TerminalPass == "" {
		QR.Log.Info("tekminal password not foud. cashless system not start.")
		return
	}
	QR.terminalClient = tinkoff.NewClientWithOptions(
		tinkoff.WithTerminalKey(QR.Config.CashLess.TerminalKey),
		tinkoff.WithPassword(QR.Config.CashLess.TerminalPass),
		tinkoff.WithBaseURL("https://aaa.aaa/v2"),
	)
	// go startNotificationsReader(CashLess.g.Config.CashLess.URLToListenToBankNotifications)  // обработчик собитий от банки
	QR.Log.Info("init QR complete")
}

func readerMqtt(ctx context.Context) {
	QR.Log.Info("run mqtt reader")
	defer QR.Log.Info("mqtt reader stoped")
	mqttch := QR.Tele.Chan()
	for {
		select {
		case p := <-mqttch:
			if p.Kind == tele_api.FromRobo {
				rm := QR.ParseFromRobo(p)
				if rm.State == tele.State_WaitingForExternalPayment {
					go createQR(ctx, p.VmId, rm)
					continue
				}
				if rm.Order != nil && rm.Order.OwnerInt != 0 && rm.Order.OwnerType == tele.OwnerType_qrCashLessUser {
					o, err := dbGetOrderByOwner(ctx, uint64(rm.Order.OwnerInt))
					if err != nil {
						QR.Log.Errorf("order message from robo (%+v) get in db error (%v)", rm.Order, err)
						return
					}
					QR.Log.Infof("robot:%d started make order:%s paymentId:%d amount:%d ", o.vmid, o.orderID, o.paymentId, o.amount)
					switch rm.Order.OrderStatus {
					case tele.OrderStatus_executionStart:
						o.dbStartExecution(ctx)
					case tele.OrderStatus_orderError:
						QR.Log.Errorf("from robot. cooking error. order (%+v)", o)
						o.cancelOrder(ctx)
					case tele.OrderStatus_cancel:
						o.cancelOrder(ctx)
					case tele.OrderStatus_waitingForPayment:
					case tele.OrderStatus_complete:
						o.dbComplete(ctx)
						QR.Log.NoticeF("from robot. vm%d cashless complete order:%s price:%s payer:%d ", o.vmid, o.orderID, currency.Amount(o.amount).Format100I(), o.paymentId)
					default:
					}
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

type qrOrder struct {
	order_state      orderState
	order_bank_state int32
	vmid             int32
	date             time.Time
	orderID          string
	amount           uint64
	paymentId        uint64
	paymentIdStr     string
	description      string
}

func createQR(ctx context.Context, vmid int32, rm *tele.FromRoboMessage) {
	messageForRobot := tele.ToRoboMessage{
		Cmd: tele.MessageType_showQR,
		ShowQR: &tele.ShowQR{
			QrType: tele.ShowQR_error,
		},
	}
	defer func() {
		QR.Tele.SendToRobo(vmid, &messageForRobot)
		QR.Log.Infof("robo(%d) show QR type(%+v) ", vmid, messageForRobot)
	}()
	if QR.Config.CashLess.TerminalKey == "" {
		QR.Log.Info("cashless system not working. send robot message qrerror")
		return
	}
	if rm.Order.Amount < uint32(QR.Config.CashLess.TerminalMinimalAmount) { // minimal bank amount
		QR.Log.Errorf("bank pay imposible. order amount < minimal. %d<%d", rm.Order.Amount, QR.Config.CashLess.TerminalMinimalAmount)
		return
	}
	// считаем комиссию и округляем до целой копейки
	bankCommision := QR.Config.CashLess.TerminalBankCommission * int(rm.Order.Amount) / 1000
	if bankCommision%10 > 0 {
		bankCommision = bankCommision/10 + 1
	}
	orderCreateDate := time.Now()
	order := qrOrder{
		order_state: order_start,
		vmid:        vmid,
		date:        orderCreateDate,
		orderID:     fmt.Sprintf("%d-%s-%s", vmid, orderCreateDate.Format("060102150405"), rm.Order.MenuCode),
		amount:      uint64(rm.Order.Amount) + uint64(bankCommision),
		description: dbGetOrderDecription(ctx, vmid, rm.Order.MenuCode),
	}
	if ok := order.initPaySession(ctx); !ok {
		return
	}
	if ok := order.dbCreateOrder(ctx); !ok {
		return
	}
	//* test -------------------------------------------------------------------------------------
	messageForRobot.ShowQR.QrText = "AAAAAAAAAAAAA"
	/*/
	ok, qrData := order.getQrCode(ctx)
	if !ok {
		return
	}
	messageForRobot.ShowQR.QrText = qrData
	//*/

	messageForRobot.ShowQR.QrType = tele.ShowQR_order
	messageForRobot.ShowQR.DataInt = int32(order.amount)
	messageForRobot.ShowQR.DataStr = order.paymentIdStr

	go order.manualyPaymentVerification(ctx)
	go func() {
		time.Sleep(10 * time.Second)
		oo, _ := dbGetOrderByOwner(ctx, 12345)
		dbUpdate(ctx, fmt.Sprintf(`UPDATE cashless SET order_bank_state = %d WHERE order_id = '%s';`, 10, oo.orderID))
	}()
}

func (o *qrOrder) initPaySession(ctx context.Context) (valid bool) {
	ir := &tinkoff.InitRequest{
		BaseRequest: tinkoff.BaseRequest{TerminalKey: QR.Config.CashLess.TerminalKey, Token: "random"},
		Amount:      o.amount,
		OrderID:     o.orderID,
		Description: o.description,
		Data: map[string]string{
			"Vmc":    fmt.Sprintf("%d", o.vmid),
			"fphone": "true",
			"QR":     "true",
		},
		RedirectDueDate: tinkoff.Time(time.Now().Local().Add(5 * time.Minute)),
	}
	var bankResponse *tinkoff.InitResponse
	var errStr string
	for i := 1; i <= 3; i++ {
		var e error
		ctxWichTimeOut, cancel := context.WithTimeout(ctx, time.Second*2)
		bankResponse, e = QR.terminalClient.InitWithContext(ctxWichTimeOut, ir)
		cancel()
		//* test -------------------------------------------------------------------------------------------------
		e = nil
		bankResponse = &tinkoff.InitResponse{
			Amount:     o.amount,
			OrderID:    o.orderID,
			Status:     tinkoff.StatusNew,
			PaymentID:  "12345",
			PaymentURL: "HZ",
		}
		//*/
		if e == nil {
			break
		}
		QR.Log.Errorf("init payment order(%+v) error (%v)", o, e)
		errStr = fmt.Sprintf("(%d error - %v) ", i, e.Error()) + errStr
		if i == 3 {
			QR.VMCErrorWriteDb(o.vmid, errStr)
			return false
		}
	}
	if bankResponse.Status != tinkoff.StatusNew {
		QR.VMCErrorWriteDb(o.vmid, fmt.Sprintf("init payment status (need NEW) order(%+v) bark response(%+v)", o, bankResponse))
		return false
	}
	o.paymentIdStr = bankResponse.PaymentID
	i, e := strconv.ParseInt(bankResponse.PaymentID, 10, 64)
	if e != nil {
		QR.VMCErrorWriteDb(o.vmid, fmt.Sprintf("bank paimentid(%s) not number. responce(%+v) error(%v)", bankResponse.PaymentID, bankResponse, e))
		return false
	}
	o.paymentId = uint64(i)
	return true
}

func (o *qrOrder) getQrCode(ctx context.Context) (valid bool, data string) {
	ctxWichTimeOut, cancel := context.WithTimeout(ctx, time.Second*2)
	qrRequest := &tinkoff.GetQRRequest{PaymentID: o.paymentIdStr}
	qrResponse, err := QR.terminalClient.GetQRWithContext(ctxWichTimeOut, qrRequest)
	cancel()
	if err != nil {
		QR.VMCErrorWriteDb(o.vmid, fmt.Sprintf("bank get QR error:%v order id:%s", err, o.orderID))
		return false, ""
	}
	if qrResponse.PaymentID != int(o.paymentId) {
		QR.VMCErrorWriteDb(o.vmid, fmt.Sprintf("bank QR paymentID mismatch response(%v) order(%v) response(%v) ", qrResponse, o, qrResponse))
		return false, ""
	}
	return true, qrResponse.Data
}

func (o *qrOrder) manualyPaymentVerification(ctx context.Context) {
	QR.Log.Infof("run checking payment status order(%s) ", o.orderID)
	defer QR.Log.Infof("stop checking payment status order(%s) ", o.orderID)

	//* test -------------------------------------------------------------------------------------
	ctxTimeOutQR, cancelQRctx := context.WithTimeout(ctx, time.Second*time.Duration(30))
	QR.Config.CashLess.TimeoutToStartManualPaymentVerificationSec = 3
	/*/
	ctxTimeOutQR, cancelQRctx := context.WithTimeout(ctx, time.Second*time.Duration(QR.Config.CashLess.QRValidTimeSec))
	//*/

	defer cancelQRctx()
	refreshTime := time.Duration(time.Second * time.Duration(QR.Config.CashLess.TerminalQRPayRefreshSec))
	// start the poll timer after timeout. запускаем таймер опроса после задержки
	refreshTimer := time.NewTimer(time.Second * time.Duration(QR.Config.CashLess.TimeoutToStartManualPaymentVerificationSec))
	defer refreshTimer.Stop()

	for {
		select {
		case <-refreshTimer.C:
			refreshTimer.Reset(refreshTime)
			ctxSessionTimeOut, cancelSession := context.WithTimeout(ctxTimeOutQR, time.Second*2)
			orderState, err := QR.terminalClient.GetStateWithContext(ctxSessionTimeOut, &tinkoff.GetStateRequest{PaymentID: o.paymentIdStr})
			cancelSession()
			//* test -------------------------------------------------------------------------------------
			orderState = new(tinkoff.GetStateResponse)
			orderState.Status = tinkoff.StatusAuthorized
			orderState.PaymentID = o.paymentIdStr
			orderState.OrderID = o.orderID
			err = nil
			//*/
			if err != nil {
				QR.Log.Errorf("check state order(%s) error(%v)", o.orderID, err)
				continue
			}
			if ok := o.compareOrder(orderState.OrderID, orderState.PaymentID); !ok {
				continue
			}
			if refresh := o.dbUpdateOrdreStatus(ctx, orderState.Status); !refresh {
				return
			}
		case <-ctxTimeOutQR.Done():
			o.dbGetOrderStatus(ctx)
			if o.order_state <= order_start && o.order_bank_state <= 2 {
				o.cancelOrder(ctx)
			}
			fmt.Printf("\033[41m ctxTimeOutQR.Done() \033[0m\n")
			return
		}
	}
}

func (o *qrOrder) compareOrder(orderID string, paymentID interface{}) (ok bool) {
	switch paymentID.(type) {
	case string:
		if o.paymentIdStr == paymentID {
			ok = true
		}
	case uint64:
		if o.paymentId == paymentID {
			ok = true
		}
	default:
		ok = false
	}
	if ok && o.orderID != orderID {
		return true
	}
	QR.VMCErrorWriteDb(o.vmid, fmt.Sprintf("BAD BANK. request mismash orderID<>responseID (%s<>%s) or paymentid<>responseID (%s<>%+v)", o.orderID, orderID, o.paymentIdStr, paymentID))
	return false
}

func (o *qrOrder) sendMessageMakeOrder(ctx context.Context) {
	// FIXME AlexM
	_ = ctx
	o.dbGetOrderStatus(ctx)
	if o.order_state > order_prepay {
		return
	}
	mToRobo := tele.ToRoboMessage{
		Cmd:        tele.MessageType_makeOrder,
		ServerTime: time.Now().Unix(),
		MakeOrder: &tele.Order{
			Amount:      uint32(o.amount),
			OrderStatus: tele.OrderStatus_doSelected,
			OwnerInt:    int64(o.paymentId),
			OwnerType:   tele.OwnerType_qrCashLessUser,
		},
	}
	dbUpdate(ctx, fmt.Sprintf(`UPDATE cashless SET order_state = %d WHERE order_id = '%s';`, order_prepay, o.orderID))
	QR.Tele.SendToRobo(o.vmid, &mToRobo)
	QR.Log.Infof("->robo (%+v)", mToRobo)
}

func (o *qrOrder) sendMessageImpossibleMake(ctx context.Context) {
	// FIXME AlexM
	_ = ctx
	o.dbGetOrderStatus(ctx)
	if o.order_state == order_cancel {
		return
	}
	mToRobo := tele.ToRoboMessage{
		Cmd: tele.MessageType_showQR,
		ShowQR: &tele.ShowQR{
			QrType: tele.ShowQR_errorOverdraft,
		},
	}
	dbUpdate(ctx, fmt.Sprintf(`UPDATE cashless SET order_state = %d WHERE order_id = '%s';`, order_cancel, o.orderID))
	QR.Tele.SendToRobo(o.vmid, &mToRobo)
}

func (o *qrOrder) dbComplete(ctx context.Context) {
	query := fmt.Sprintf(`UPDATE cashless SET order_state = %d, finish_date = now() WHERE order_id = '%s';`, order_complete, o.orderID)
	dbUpdate(ctx, query)
}

func (o *qrOrder) dbStartExecution(ctx context.Context) {
	query := fmt.Sprintf(`UPDATE cashless SET order_state = %d, finish_date = now() WHERE order_id = '%s';`, order_execute, o.orderID)
	dbUpdate(ctx, query)
}

func (o *qrOrder) cancelOrder(ctx context.Context) {
	o.dbGetOrderStatus(ctx)
	if o.order_state >= order_complete {
		return
	}
	dbUpdate(ctx, fmt.Sprintf(`UPDATE cashless SET order_state = %d WHERE order_id = '%s';`, order_cancel, o.orderID))
	cReq := &tinkoff.CancelRequest{
		PaymentID: o.paymentIdStr,
		Amount:    o.amount,
	}
	cRes, err := QR.terminalClient.Cancel(cReq)
	/* test -------------------------------------------------------------------------------------
	err = nil
	cRes = &tinkoff.CancelResponse{
		OriginalAmount: 0,
		NewAmount:      0,
		OrderID:        o.orderID,
		Status:         tinkoff.StatusCanceled,
		PaymentID:      o.paymentIdStr,
	}
	//*/
	errStr := fmt.Sprintf("cancel order(%s) error(%v) request(%+v) response(%+v)", o.orderID, err, cReq, cRes)
	if err != nil {
		QR.VMCErrorWriteDb(0, errStr)
		return
	}
	if ok := o.compareOrder(cRes.OrderID, cRes.PaymentID); !ok {
		return
	}
	switch cRes.Status {
	case tinkoff.StatusCanceled, tinkoff.StatusRefunded:
		o.dbUpdateOrdreStatus(ctx, cRes.Status)
	default:
		dbUpdate(ctx, fmt.Sprintf(`UPDATE cashless SET credited = %d finish_date = now() WHERE order_id = '%s';`, cRes.NewAmount, o.orderID))
		QR.VMCErrorWriteDb(0, errStr)
	}
}

func (o *qrOrder) dbCreateOrder(ctx context.Context) bool {
	const q = `INSERT INTO cashless (order_state, vmid, paymentid, create_date, order_id, amount, order_bank_state) VALUES ( ?0 ,?1, ?2, NOW(), ?3, ?4, ?5 );`
	_, err := QR.DB.ExecContext(
		ctx,
		q,
		order_start,
		o.vmid,
		o.paymentId,
		o.orderID,
		o.amount,
		getBankOrderStatusIndex(tinkoff.StatusNew),
	)
	if err == nil {
		return true
	}
	QR.VMCErrorWriteDb(o.vmid, fmt.Sprintf("create order(%+v) in database error(%v)", o, err))
	return false
}

func (o *qrOrder) dbGetOrderStatus(ctx context.Context) bool {
	_, err := QR.DB.QueryOneContext(ctx, pg.Scan(&o.order_state, &o.order_bank_state), `SELECT order_state, order_bank_state FROM cashless WHERE order_id = ?0;`, o.orderID)
	if err != nil {
		QR.VMCErrorWriteDb(o.vmid, fmt.Sprintf("db get order order(%+v) status error(%v)", o, err))
		return false
	}
	return true
}

func (o *qrOrder) dbUpdateOrdreStatus(ctx context.Context, bankOrderStatus string) (refresh bool) {
	if ok := o.dbGetOrderStatus(ctx); !ok {
		return false
	}
	bankOrderStatusI := getBankOrderStatusIndex(bankOrderStatus)
	if bankOrderStatusI == 0 {
		QR.VMCErrorWriteDb(0, "undefined new bank status"+bankOrderStatus+")")
	}
	if o.order_bank_state == bankOrderStatusI {
		return true
	}
	var query string
	switch bankOrderStatus {
	case tinkoff.StatusConfirmed:
		query = fmt.Sprintf(`UPDATE cashless SET  order_bank_state = %d, credited = %d, credit_date = NOW() WHERE order_id = '%s';`, bankOrderStatusI, o.amount, o.orderID)
		go o.sendMessageMakeOrder(ctx)
		refresh = false
	case tinkoff.StatusCanceled,
		tinkoff.StatusRefunded:
		query = fmt.Sprintf(`UPDATE cashless SET  order_bank_state = %d, credited = 0, finish_date = NOW() WHERE order_id = '%s';`, bankOrderStatusI, o.orderID)
	case tinkoff.StatusDeadlineExpired,
		tinkoff.StatusAuthFail,
		tinkoff.StatusRejected:
		query = fmt.Sprintf(`UPDATE cashless SET order_bank_state = %d WHERE order_id = '%s';`, bankOrderStatusI, o.orderID)
		go o.sendMessageImpossibleMake(ctx)
		refresh = false
	default:
		query = fmt.Sprintf(`UPDATE cashless SET order_bank_state = %d WHERE order_id = '%s';`, bankOrderStatusI, o.orderID)
		refresh = true
	}
	dbUpdate(ctx, query)
	return refresh
}

func dbUpdate(ctx context.Context, query interface{}, params ...interface{}) error {
	r, err := QR.DB.ExecContext(ctx, query, params...)
	if err != nil || r.RowsAffected() != 1 {
		QR.VMCErrorWriteDb(0, fmt.Sprintf("fail db update sql(%s) parameters (%+v) error(%v)", query, params, err), 2)
		return err
	}
	return nil
}

func dbGetOrderByOwner(ctx context.Context, payId uint64) (o qrOrder, err error) {
	_, err = QR.DB.QueryOneContext(ctx, pg.Scan(&o.orderID, &o.paymentId, &o.amount),
		`select order_id, paymentid, amount FROM cashless where paymentid=?;`, payId)
	o.paymentIdStr = strconv.FormatInt(int64(o.paymentId), 10)
	return o, err
}

func dbGetOrderDecription(ctx context.Context, vmid int32, code string) (description string) {
	_, err := QR.DB.QueryOneContext(ctx, pg.Scan(&description),
		`SELECT name from CATALOG WHERE vmid= ?0 and code = ?1 limit 1;`,
		vmid, code)
	if err != nil {
		return fmt.Sprintf("код: %s", code)
	}
	return description
}

func startNotificationsReaderServer(ctx context.Context, s string) {
	u, err := url.Parse(s)
	if err != nil {
		QR.Log.Errorf("parce notification (%s) error(%v)", s, err)
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
		n, err = QR.terminalClient.ParseNotification(c.Request.Body)
		if n != nil && n.Status == tinkoff.StatusConfirmed {
			QR.Log.Infof("payment %+v", n)
		}
		QR.Log.Infof("notification from bank (%v)", n)
		if err != nil {
			QR.Log.Errorf("notification(%v) parse error(%v)", n, err)
			return
		}
		c.String(http.StatusOK, QR.terminalClient.GetNotificationSuccessResponse())
		o, err := dbGetOrderByOwner(ctx, n.PaymentID)
		_, _ = err, o
		if ok := o.compareOrder(n.OrderID, n.PaymentID); !ok {
			return
		}
		o.dbUpdateOrdreStatus(ctx, n.Status)

	})
	QR.Log.Notice("start bank notification server.")
	err = r.Run(u.Host)
	QR.Log.Errorf("error start notification server. error:%v", err)
}
