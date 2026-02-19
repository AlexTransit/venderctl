package state

import (
	"fmt"

	vender_api "github.com/AlexTransit/vender/tele"
)

type client struct {
	Name         string
	telegrammId  int64  //telegramm id
	Balance      int64  // client balanse. possible minus <= credit
	Credit       uint32 //client credit
	Diskont      int    //persent discont
	Defaultrobot int
	Ban          bool //banned client
}

func (g *Global) ClientGet(clientId int64) (cl client, err error) {
	db := g.DB.Conn()
	defer db.Close()
	_, err = db.QueryOne(&cl, `SELECT ban, name, balance, credit, diskont, defaultrobot FROM tg_user WHERE userid = ?;`, clientId)
	if err != nil {
		return cl, err
	}
	if cl.Ban {
		return cl, fmt.Errorf("клиент забанен")
	}
	return cl, nil
}

func (g *Global) ClientChangeDefaultRobot(clientId int64, vmid int) error {
	return nil
}

// check robot state
// если false то в ошибке присылает почему не может (нет связи, работает с другим клиентом и т.д)
func (g *Global) СheckRobotMakeState(vmid int32) (valid bool, messageImpossible string) {
	roboState := g.GetRoboState(vmid)
	if roboState == vender_api.State_Nominal {
		return true, ""
	}
	switch roboState {
	case vender_api.State_Invalid:
		messageImpossible = "автомат не в сети.\nили отключено электричество, или недоступен интернет.\n"
	case vender_api.State_Client, vender_api.State_RemoteControl, vender_api.State_WaitingForExternalPayment:
		messageImpossible = "автомат сейчас работает с другим клиентом.\n"
	case vender_api.State_Broken:
		messageImpossible = "автомат сломался.\nподождите пока его реанимируют (возможно его починят дистанционно)"
	default:
		messageImpossible = "автомат сейчас не может выполнить заказ. \n"
	}

	return false, messageImpossible
}
