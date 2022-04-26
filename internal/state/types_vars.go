package state

import (
	"time"

	"github.com/AlexTransit/vender/log2"
	"github.com/AlexTransit/vender/tele"
	vender_api "github.com/AlexTransit/vender/tele"
	tele_api "github.com/AlexTransit/venderctl/internal/tele/api"
	"github.com/go-pg/pg/v9"
	"github.com/temoto/alive/v2"
)

const ContextKey = "run/state-global"

type Global struct {
	Alive        *alive.Alive
	BuildVersion string
	Config       *Config
	DB           *pg.DB
	Log          *log2.Log
	Tele         tele_api.Teler
	Vmc          map[int32]vmcStruct
}

type vmcStruct struct {
	Connect bool
	State   vender_api.State
}

// CashLess
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
	Vmid        int32
	State       cashlessState
	PaymentID   string
	OrderID     string
	Amount      uint64
	Description string
	Date        time.Time
	// StatrtCooking time.Time
	ToRoboMessage *tele.ToRoboMessage
}

// END CashLess
