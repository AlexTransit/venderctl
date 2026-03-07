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
	Balance      int64                // client balanse. possible minus <= credit
	Credit       uint32               //client credit
	ClientType   vender_api.OwnerType `pg:"clienttype"`
	Diskont      int                  //persent discont
	Defaultrobot int
	Ban          bool //banned client
}

func (g *Global) ClientGet(clientId int64, clientType vender_api.OwnerType) (cl Client, err error) {
	db := g.DB.Conn()
	defer db.Close()
	_, err = db.QueryOne(&cl,
		`SELECT userid AS id, user_type AS ClientType, ban, name, (balance*100)::bigint AS balance, (credit*100)::bigint AS credit, discount::int AS diskont, defaultrobot FROM users WHERE userid = ?0 AND user_type = ?1;`,
		clientId, clientType)
	if err != nil {
		return cl, err // error
	}
	if cl.Ban {
		return cl, fmt.Errorf("клиент забанен")
	}
	if cl.Defaultrobot == 0 {
		cl.Defaultrobot = g.clientPopularRobot(clientId, clientType)
	}
	return cl, nil
}

func (g *Global) clientPopularRobot(clientId int64, clientType vender_api.OwnerType) int {
	var vmid int
	_, err := g.DB.QueryOne(pg.Scan(&vmid), `SELECT vmid FROM trans	WHERE executer = ?0 AND executer_type = ?1 GROUP BY vmid ORDER BY COUNT(*) DESC, MAX(vmtime) DESC LIMIT 1;`,
		clientId, clientType)
	if err == nil {
		return vmid
	}
	return 0
}
func (g *Global) ClientUpdateBalance(clientId int64, clientType vender_api.OwnerType, change int64) {
	_, err := g.DB.Exec(`UPDATE users SET balance = balance - ?2 WHERE userid = ?0 AND user_type = ?1;`, clientId, clientType, float64(change)/100)
	if err != nil {
		g.Log.Errorf("writeOrderToDb: %s userId=%d userType=%+v price=%v err=%v", vender_api.OwnerType(clientType).String(), clientId, clientType, change, err)
	}
	g.Log.Infof("update balance %s userId=%d price=%v", vender_api.OwnerType(clientType).String(), clientId, change)
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

func (g *Global) LogUserOrder(prefix string, userId int64, userType int32, action string, balanceInfo int64) {
	const q = `INSERT INTO user_orders (userid, user_type, action, balance_info) VALUES (?0, ?1, ?2, ?3)`
	g.Alive.Add(1)
	_, err := g.DB.Exec(q, userId, userType, prefix+" "+action, float64(balanceInfo)/100)
	g.Alive.Done()
	if err != nil {
		g.Log.Errorf("logUserOrder userId=%d userType=%d err=%v", userId, userType, err)
	}
}
