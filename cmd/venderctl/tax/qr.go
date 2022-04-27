package tax

import (
	"context"
	"fmt"
	"time"

	"github.com/AlexTransit/vender/tele"
	"github.com/AlexTransit/venderctl/internal/state"
	"github.com/go-pg/pg/v9"
	"github.com/nikita-vanyasin/tinkoff"
)

var CashLess struct {
	g    *state.Global
	Stop chan int32
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
	State         cashlessState
	PaymentID     string
	OrderID       string
	Amount        uint64
	Description   string
	Date          time.Time
	ToRoboMessage *tele.ToRoboMessage
}

var terminalClient *tinkoff.Client

func CashLessInit(ctx context.Context) bool {
	CashLess.g = state.GetGlobal(ctx)
	CashLessPay = make(map[int32]*CashLessOrderStruct)
	CashLess.Stop = make(chan int32, 1)
	if CashLess.g.Config.CashLess.TerminalTimeOutSec == 0 {
		CashLess.g.Config.CashLess.TerminalTimeOutSec = 30
	}
	if CashLess.g.Config.CashLess.TerminalBankCommission == 0 {
		CashLess.g.Config.CashLess.TerminalBankCommission = 45
	}
	if CashLess.g.Config.CashLess.TerminalMinimalAmount == 0 {
		CashLess.g.Config.CashLess.TerminalMinimalAmount = 1000
	}
	if tk := CashLess.g.Config.CashLess.TerminalKey; tk == "" {
		CashLess.g.Log.Info("tekminal key not foud. cashless system not start")
		return false
	}
	if tp := CashLess.g.Config.CashLess.TerminalPass; tp == "" {
		CashLess.g.Log.Info("tekminal password not foud. cashless system not start")
		return false
	}
	// terminalClient = &tinkoff.Client{}
	terminalClient = tinkoff.NewClient(CashLess.g.Config.CashLess.TerminalKey, CashLess.g.Config.CashLess.TerminalPass)
	return true
}

