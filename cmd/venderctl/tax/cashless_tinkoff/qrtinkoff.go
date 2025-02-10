package cashless_tinkoff

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/AlexTransit/vender/currency"
	"github.com/AlexTransit/vender/log2"
	"github.com/AlexTransit/vender/tele"
	"github.com/AlexTransit/venderctl/internal/state"
	tele_api "github.com/AlexTransit/venderctl/internal/tele/api"
	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v9"
	"github.com/nikita-vanyasin/tinkoff"
)

var QR struct {
	*state.Global
	qrDb           *pg.DB
	terminalClient *tinkoff.Client
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

type qrOrder struct {
	Order_state      orderState
	Bank_order_state int32
	Vmid             int32
	date             time.Time
	OrderID          string
	Amount           uint64
	Credited         uint64
	Paymentid        uint64
	paymentIdStr     string
	description      string
}

func QrInit(ctx context.Context) {
	QR.Global = state.GetGlobal(ctx)
	QR.qrDb = (*pg.DB)(QR.DB.Conn().WithParam("worker", "QR").WithTimeout(5 * time.Second))
	QR.Log.Info("start init QR")
	if QR.Config.CashLess.DebugLevel < 1 || QR.Config.CashLess.DebugLevel > 7 {
		QR.Log.SetLevel(log2.LOG_DEBUG)
	} else {
		QR.Log.SetLevel(log2.Level(QR.Config.CashLess.DebugLevel))
	}
	QR.Config.CashLess.QRValidTimeSec = state.ConfigInt(QR.Config.CashLess.QRValidTimeSec, 10, 300)
	QR.Config.CashLess.TerminalQRPayRefreshSec = state.ConfigInt(QR.Config.CashLess.TerminalQRPayRefreshSec, 2, 10)
	QR.Config.CashLess.TerminalMinimalAmount = state.ConfigInt(QR.Config.CashLess.TerminalMinimalAmount, 1000, 1000)
	QR.Config.CashLess.TimeoutToStartManualPaymentVerificationSec = state.ConfigInt(QR.Config.CashLess.TimeoutToStartManualPaymentVerificationSec, 2, 30)

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
		/* test -------------------------------------------------------------------------------------
		tinkoff.WithBaseURL("https://aaa.aaa/v2"),
		//*/
	)
	go startNotificationsReaderServer(ctx, QR.Config.CashLess.URLToListenToBankNotifications) // обработчик собитий от банки
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
					switch rm.Order.OrderStatus {
					case tele.OrderStatus_executionStart:
						QR.Log.Infof("robot:%d started make order:%s paymentId:%d amount:%d ", o.Vmid, o.OrderID, o.Paymentid, o.Amount)
						o.dbStartExecution(ctx)
					case tele.OrderStatus_orderError:
						QR.Log.Errorf("from robot. cooking error. order (%+v)", o)
						o.cancelOrder(ctx)
					case tele.OrderStatus_cancel:
						o.cancelOrder(ctx)
					case tele.OrderStatus_waitingForPayment:
					case tele.OrderStatus_complete:
						o.dbComplete(ctx)
						QR.Log.NoticeF("from robot. vm%d cashless complete order:%s price:%s payer:%d ", o.Vmid, o.OrderID, currency.Amount(o.Amount).Format100I(), o.Paymentid)
					default:
					}
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

func createQR(ctx context.Context, vmid int32, rm *tele.FromRoboMessage) {
	QR.Alive.Add(1)
	messageForRobot := tele.ToRoboMessage{
		Cmd: tele.MessageType_showQR,
		ShowQR: &tele.ShowQR{
			QrType: tele.ShowQR_error,
		},
	}
	defer func() {
		QR.Tele.SendToRobo(vmid, &messageForRobot)
		QR.Log.Debugf("-> robo(%d) type(%+v) ", vmid, messageForRobot)
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
		bankCommision += 10
	}
	bankCommision /= 10
	orderCreateDate := time.Now()
	order := qrOrder{
		Order_state: order_start,
		Vmid:        vmid,
		date:        orderCreateDate,
		OrderID:     fmt.Sprintf("%d-%s-%s", vmid, orderCreateDate.Format("060102150405"), rm.Order.MenuCode),
		Amount:      uint64(rm.Order.Amount) + uint64(bankCommision),
		description: dbGetOrderDecription(ctx, vmid, rm.Order.MenuCode),
	}
	/* test -------------------------------------------------------------------------------------
	order.Paymentid = uint64(time.Now().Unix())
	order.Paymentid = 5414930555
	order.OrderID = "88-241201101710-0"
	//*/
	if ok := order.initPaySession(ctx); !ok {
		return
	}
	if ok := order.dbCreateOrder(ctx); !ok {
		return
	}
	ok, qrData := order.getQrCode(ctx)
	/* test -------------------------------------------------------------------------------------
	qrData = "AAAAAAAAAAAAA"
	ok = true
	//*/
	if !ok {
		return
	}
	messageForRobot.ShowQR.QrText = qrData
	messageForRobot.ShowQR.QrType = tele.ShowQR_order
	messageForRobot.ShowQR.DataInt = int32(order.Amount)
	messageForRobot.ShowQR.DataStr = order.paymentIdStr

	go order.manualyPaymentVerification(ctx)
	QR.Log.Infof("robo(%d) show QR for payment order(%s) ", vmid, order.OrderID)
	QR.Alive.Done()
}

func (o *qrOrder) initPaySession(ctx context.Context) (valid bool) {
	ir := &tinkoff.InitRequest{
		BaseRequest: tinkoff.BaseRequest{TerminalKey: QR.Config.CashLess.TerminalKey, Token: "random"},
		Amount:      o.Amount,
		OrderID:     o.OrderID,
		Description: o.description,
		Data: map[string]string{
			"Vmc": fmt.Sprintf("%d", o.Vmid),
			"QR":  "true",
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
		/* test -------------------------------------------------------------------------------------------------
		e = nil
		bankResponse = &tinkoff.InitResponse{
			Amount:     o.Amount,
			OrderID:    o.OrderID,
			Status:     tinkoff.StatusNew,
			PaymentID:  fmt.Sprint(o.Paymentid),
			PaymentURL: "HZ",
		}
		//*/
		if e == nil {
			break
		}
		QR.Log.Errorf("init payment order(%+v) error (%v)", o, e)
		errStr = fmt.Sprintf("(%d error - %v) ", i, e.Error()) + errStr
		if i == 3 {
			QR.VMCErrorWriteDb(o.Vmid, errStr)
			return false
		}
	}
	if bankResponse.Status != tinkoff.StatusNew {
		QR.VMCErrorWriteDb(o.Vmid, fmt.Sprintf("init payment status (need NEW) order(%+v) bark response(%+v)", o, bankResponse))
		return false
	}
	o.paymentIdStr = bankResponse.PaymentID
	i, e := strconv.ParseInt(bankResponse.PaymentID, 10, 64)
	if e != nil {
		QR.VMCErrorWriteDb(o.Vmid, fmt.Sprintf("bank paimentid(%s) not number. responce(%+v) error(%v)", bankResponse.PaymentID, bankResponse, e))
		return false
	}
	o.Paymentid = uint64(i)
	return true
}

func (o *qrOrder) getQrCode(ctx context.Context) (valid bool, data string) {
	ctxWichTimeOut, cancel := context.WithTimeout(ctx, time.Second*2)
	qrRequest := &tinkoff.GetQRRequest{PaymentID: o.paymentIdStr}
	qrResponse, err := QR.terminalClient.GetQRWithContext(ctxWichTimeOut, qrRequest)
	cancel()
	if err != nil {
		QR.VMCErrorWriteDb(o.Vmid, fmt.Sprintf("bank get QR error:%v order id:%s", err, o.OrderID))
		return false, ""
	}
	if qrResponse.PaymentID != int(o.Paymentid) {
		QR.VMCErrorWriteDb(o.Vmid, fmt.Sprintf("bank QR paymentID mismatch response(%v) order(%v) response(%v) ", qrResponse, o, qrResponse))
		return false, ""
	}
	return true, qrResponse.Data
}

func (o *qrOrder) manualyPaymentVerification(ctx context.Context) {
	QR.Log.Infof("run checking payment status order(%s) ", o.OrderID)
	QR.Alive.Add(1)
	qrTimeout := time.NewTimer(time.Second * time.Duration(QR.Config.CashLess.QRValidTimeSec))
	refreshTime := time.Duration(time.Second * time.Duration(QR.Config.CashLess.TerminalQRPayRefreshSec))
	// start the poll timer after timeout. запускаем таймер опроса после задержки
	refreshTimer := time.NewTimer(time.Second * time.Duration(QR.Config.CashLess.TimeoutToStartManualPaymentVerificationSec))
	defer func() {
		refreshTimer.Stop()
		//		cancelQRctx()
		QR.Log.Infof("stop checking payment status order(%s) ", o.OrderID)
		QR.Alive.Done()
	}()
	/* test -------------------------------------------------------------------------------------
	go func() {
		time.Sleep(10 * time.Second)
		fmt.Printf("\033[41m test \033[0m\n")
		_, _ = QR.terminalClient.SBPPayTestWithContext(ctx, &tinkoff.SBPPayTestRequest{
			PaymentID:         o.paymentIdStr,
			IsDeadlineExpired: false,
			IsRejected:        false,
		})
	}()
	//*/
	stpch := QR.Alive.StopChan()
	for {
		select {
		case <-refreshTimer.C:
			refreshTimer.Reset(refreshTime)
			ctxSessionTimeOut, cancelSession := context.WithTimeout(ctx, time.Second*5)
			orderState, err := QR.terminalClient.GetStateWithContext(ctxSessionTimeOut, &tinkoff.GetStateRequest{PaymentID: o.paymentIdStr})
			cancelSession()
			/* test -------------------------------------------------------------------------------------
			fmt.Printf("\033[41m read bank state \033[0m\n")
			orderState = new(tinkoff.GetStateResponse)
			orderState.Status = tinkoff.StatusConfirmed
			orderState.PaymentID = o.paymentIdStr
			orderState.OrderID = o.OrderID
			err = nil
			//*/
			if err != nil {
				QR.Log.Errorf("check state order(%s) error(%v)", o.OrderID, err)
				continue
			}
			if ok := o.compareOrder(orderState.OrderID, orderState.PaymentID); !ok {
				continue
			}
			o.dbGetOrderStatus(ctx)
			if refresh := o.dbUpdateOrdreStatus(ctx, orderState.Status); !refresh {
				return
			}
		case <-stpch:
			return
		case <-qrTimeout.C:
			QR.Log.Debugf("QR canceled by timeout order(%s) ", o.OrderID)
			o.cancelOrder(ctx)
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
		if o.Paymentid == paymentID {
			ok = true
		}
	default:
		ok = false
	}
	if ok && o.OrderID == orderID {
		return true
	}
	QR.VMCErrorWriteDb(o.Vmid, fmt.Sprintf("BAD BANK. request mismash orderID<>responseID (%s<>%s) or paymentid<>responseID (%s<>%+v)", o.OrderID, orderID, o.paymentIdStr, paymentID))
	return false
}

func (o *qrOrder) sendMessageMakeOrder(ctx context.Context) {
	o.dbGetOrderStatus(ctx)
	if o.Order_state > order_prepay {
		return
	}
	mToRobo := tele.ToRoboMessage{
		Cmd:        tele.MessageType_makeOrder,
		ServerTime: time.Now().Unix(),
		MakeOrder: &tele.Order{
			Amount:        uint32(o.Amount),
			OrderStatus:   tele.OrderStatus_doSelected,
			PaymentMethod: tele.PaymentMethod_Cashless,
			OwnerInt:      int64(o.Paymentid),
			OwnerType:     tele.OwnerType_qrCashLessUser,
		},
	}
	dbUpdate(ctx, fmt.Sprintf(`UPDATE cashless SET order_state = %d WHERE order_id = '%s';`, order_prepay, o.OrderID))
	QR.Tele.SendToRobo(o.Vmid, &mToRobo)
	QR.Log.Infof("->robo (%+v)", mToRobo)
}

func (o *qrOrder) sendMessageImpossibleMake(ctx context.Context) {
	o.dbGetOrderStatus(ctx)
	if o.Order_state == order_cancel {
		return
	}
	mToRobo := tele.ToRoboMessage{
		Cmd: tele.MessageType_showQR,
		ShowQR: &tele.ShowQR{
			QrType: tele.ShowQR_errorOverdraft,
		},
	}
	dbUpdate(ctx, fmt.Sprintf(`UPDATE cashless SET order_state = %d WHERE order_id = '%s';`, order_cancel, o.OrderID))
	QR.Tele.SendToRobo(o.Vmid, &mToRobo)
}

func (o *qrOrder) dbComplete(ctx context.Context) {
	query := fmt.Sprintf(`UPDATE cashless SET order_state = %d, finish_date = now() WHERE order_id = '%s';`, order_complete, o.OrderID)
	dbUpdate(ctx, query)
}

func (o *qrOrder) dbStartExecution(ctx context.Context) {
	query := fmt.Sprintf(`UPDATE cashless SET order_state = %d, finish_date = now() WHERE order_id = '%s';`, order_execute, o.OrderID)
	dbUpdate(ctx, query)
}

func (o *qrOrder) cancelOrder(ctx context.Context) {
	o.dbGetOrderStatus(ctx)
	if o.Order_state == order_cancel {
		return
	}
	QR.Log.Debugf("begin QR cancel procedure. bank order state:%s. order(%s)", getBankOrderStatusName(o.Bank_order_state), o.OrderID)
	if o.Order_state >= order_complete {
		QR.Log.Info("ignore QR cancel. order complete or canceled")
		return
	}
	dbUpdate(ctx, fmt.Sprintf(`UPDATE cashless SET order_state = %d WHERE order_id = '%s';`, order_cancel, o.OrderID))
	if o.Order_state <= order_start && o.Bank_order_state <= 2 {
		o.Credited = 0
	}
	cReq := &tinkoff.CancelRequest{
		PaymentID: o.paymentIdStr,
		Amount:    o.Credited,
	}
	cRes, err := QR.terminalClient.Cancel(cReq)
	/* test -------------------------------------------------------------------------------------
	cRes = &tinkoff.CancelResponse{
		OriginalAmount: 0,
		NewAmount:      0,
		OrderID:        o.OrderID,
		Status:         tinkoff.StatusQRRefunding,
		PaymentID:      o.paymentIdStr,
	}
	err = nil
	//*/
	QR.Log.Debugf("bank cancel order(%s) error(%+v) request(%+v) response(%+v)", o.OrderID, err, cReq, cRes)
	if err != nil {
		QR.VMCErrorWriteDb(o.Vmid, "error cansel order "+o.OrderID)
		return
	}
	if ok := o.compareOrder(cRes.OrderID, cRes.PaymentID); !ok {
		return
	}
	o.Amount = cRes.NewAmount
	o.dbUpdateOrdreStatus(ctx, cRes.Status)
}

func (o *qrOrder) dbCreateOrder(ctx context.Context) bool {
	const q = `INSERT INTO cashless (order_state, vmid, paymentid, create_date, order_id, amount, bank_order_state) VALUES ( ?0 ,?1, ?2, NOW(), ?3, ?4, ?5 );`
	_, err := QR.qrDb.ExecContext(
		ctx,
		q,
		order_start,
		o.Vmid,
		o.Paymentid,
		o.OrderID,
		o.Amount,
		getBankOrderStatusIndex(tinkoff.StatusNew),
	)
	if err == nil {
		return true
	}
	QR.VMCErrorWriteDb(o.Vmid, fmt.Sprintf("create order(%+v) in database error(%v)", o, err))
	return false
}

func (o *qrOrder) dbGetOrderStatus(ctx context.Context) (needsProcessing bool) {
	odb := qrOrder{}
	query := fmt.Sprintf("SELECT order_state, bank_order_state FROM cashless WHERE order_id = '%s'", o.OrderID)
	_, err := QR.qrDb.QueryOneContext(ctx, &odb, query)
	if err != nil {
		QR.VMCErrorWriteDb(o.Vmid, fmt.Sprintf("read order(%s) from db error(%v)", o.OrderID, err))
		return false
	}
	o.Order_state = odb.Order_state
	if odb.Bank_order_state != o.Bank_order_state {
		needsProcessing = true
	}
	o.Bank_order_state = odb.Bank_order_state
	return
}

func (o *qrOrder) dbUpdateOrdreStatus(ctx context.Context, bankOrderStatusStr string, payer ...string) (refresh bool) {
	bankOrderStatusI := getBankOrderStatusIndex(bankOrderStatusStr)
	if bankOrderStatusI == 0 {
		QR.VMCErrorWriteDb(0, "undefined new bank status ("+bankOrderStatusStr+")", 2)
	}
	var query string
	payerOwn := ""
	if payer != nil {
		payerOwn = payer[0]
	}
	switch bankOrderStatusStr {
	case tinkoff.StatusAuthorizing:
		query = fmt.Sprintf(`UPDATE cashless SET bank_order_state = %d, payer = '%s' WHERE order_id = '%s';`, bankOrderStatusI, payerOwn, o.OrderID)
		refresh = true
	case tinkoff.StatusConfirmed:
		query = fmt.Sprintf(`UPDATE cashless SET bank_order_state = %d, credited = %d, payer = '%s', credit_date = NOW() WHERE order_id = '%s';`, bankOrderStatusI, o.Amount, payerOwn, o.OrderID)
		o.Credited = o.Amount
		if o.Bank_order_state != bankOrderStatusI {
			defer o.sendMessageMakeOrder(ctx)
		}
		refresh = false
	case tinkoff.StatusCanceled,
		tinkoff.StatusRefunded,
		tinkoff.StatusQRRefunding,
		tinkoff.StatusPartialRefunded:
		query = fmt.Sprintf(`UPDATE cashless SET bank_order_state = %d, credited = credited - %d, finish_date = NOW() WHERE order_id = '%s';`, bankOrderStatusI, o.Amount, o.OrderID)
		refresh = false
	case tinkoff.StatusDeadlineExpired,
		tinkoff.StatusAuthFail,
		tinkoff.StatusRejected:
		query = fmt.Sprintf(`UPDATE cashless SET bank_order_state = %d, finish_date = NOW() WHERE order_id = '%s';`, bankOrderStatusI, o.OrderID)
		if o.Bank_order_state != bankOrderStatusI {
			defer o.sendMessageImpossibleMake(ctx)
		}
		refresh = false
	default:
		query = fmt.Sprintf(`UPDATE cashless SET bank_order_state = %d WHERE order_id = '%s';`, bankOrderStatusI, o.OrderID)
		refresh = true
	}
	if o.Bank_order_state != bankOrderStatusI {
		dbUpdate(ctx, query)
	}
	return refresh
}

func dbUpdate(ctx context.Context, query interface{}, params ...interface{}) error {
	r, err := QR.qrDb.ExecContext(ctx, query, params...)
	if err != nil || r.RowsAffected() != 1 {
		QR.VMCErrorWriteDb(0, fmt.Sprintf("fail db update sql(%s) parameters (%+v) error(%v)", query, params, err), 2)
		return err
	}
	return nil
}

func dbGetOrderByOwner(ctx context.Context, payId uint64) (qrOrder, error) {
	O := qrOrder{}
	query := fmt.Sprintf(`SELECT vmid, order_id, paymentid, amount, credited, order_state, bank_order_state FROM cashless WHERE paymentid=%d;`, payId)
	_, err := QR.qrDb.QueryOneContext(ctx, &O, query)
	if err != nil {
		QR.Log.Errorf("db get order by payer(%d) error(%v)", payId, err)
		return O, err
	}
	O.paymentIdStr = strconv.FormatInt(int64(O.Paymentid), 10)
	return O, err
}

func dbGetOrderDecription(ctx context.Context, vmid int32, code string) (Name string) {
	_, err := QR.qrDb.QueryOneContext(ctx, &Name,
		`SELECT name from CATALOG WHERE vmid= ?0 and code = ?1 limit 1;`,
		vmid, code)
	if err != nil {
		return fmt.Sprintf("код: %s", code)
	}
	return Name
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
		/* test -------------------------------------------------------------------------------------
		n = &tinkoff.Notification{
			OrderID:   "88-241201101710-0",
			PaymentID: 5414930555,
			Amount:    1005,
			PAN:       "+7 963 012 9955",
		}
		n.PaymentID = 5414930555
		b, _ := io.ReadAll(c.Request.Body)
		switch b[0] {
		case 'a':
			n.Status = tinkoff.StatusConfirmed
		case 'c':
			n.Status = tinkoff.StatusCanceled
		default:
			n.Status = tinkoff.StatusAuthorized
		}
		/*/
		n, err = QR.terminalClient.ParseNotification(c.Request.Body)
		QR.Log.Debugf("notification from bank (%+v)", n)
		if err != nil {
			QR.Log.Errorf("notification(%v) parse error(%v)", n, err)
			return
		}
		c.String(http.StatusOK, QR.terminalClient.GetNotificationSuccessResponse())
		//*/
		o, err := dbGetOrderByOwner(ctx, n.PaymentID)
		if err != nil {
			QR.Log.Errorf("unknown payer(%+v)", n)
			QR.VMCErrorWriteDb(0, fmt.Sprintf("notification for unknown payer(%d)", n.PaymentID))
			return
		}
		if ok := o.compareOrder(n.OrderID, n.PaymentID); !ok {
			return
		}
		o.dbUpdateOrdreStatus(ctx, n.Status, n.PAN)
	})
	QR.Log.Notice("start bank notification server.")
	err = r.Run(u.Host)
	QR.Log.Errorf("error start notification server. error:%v", err)
}
