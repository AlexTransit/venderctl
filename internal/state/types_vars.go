package state

import (
	"github.com/AlexTransit/vender/log2"
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