func CashLessStop() {
	CashLess.g.Log.Info("stop cashless system")
	CashLess.Stop <- 0 // send stop to all open transactions
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
	CashLess.g.Alive.Add(1)
	// db := CashLess.g.DB.Conn()
	defer func() {
		CashLess.g.Tele.SendToRobo(vmid, qro.ToRoboMessage)
		// _ = CashLess.g.DB.Close()
		CashLess.g.Alive.Done()
	}()
	if rm.Order.Amount < CashLess.g.Config.CashLess.TerminalMinimalAmount { // minimal bank amount
		CashLess.g.Log.Errorf("bank pay imposible. the amount is less than the minimum\n%#v", rm.Order.Amount)
		qro.ToRoboMessage.ShowQR.QrType = tele.ShowQR_error
		return
	}
	persentAmount := (rm.Order.Amount * CashLess.g.Config.CashLess.TerminalBankCommission) / 10000
	od := time.Now()
	qro.Vmid = vmid
	qro.OrderID = fmt.Sprintf("%d-%s-%s", vmid, od.Format("060102150405"), rm.Order.MenuCode)
	qro.Amount = uint64(rm.Order.Amount + persentAmount)
	qro.Date = od
	qro.Description = menuGetName(vmid, rm.Order.MenuCode)
	//-----------------------------------
	// res := tinkoff.InitResponse{
	// 	Amount:     1004,
	// 	OrderID:    "88-22042715000-101",
	// 	Status:     tinkoff.StatusNew,
	// 	PaymentID:  "123",
	// 	PaymentURL: "https://aa.aa/new/Oj3KTptg",
	// }
	// var err error
	// // err := fmt.Errorf("AAA")
	// /*
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
	// ---------------------------
	// qrr := tinkoff.GetQRResponse{
	// 	OrderID:   "88-22042715000-101",
	// 	Data:      "xfhgdjkfvhkjdhvfbkdjhvbkfjxfhbvjkdfhbvkx",
	// 	PaymentID: 123,
	// }
	// /*
	qrr, err := terminalClient.GetQR(&tinkoff.GetQRRequest{
		PaymentID: qro.PaymentID,
		// DataType:  "PAYLOAD",
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
	qro.State = CreateQR
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
	const q = `INSERT INTO cashless (vmid, create_date, payment_id, order_id, amount) VALUES ( ?0, ?1, ?2, ?3, ?4 );`
	_, err := CashLess.g.DB.Exec(q, o.Vmid, o.Date, o.PaymentID, o.OrderID, o.Amount)
	if err != nil {
		CashLess.g.Log.Errorf("qr db write:%v", err)
	}
}

func (o *CashLessOrderStruct) waitingForPayment() {
	tmr := time.NewTimer(time.Second * time.Duration(CashLess.g.Config.CashLess.TerminalTimeOutSec))
	refreshTime := time.Duration(time.Second * 3)
	refreshTimer := time.NewTimer(refreshTime)
	defer func() {
		tmr.Stop()
		refreshTimer.Stop()
	}()
	for {
		select {
		case vmid := <-CashLess.Stop:
			if vmid == o.Vmid || vmid == 0 {
				CashLess.g.Log.Infof("order cancel by command. order:%v", o)
				o.cancelOrder()
				return
			}
		case <-tmr.C:
			CashLess.g.Log.Infof("order cancel by timeout ")
			o.cancelOrder()
			return
		case <-refreshTimer.C:
			if s, err := terminalClient.GetState(&tinkoff.GetStateRequest{PaymentID: o.PaymentID}); err == nil {
				/*
					if true {
						var err error
						s := tinkoff.GetStateResponse{
							// Status: tinkoff.StatusNew,
							Status: tinkoff.PayTypeOneStep,
						}
						//*/
				if err != nil {
					o.cancelOrder()
					CashLess.g.Log.Errorf("cashless get status:", err)
					return
				}

				switch s.Status {
				case tinkoff.StatusConfirmed:
					o.writeDBOrderPaid()
					o.sendStartCook()
					return
				case tinkoff.StatusRejected:
					o.bankQRReject()
					return
				case tinkoff.StatusNew:
				default:
					// o.bankQRError()
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

func (o *CashLessOrderStruct) bankQRError() {
	sm := tele.ToRoboMessage{
		Cmd: tele.MessageType_showQR,
		ShowQR: &tele.ShowQR{
			QrType: tele.ShowQR_error,
		},
	}
	CashLess.g.Tele.SendToRobo(o.Vmid, &sm)
}

func (o *CashLessOrderStruct) sendStartCook() {
	sm := tele.ToRoboMessage{
		Cmd: tele.MessageType_makeOrder,
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
	cReq := &tinkoff.CancelRequest{
		PaymentID: o.PaymentID,
		Amount:    o.Amount,
	}
	cRes, err := terminalClient.Cancel(cReq)
	q := `UPDATE cashless SET finish_date = now() WHERE payment_id = ?0 and order_id = ?1;`
	switch cRes.Status {
	case tinkoff.StatusQRRefunding:
		q = `UPDATE cashless SET state = 'order_cancel', finish_date = now(), credited = 0 WHERE payment_id = ?0 and order_id = ?1;`
	default:
		CashLess.g.Log.Errorf("tinkoff fail cancel (%v) error:%v", o, err)
	}

	r, err := CashLess.g.DB.Exec(q, o.PaymentID, o.OrderID)
	if err != nil || r.RowsAffected() != 1 {
		CashLess.g.Log.Errorf("fail db update:%v", err)
	}
	o.ToRoboMessage.ShowQR = &tele.ShowQR{}
	o.ToRoboMessage.ShowQR.QrType = tele.ShowQR_error
	CashLess.g.Tele.SendToRobo(o.Vmid, o.ToRoboMessage)
	delete(CashLessPay, o.Vmid)
}

func (o *CashLessOrderStruct) writeDBOrderPaid() {
	const q = `UPDATE cashless SET state = 'order_prepay', credit_date = now(), credited = ?2 WHERE payment_id = ?0 and order_id = ?1;`
	r, err := CashLess.g.DB.Exec(q, o.PaymentID, o.OrderID, o.Amount)
	if err != nil || r.RowsAffected() != 1 {
		CashLess.g.Log.Errorf("fail db update:%v", err)
	}
}

func (o *CashLessOrderStruct) writeDBOrderComplete() {
	const q = `UPDATE cashless SET state = 'order_complete', finish_date = now() WHERE payment_id = ?0 and order_id = ?1;`
	r, err := CashLess.g.DB.Exec(q, o.PaymentID, o.OrderID, o.Amount)
	if err != nil || r.RowsAffected() != 1 {
		CashLess.g.Log.Errorf("fail db update:%v", err)
	}
	delete(CashLessPay, o.Vmid)
	// go waitingReceipt(o)
}

// func waitingReceipt(o *state.CashLessOrderStruct) {
// 	time.Sleep(time.Second * time.Duration(CashLess.g.Config.CashLess.TerminalTimeOutSec))
// 	delete(state.CashLessPay, o.Vmid)
// }

// func updateDBOrderReceipt(o *state.CashLessOrderStruct) {
// 	const q = `UPDATE cashless SET state = 'order_complete', finish_date = now() WHERE payment_id = ?0 and order_id = ?1;`
// 	r, err := CashLess.g.DB.Exec(q, o.PaymentID, o.OrderID, o.Amount)
// 	if err != nil || r.RowsAffected() != 1 {
// 		CashLess.g.Log.Errorf("fail db update:%v", err)
// 	}
// 	go waitingReceipt(o)
// }
