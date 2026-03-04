package state

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	vender_api "github.com/AlexTransit/vender/tele"
	"github.com/go-pg/pg/v9"
)

type Client struct {
	Name         string
	Id           int64
	Balance      int64  // client balanse. possible minus <= credit
	Credit       uint32 //client credit
	clientType   int32
	Diskont      int //persent discont
	Defaultrobot int
	Ban          bool //banned client
}

func (g *Global) ClientGet(clientId int64, clientType int32) (cl Client, err error) {
	db := g.DB.Conn()
	defer db.Close()
	var sql string
	switch vender_api.OwnerType(clientType) {
	case vender_api.OwnerType_webUser:
		sql = `SELECT ban, name, balance, credit, diskont, defaultrobot FROM web_user WHERE userid = ?0;`
		// cl, err = h.App.WebClientGet(userId)
	case vender_api.OwnerType_telegramUser:
		sql = `SELECT ban, name, balance, credit, diskont, defaultrobot FROM tg_user WHERE userid = ?0;`
	default:
		return cl, fmt.Errorf("unknown user type:%d", clientType)
	}
	_, err = db.QueryOne(&cl, sql, clientId)
	if err != nil {
		return cl, err // error
	}
	if cl.Ban {
		return cl, fmt.Errorf("клиент забанен")
	}
	return cl, nil
}
func (g *Global) ClientUpdateBalance(change int64, clientId int64, clientType int32) {
	var sql string
	switch vender_api.OwnerType(clientType) {
	case vender_api.OwnerType_webUser:
		sql = `UPDATE web_user SET balance = balance - ?1 WHERE userid = ?0;`
	case vender_api.OwnerType_telegramUser:
		sql = `UPDATE tg_user SET balance = balance - ?1 WHERE userid = ?0;`
	default:
		g.Log.Errorf("unknown user type:%d", clientType)
		return
	}
	if _, err := g.DB.Exec(sql, clientId, change); err != nil {
		g.Log.Errorf("writeOrderToDb: %s userId=%d userType=%+v price=%d err=%v", vender_api.OwnerType(clientType).String(), clientId, clientType, change, err)
	}
	g.Log.Infof("update balance %s userId=%d price=%d", vender_api.OwnerType(clientType).String(), clientId, change)
}

func (g *Global) ClientChangeDefaultRobot(clientId int64, vmid int) error {
	return nil
}

func (g *Global) CreateWebAuthToken(userId int64, userType int) (string, error) {
	b := make([]byte, 16)
	rand.Read(b)
	token := hex.EncodeToString(b)
	_, err := g.DB.Exec(
		`INSERT INTO web_auth_tokens (token, userid, user_type) VALUES (?0, ?1, ?2)`,
		token, userId, userType)
	return token, err
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

func (g *Global) GetDrinkName(vmid int32, code string) string {
	var name string
	_, err := g.DB.QueryOne(pg.Scan(&name),
		`SELECT name FROM catalog WHERE vmid = ?0 AND code = ?1`, vmid, code)
	if err != nil || name == "" {
		return code
	}
	return name
}

func (g *Global) Sha256sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
