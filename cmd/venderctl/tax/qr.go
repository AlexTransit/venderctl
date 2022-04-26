package tax

import (
	"context"
	"fmt"
	"time"

	"github.com/AlexTransit/vender/tele"
	"github.com/AlexTransit/venderctl/internal/state"
	tele_api "github.com/AlexTransit/venderctl/internal/tele/api"
	"github.com/go-pg/pg/v9"
	"github.com/nikita-vanyasin/tinkoff"
)

var CashLessPay map[int32]*CashLessOrderStruct
var CashLess struct {
	g      *state.Global
	client *tinkoff.Client
	Stop   chan int32
}

type cashlessState uint8

const (
	createPaimentID cashlessState = iota
	createQR
	paid
	cooking
	complete
)

type CashLessOrderStruct struct {
	vmid        int32
	state       cashlessState
	PaymentID   string
	OrderID     string
	Amount      uint64
	Description string
	Date        time.Time
	// StatrtCooking time.Time
	ToRoboMessage *tele.ToRoboMessage
}

func CashLessInit(ctx context.Context) {
	CashLess.g = state.GetGlobal(ctx)
	CashLessPay = make(map[int32]*CashLessOrderStruct)
	CashLess.Stop = make(chan int32, 1)
	if CashLess.g.Config.CashLess.TerminalTimeOutSec == 0 {
		CashLess.g.Config.CashLess.TerminalTimeOutSec = 30
	}
	if CashLess.g.Config.CashLess.TerminalBankCommission == 0 {
		CashLess.g.Config.CashLess.TerminalBankCommission = 70
	}
	if CashLess.g.Config.CashLess.TerminalMinimalAmount == 0 {
		CashLess.g.Config.CashLess.TerminalMinimalAmount = 1000
	}

}

func CashLessStop() {
	CashLess.Stop <- 0 // send stop to all open transactions
}

func MakeQr(ctx context.Context, vmid int32, rm *tele.FromRoboMessage) {
	if _, ok := CashLessPay[vmid]; ok {
		if CashLessPay[vmid].PaymentID != "" {
			CashLess.g.Log.Errorf("qr for vmid %d created (%#v)", vmid, CashLessPay[vmid])
			return
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
		CashLess.g.Log.Errorf("bank pay imposible. minimal amount %#v", rm.Order.Amount)
		qro.ToRoboMessage.ShowQR.QrType = tele.ShowQR_error
		return
	}
	persentAmount := (rm.Order.Amount * CashLess.g.Config.CashLess.TerminalBankCommission) / 10000
	od := time.Now()
	qro.vmid = vmid
	qro.OrderID = fmt.Sprintf("%d-%s-%s", vmid, od.Format("060102150405"), rm.Order.MenuCode)
	qro.Amount = uint64(rm.Order.Amount + persentAmount)
	qro.Date = od
	qro.Description = menuGetName(vmid, rm.Order.MenuCode)
	CashLess.client = tinkoff.NewClient(CashLess.g.Config.CashLess.TerminalKey, CashLess.g.Config.CashLess.TerminalPass)
	/*
		for test
		res, err := CashLess.client.Init(&tinkoff.InitRequest{
			Amount:      qro.Amount,
			OrderID:     qro.OrderID,
			Description: qro.Description,
			Data:        map[string]string{"Vmc": fmt.Sprintf("%d", vmid)},
		})
		//*/
	res := tinkoff.InitResponse{
		Status:    tinkoff.StatusNew,
		PaymentID: "123",
	}
	var err error
	// end for test
	if err != nil || res.Status != tinkoff.StatusNew {
		CashLess.g.Log.Errorf("bank pay init error:%v", err)
		qro.ToRoboMessage.ShowQR.QrType = tele.ShowQR_error
		return
	}
	qro.PaymentID = res.PaymentID
	//for test
	// qrr, err := client.GetQR(&tinkoff.GetQRRequest{
	// 	PaymentID: qro.PaymentID,
	// 	DataType:  "PAYLOAD",
	// })
	qrr := tinkoff.GetQRResponse{
		Data: "data for qr",
	}
	// end for test
	if err != nil {
		CashLess.g.Log.Errorf("bank get QR error:%v", err)
		qro.ToRoboMessage.ShowQR.QrType = tele.ShowQR_error
		return
	}
	qro.ToRoboMessage.ShowQR.QrText = qrr.Data
	qro.ToRoboMessage.ShowQR.QrType = tele.ShowQR_order
	qro.ToRoboMessage.ShowQR.DataInt = int32(qro.Amount)
	qro.state = createQR
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
	_, err := CashLess.g.DB.Exec(q, o.vmid, o.Date, o.PaymentID, o.OrderID, o.Amount)
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
			if vmid == o.vmid || vmid == 0 {
				CashLess.g.Log.Infof("order cancel by command. order:%v", o)
				cancelOrder(o)
				return
			}
		case <-tmr.C:
			CashLess.g.Log.Infof("order cancel by timeout ")
			cancelOrder(o)
			return
		case <-refreshTimer.C:
			// test
			// if s, err := CashLess.client.GetState(&tinkoff.GetStateRequest{PaymentID: o.PaymentID}); err == nil {
			var err error
			var s tinkoff.GetStateResponse
			s.Status = tinkoff.StatusConfirmed
			// s.Status = tinkoff.StatusNew
			// err = fmt.Errorf("aaa")
			if err != nil {
				cancelOrder(o)
				return
			}
			if s.Status == tinkoff.StatusConfirmed {
				// end test
				CashLess.g.Log.Errorf("cashless get status:", err)
				if s.Status == tinkoff.StatusConfirmed {
					o.witeDBOrderPaid()
					o.sendStartCook()
					// go WaitingCompleteOrder(o)
					return
				}
			}
			refreshTimer.Reset(refreshTime)
		}
	}
}

func (o *CashLessOrderStruct) WaitingCompleteOrder() {
	mqttch := CashLess.g.Tele.Chan()
	for {
		select {
		case vmid := <-CashLess.Stop:
			if vmid == o.vmid || vmid == 0 {
				CashLess.g.Log.Infof("order cancel by command. order:%v", o)
				cancelOrder(o)
				return
			}
		case p := <-mqttch:
			fmt.Printf("\n\033[41m QRRRR IN-%v \033[0m\n\n", p)
			rm := CashLess.g.ParseFromRobo(p)
			// fmt.Printf("\n\033[41m AAAAAAA(%v)(%v) \033[0m\n\n", p.VmId, rm)
			if p.VmId == o.vmid && p.Kind == tele_api.FromRobo {
				if rm.Order != nil {
					switch rm.Order.OrderStatus {
					case tele.OrderStatus_orderError:
						cancelOrder(o)
						return
					case tele.OrderStatus_complete:
						// fmt.Printf("\n\033[41m ordercomplete \033[0m\n\n")
						// return
					}

				}
				// fmt.Printf("\n\033[41m RRRRRRRRRR:%v \033[0m\n\n", rm)

			}
		}
	}
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
	CashLess.g.Tele.SendToRobo(o.vmid, &sm)
}

func cancelOrder(o *CashLessOrderStruct) {
	// cReq := &tinkoff.CancelRequest{
	// 	PaymentID: o.OrderID,
	// 	Amount:    o.Amount,
	// }
	// cRes, err := CashLess.client.Cancel(cReq)
	var err error = nil
	cRes := tinkoff.CancelResponse{
		BaseResponse: tinkoff.BaseResponse{
			Success: true,
		},
		Status: tinkoff.StatusCanceled,
	}
	x := cRes.Status != tinkoff.StatusCanceled
	if err != nil || !cRes.BaseResponse.Success || (x || cRes.Status != tinkoff.StatusRefunded) {
		CashLess.g.Log.Errorf("tinkoff fail cancel (%v) error:%v", o, err)
	}
	const q = `UPDATE cashless SET state = 'order_cancel', finish_date = now(), credited = 0 WHERE payment_id = ?0 and order_id = ?1;`
	r, err := CashLess.g.DB.Exec(q, o.PaymentID, o.OrderID)
	if err != nil || r.RowsAffected() != 1 {
		CashLess.g.Log.Errorf("fail db update:%v", err)
	}
	o.ToRoboMessage.ShowQR = &tele.ShowQR{}
	o.ToRoboMessage.ShowQR.QrType = tele.ShowQR_error
	CashLess.g.Tele.SendToRobo(o.vmid, o.ToRoboMessage)
	delete(CashLessPay, o.vmid)
}

func (o *CashLessOrderStruct) witeDBOrderPaid() {
	const q = `UPDATE cashless SET state = 'order_prepay', credit_date = now(), credited = ?2 WHERE payment_id = ?0 and order_id = ?1;`
	r, err := CashLess.g.DB.Exec(q, o.PaymentID, o.OrderID, o.Amount)
	if err != nil || r.RowsAffected() != 1 {
		CashLess.g.Log.Errorf("fail db update:%v", err)
	}

}

func (o *CashLessOrderStruct) witeDBOrderComplete() {
	const q = `UPDATE cashless SET state = 'order_complete', credit_date = now() WHERE payment_id = ?0 and order_id = ?1;`
	r, err := CashLess.g.DB.Exec(q, o.PaymentID, o.OrderID, o.Amount)
	if err != nil || r.RowsAffected() != 1 {
		CashLess.g.Log.Errorf("fail db update:%v", err)
	}

}
