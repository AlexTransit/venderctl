package telegramm

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	vender_api "github.com/AlexTransit/vender/tele"
	"github.com/AlexTransit/venderctl/cmd/internal/cli"
	"github.com/AlexTransit/venderctl/internal/state"
	tele_api "github.com/AlexTransit/venderctl/internal/tele/api"
	"github.com/coreos/go-systemd/daemon"
	"github.com/go-pg/pg/v9"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/juju/errors"
)

const CmdName = "telegram"

var Cmd = cli.Cmd{
	Name:   CmdName,
	Desc:   "telegram bot. control vmc via telegram bot",
	Action: telegramMain,
}

// var tg *tgbotapi.BotAPI
var tb = new(tgbotapiot)

type tgCommand uint8

const (
	tgCommandInvalid tgCommand = iota
	tgCommandCook
	tgCommandBalance
	tgCommandHelp
	tgCommandBabloToUser
	tgCommandOther
	// tgCommandTest
)

type tgbotapiot struct {
	bot          *tgbotapi.BotAPI
	updateConfig tgbotapi.UpdateConfig
	admin        int64
	g            *state.Global
	chatId       map[int64]tgUser
	forvardMsg   []string
}

type tgUser struct {
	tOut          bool   // cook timeout. true after 200 seconds
	Ban           bool   //banned client
	Credit        uint32 //client credit
	Balance       int64  // client ballanse. possible minus
	MoneyAvalible int64  // balanse + credit
	Diskont       int
	id            int64 //telegramm id
	rcook         cookSruct
}

type cookSruct struct {
	code  string
	sugar uint8
	cream uint8
	vmid  int32
}

func telegramMain(ctx context.Context, flags *flag.FlagSet) error {

	g := state.GetGlobal(ctx)
	g.InitVMC()
	configPath := flags.Lookup("config").Value.String()
	g.Config = state.MustReadConfig(g.Log, state.NewOsFullReader(), configPath)
	g.Config.Tele.SetMode("telegram")
	// g.Vmc = make(map[int32]bool)
	// g.Vmc[1] = true

	if err := telegramInit(ctx); err != nil {
		return errors.Annotate(err, "telegramInit")
	}
	return tb.telegramLoop(ctx)

}

func telegramInit(ctx context.Context) error {
	var err error
	tb.g = state.GetGlobal(ctx)
	if err = tb.g.InitDB(CmdName); err != nil {
		return errors.Annotate(err, "telegramm_db_init")
	}

	if err = tb.g.Tele.Init(ctx, tb.g.Log, tb.g.Config.Tele); err != nil {
		return errors.Annotate(err, "MQTT.Init")
	}

	if tb.bot, err = tgbotapi.NewBotAPI(tb.g.Config.Telegram.TelegrammBotApi); err != nil {
		log.Fatalf("Bot connect fail :%s ", err)
		os.Exit(1)
	}

	tb.bot.Debug = tb.g.Config.Telegram.DebugMessages
	tb.chatId = make(map[int64]tgUser)
	tb.admin = tb.g.Config.Telegram.TelegramAdmin

	// log.Printf("Authorized on account '%s'", tb.bot.Self.UserName)

	cli.SdNotify(daemon.SdNotifyReady)
	tb.g.Log.Infof("telegram init complete")
	return tb.telegramLoop(ctx)

}

func (tb *tgbotapiot) telegramLoop(ctx context.Context) error {
	// g := state.GetGlobal(ctx)
	mqttch := tb.g.Tele.Chan()
	stopch := tb.g.Alive.StopChan()
	tb.updateConfig = tgbotapi.NewUpdate(10)
	tb.updateConfig.Timeout = 60

	tgch := tb.bot.GetUpdatesChan(tb.updateConfig)

	for {
		select {
		case p := <-mqttch:
			// старый и новый обработчик
			rm := tb.g.ParseFromRobo(p)
			if p.Kind == tele_api.FromRobo {
				if rm.Order != nil {
					if rm.Order.OwnerType == vender_api.OwnerType_telegramUser {
						tb.g.Log.Infof("order telegramm message from robot (%v)", rm)
						tb.g.Alive.Add(1)
						tb.cookResponseN(rm.Order)
						tb.g.Alive.Done()
					}
				}
			}
			tb.g.Alive.Add(1)
			pp := tb.g.ParseMqttPacket(p)
			if rm != nil && rm.Err != nil {
				errm := fmt.Sprintf("%v: %v", p.VmId, rm.Err.Message)
				tb.tgSend(tb.admin, errm)
			}
			tb.cookResponse(pp)
			tb.g.Alive.Done()
		case tgm := <-tgch:
			if tgm.Message == nil && tgm.EditedMessage != nil {
				tb.g.Log.Infof("telegramm message change (%v)", tgm.EditedMessage)
				tb.logTgDbChange(*tgm.EditedMessage)
				break
			}
			if tgm.ChannelPost != nil {
				tb.TgChannelParser(tgm.ChannelPost)
				break
			}
			if tgm.Message == nil {
				break
			}
			notBot := !tgm.Message.From.IsBot
			if !notBot {
				tb.g.Log.Infof("ignore telegramm message from bot (%v)", tgm.Message)
				break
			}
			if int(time.Now().Unix())-tgm.Message.Date > 60 {
				tb.tgSend(tgm.Message.From.ID, "была проблема со связью.\nкоманда поступила c опозданием.\nесли актуально повторите еще раз.")
				break
			}
			tb.g.Log.Debugf("income telegramm messgage from (%d) message (%s)", tgm.Message.From.ID, tgm.Message.Text)
			tb.g.Alive.Add(1)
			err := tb.onTeleBot(tgm)
			tb.g.Alive.Done()
			if err != nil {
				tb.g.Log.Error(errors.ErrorStack(err))
			}

		case <-stopch:
			return nil
		}
	}
}

func (tb *tgbotapiot) TgChannelParser(m *tgbotapi.Message) {
	tb.g.Log.Infof("channel parser autor(%s) message(%v)", m.AuthorSignature, m.Text)
	if m.AuthorSignature == tb.g.Config.Telegram.AdminBot {
		var responseMessage string
		defer func() {
			tb.tgSend(tb.admin, responseMessage)

		}()
		parts := strings.FieldsFunc(m.Text, func(r rune) bool { return r == '_' })
		clid, err := strconv.ParseInt(parts[0], 10, 64)

		if err != nil {
			responseMessage = fmt.Sprintf("error bot(%v) command (%s)", m.AuthorSignature, m.Text)
			return
		}
		bablo, err := strconv.Atoi(parts[1])
		if err != nil {
			responseMessage = fmt.Sprintf("error bot(%v) bablo (%s)", m.AuthorSignature, m.Text)
			return
		}
		tb.addCredit(clid, bablo)
		responseMessage = fmt.Sprintf("bot add money user(%v) bablo (%v)", clid, bablo)
		tb.g.Log.Infof("chatId:%v autor:%v message:%v", m.Chat.ID, m.AuthorSignature, m.Text)
	}
}

func balance(b int64) string {
	// return fmt.Sprintf("баланс: %.2f ", float64(b)/100)
	return "баланс: " + amoutToString(b)
}
func amoutToString(i int64) string {
	return fmt.Sprintf("%.2f ", float64(i)/100)
}

func (tb *tgbotapiot) onTeleBot(m tgbotapi.Update) error {
	cl, err := tb.getClient(m.Message.From.ID)
	if err != nil {
		return tb.registerNewUser(m)
	}
	//parse command
	switch parseCommand(m.Message.Text) {
	case tgCommandInvalid:
		return nil
	case tgCommandBalance:
		msg := balance(cl.Balance)
		if cl.Credit != 0 {
			msg = msg + fmt.Sprintf("кредит: %d \nдоступно: %.2f", cl.Credit, float64(cl.Balance+int64(cl.Credit)*100)/100)
		}
		tb.tgSend(cl.id, msg)
	case tgCommandCook:
		if tb.chatId[cl.id].tOut {
			delete(tb.chatId, cl.id)
		}
		if tb.chatId[cl.id].id != 0 {
			return nil
		}
		tb.commandCook(*m.Message, cl)

	case tgCommandHelp:
		return tb.replayCommandHelp(cl.id)
	case tgCommandOther:
		if m.Message.From.ID != tb.admin {
			msg := fmt.Sprintf("команда (%s) \nнепонятно что делать.", m.Message.Text)
			tb.tgSend(cl.id, msg)
			break
		}
		// обрабатываем команды админа
		parts := regexp.MustCompile(`^((bablo|credit)(-?\d+)|(\d+)c(..*))$`).FindStringSubmatch(m.Message.Text)
		if parts != nil {
			switch {
			case parts[2] != "":
				tb.forvardMsg = parts
				return nil // wait next message. forward include client id
			case parts[4] != "":
				tb.sendExec(parts[4], parts[5])
				return nil
			default:
				return nil
			}
		}
		if len(tb.forvardMsg) == 0 {
			p := regexp.MustCompile(`(\d+)`).FindStringSubmatch(m.Message.Text)
			if len(p) == 2 {
				tb.forvardMsg = []string{"", "", "bablo", p[1]}
				// tb.forvardMsg[1] = "bablo"
				// tb.forvardMsg[2] = p[1]
			}
		}
		if len(tb.forvardMsg) != 0 {
			defer clearForwardMessage()
			if m.Message.ForwardFrom == nil {
				tb.tgSend(tb.admin, "client ID не доступен")
				tb.forvardMsg[1] = `-` + tb.forvardMsg[1]
				return nil
			}
			client, err := tb.getClient(m.Message.ForwardFrom.ID)
			if err != nil {
				TgSendError(fmt.Sprintf("error get client for put bablo (%v)", err))
			}
			switch {
			case tb.forvardMsg[2] == "bablo":
				client.rcook.code = "bablo"
				bablo, _ := strconv.Atoi(tb.forvardMsg[3])
				tb.addCredit(client.id, bablo)
			case tb.forvardMsg[2] == "credit":
				credit, _ := strconv.Atoi(tb.forvardMsg[3])
				tb.setCredit(client.id, credit)
				tb.tgSend(client.id, fmt.Sprintf("появилась возможность делать отрицательный баланс на : %d", credit))
				tb.tgSend(tb.admin, fmt.Sprintf("установлен кредит на: %d для: %d", credit, client.id))
			default:
				tb.tgSend(tb.admin, fmt.Sprintf("не пон: %v", tb.forvardMsg))
			}
		}
	}

	return nil
}

func (tb *tgbotapiot) addCredit(clientId int64, bablo int) {
	msgToUser := fmt.Sprintf("пополнение баланса на: %d\n", bablo)
	tb.tgSend(tb.admin, fmt.Sprintf("баланс: %d пополнен на: %d", clientId, bablo))
	tb.rcookWriteDb(clientId, -bablo*100, vender_api.PaymentMethod_Balance, msgToUser)
	fmt.Printf("i=%d, type: %T\n", bablo, bablo)
}

func clearForwardMessage() {
	tb.forvardMsg = nil
}

func parseCommand(cmd string) tgCommand {
	// https://extendsclass.com/regex-tester.html#js
	// https://regexr.com/
	// rm := `^((/-?\d+_m?[-.0-9]+(.+?)?)|(/balance)|(/help)|(.+)|)$`
	cmdR := regexp.MustCompile(`^((/-?\d+_m?[-.0-9]+(.+?)?)|(/balance)|(/help)|(.+)|)$`)
	parts := cmdR.FindStringSubmatch(cmd)
	if len(parts) == 0 {
		return tgCommandInvalid
	}

	switch {
	case parts[2] != "":
		return tgCommandCook
	case parts[4] != "":
		return tgCommandBalance
	case parts[5] != "":
		return tgCommandHelp
	case parts[6] != "":
		return tgCommandOther
	default:
		return tgCommandInvalid
	}
}

func (tb *tgbotapiot) sendExec(id string, command string) {
	var vmid int32
	fmt.Sscan(id, &vmid)
	cmd := &vender_api.Command{
		Task: &vender_api.Command_Exec{Exec: &vender_api.Command_ArgExec{Scenario: command}},
	}
	tb.g.Tele.CommandTx(vmid, cmd)
}

func (tb *tgbotapiot) commandCook(m tgbotapi.Message, client tgUser) {
	// cook commands
	if (int32(client.Balance) + int32(client.Credit*100)) <= 0 {
		tb.tgSend(client.id, "недостаточно средств")
		return
	}
	var ok bool
	if client.rcook, ok = parseCookCommand(m.Text); !ok {
		tb.tgSend(client.id, "команда приготовления написана с ошибкой.\n почитайте /help и сделайте понятную для меня команду.")
		return
	}
	tb.logTgDb(m)
	if !tb.checkRobo(client.rcook.vmid, client.id) {
		return
	}
	tb.chatId[client.id] = client
	tb.sendCookCmdN(client.id)
	go tb.RunCookTimer(client.id)
}

func parseCookCommand(cmd string) (cs cookSruct, resultFunction bool) {
	// команда /88_m3_c4_s4
	// приготовить робот:88 код:3 cream:4 sugar:4 (сливики/сахар необязательные)
	// 1 - robo, 2 - code , 3 - valid creame, 4 - value creme, 5 - valid sugar, 6 value sugar
	// var cs cookSruct
	reCmdMake := regexp.MustCompile(`^/(-?\d+)_m?([-.0-9]+)(_?[c,C,с,С]([0-6]))?(_?[s,S]([0-8]))?$`)
	parts := reCmdMake.FindStringSubmatch(cmd)
	if len(parts) == 0 {
		return cs, false
	}
	fmt.Sscan(parts[1], &cs.vmid)
	cs.code = parts[2]
	if parts[4] != "" {
		fmt.Sscan(parts[4], &cs.cream)
		cs.cream++
	}
	if parts[6] != "" {
		fmt.Sscan(parts[6], &cs.sugar)
		cs.sugar++
	}
	return cs, true
}

func (tb *tgbotapiot) checkRobo(vmid int32, user int64) bool {
	if !tb.g.RobotConnected(vmid) {
		tb.tgSend(user, "автомат не в сети.\nили отключено электричество, или недоступен интернет.")
		return false
	}
	if tb.g.Vmc[vmid].State == vender_api.State_Invalid {
		tb.g.Tele.SendToRobo(vmid, &vender_api.ToRoboMessage{
			Cmd: vender_api.MessageType_reportState,
		})
	}
	if tb.g.Vmc[vmid].State != vender_api.State_Nominal && tb.g.Vmc[vmid].State != vender_api.State_WaitingForExternalPayment {
		errm := "автомат сейчас не может выполнить заказ."
		tb.tgSend(user, errm)
		return false
	}
	return true
}

func (tb *tgbotapiot) logTgDbChange(m tgbotapi.Message) {
	const q = `UPDATE tgchat set (changedate, changetext) = (?0,?1) WHERE messageid=?2;`
	tb.g.Alive.Add(1)
	_, err := tb.g.DB.Exec(q,
		m.EditDate,
		m.Text,
		m.MessageID,
	)
	tb.g.Alive.Done()
	if err != nil {
		tb.g.Log.Errorf("db query=%s err=%v", q, err)
	}
}

func (tb *tgbotapiot) setCredit(id int64, credit int) {
	const q = `UPDATE tg_user SET credit = ?1 WHERE userid = ?0;`
	tb.g.Alive.Add(1)
	_, err := tb.g.DB.Exec(q, id, credit)
	tb.g.Alive.Done()
	if err != nil {
		tb.g.Log.Errorf("db query=%s err=%v", q, err)
	}
}

func (tb *tgbotapiot) logTgDb(m tgbotapi.Message) {
	const q = `insert into tg_chat (messageid, fromid, toid, date, text) values (?0, ?1, ?2, ?3, ?4);`
	tb.g.Alive.Add(1)
	_, err := tb.g.DB.Exec(q, m.MessageID, m.From.ID, m.Chat.ID, m.Date, m.Text)
	if err != nil {
		tb.g.Log.Errorf("db query=%s err=%v", q, err)
	}
	tb.g.Alive.Done()
}

func (tb *tgbotapiot) sendCookCmdN(chatId int64) {
	cl := tb.chatId[chatId]
	trm := vender_api.ToRoboMessage{
		ServerTime: time.Now().Unix(),
		Cmd:        vender_api.MessageType_makeOrder,
		MakeOrder: &vender_api.Order{
			MenuCode:      cl.rcook.code,
			Amount:        uint32(cl.MoneyAvalible),
			OrderStatus:   vender_api.OrderStatus_doTransferred,
			PaymentMethod: vender_api.PaymentMethod_Balance,
			OwnerInt:      chatId,
			OwnerType:     vender_api.OwnerType_telegramUser,
		},
	}
	if cl.rcook.cream != 0 {
		trm.MakeOrder.Cream = []byte{cl.rcook.cream}
	}
	if cl.rcook.sugar != 0 {
		trm.MakeOrder.Sugar = []byte{cl.rcook.sugar}
	}
	tb.g.Log.Infof("telegram client (%v) send remote cook code:%s", cl, cl.rcook.code)
	tb.g.Tele.SendToRobo(cl.rcook.vmid, &trm)
}
func (tb *tgbotapiot) cookResponseN(ro *vender_api.Order) {
	var msg string
	client := ro.OwnerInt
	switch ro.OrderStatus {
	case vender_api.OrderStatus_robotIsBusy:
		msg = "автомат в данный момент обрабатывает другой заказ. попробуйте позднее."
	case vender_api.OrderStatus_executionInaccessible:
		msg = "код заказа недоступен.\nнеправильный код или мало ингредиентов на выбранный заказ.\nукажите другой код."
	case vender_api.OrderStatus_overdraft:
		msg = "недостаточно средств. пополните баланс и попробуйте снова."
	case vender_api.OrderStatus_executionStart:
		msg = fmt.Sprintf("начинаю готовить. \nкод: %s автомат: %d", tb.chatId[client].rcook.code, tb.chatId[client].rcook.vmid)
		tb.tgSend(int64(client), msg)
		return
	case vender_api.OrderStatus_orderError:
		msg = "ошибка приготовления."
	case vender_api.OrderStatus_complete:
		finMsg := fmt.Sprintf("автомат : %d приготовил код: %s цена: %s\n",
			tb.chatId[client].rcook.vmid,
			ro.MenuCode,
			amoutToString(int64(ro.Amount)))
		tb.rcookWriteDb(tb.chatId[client].id, int(ro.Amount), vender_api.PaymentMethod_Balance, finMsg)
		if dis := tb.chatId[client].Diskont; dis != 0 {
			go func() {
				bonus := (int(ro.Amount) * dis) / 100
				time.Sleep(10 * time.Second)
				cl, _ := tb.getClient(client)
				cl.rcook.code = "bonus"
				bunusMgs := fmt.Sprintf("начислен бонус: %s\n", amoutToString(int64(bonus)))
				tb.rcookWriteDb(cl.id, -bonus, vender_api.PaymentMethod_Balance, bunusMgs)
			}()
		}
	default:
		msg = "что то пошло не так. без паники. хозяину уже в сообщили."
		TgSendError(fmt.Sprintf("vmid=%d code error invalid order=%s", tb.chatId[client].rcook.vmid, ro.String()))
	}
	tb.tgSend(client, msg)
	delete(tb.chatId, client)
}
func (tb *tgbotapiot) cookResponse(rm *vender_api.Response) {
	if rm == nil || rm.Executer == 0 {
		return
	}
	var msg string
	client := rm.Executer
	switch rm.CookReplay {
	case vender_api.CookReplay_vmcbusy:
		msg = "автомат в данный момент обрабатывает другой заказ. попробуйте позднее."
	case vender_api.CookReplay_cookStart:
		msg = fmt.Sprintf("начинаю готовить. \nкод: %s автомат: %d", tb.chatId[client].rcook.code, tb.chatId[client].rcook.vmid)
		tb.tgSend(int64(rm.Executer), msg)
		return
	case vender_api.CookReplay_cookFinish:
		user := tb.chatId[rm.Executer]
		price := int(rm.ValidateReplay)
		codeOrder := rm.Data
		if codeOrder == "" {
			codeOrder = user.rcook.code
		}
		msg = fmt.Sprintf("автомат : %d приготовил код: %s цена: %s", user.rcook.vmid, codeOrder, amoutToString(int64(price)))
		tb.tgSend(user.id, msg)
		msg = "приятного аппетита."
		if tb.chatId[rm.Executer].Diskont != 0 {
			go func() {
				bonus := (price * tb.chatId[rm.Executer].Diskont) / 100
				p := int64(price)
				time.Sleep(10 * time.Second)
				cl, _ := tb.getClient(user.id)
				user.Balance = user.Balance - p
				tb.tgSend(user.id, fmt.Sprintf("начислен бонус: %s", amoutToString(int64(bonus))))
				cl.rcook.code = "bonus"
				tb.rcookWriteDb(cl.id, -bonus, vender_api.PaymentMethod_Balance)
			}()
		}
		tb.rcookWriteDb(user.id, price, vender_api.PaymentMethod_Balance)
	case vender_api.CookReplay_cookInaccessible:
		msg = "код недоступен"
	case vender_api.CookReplay_cookOverdraft:
		msg = "недостаточно средств. пополните баланс и попробуйте снова."
	case vender_api.CookReplay_cookError:
		msg = "ошибка приготовления."
	default:
		msg = "что то пошло не так. без паники. хозяину уже в сообщили."
		TgSendError(fmt.Sprintf("vmid=%d code error invalid packet=%s", tb.chatId[rm.Executer].rcook.vmid, rm.String()))
	}
	tb.tgSend(int64(rm.Executer), msg)
	delete(tb.chatId, rm.Executer)
}

func (tb *tgbotapiot) rcookWriteDb(userId int64, price int, payMethod vender_api.PaymentMethod, addMsg ...string) {
	tb.g.Log.Infof("cooking finished telegram client:%d code:%s", userId, tb.chatId[userId].rcook.code)
	cl, _ := tb.getClient(userId)
	cl.Balance -= int64(price)
	const q = `UPDATE tg_user set balance = ?1 WHERE userid = ?0;`
	tb.g.Alive.Add(1)
	// _, err := tb.g.DB.Exec(q, user.rcook.vmid, user.rcook.code, pg.Array([2]uint8{user.rcook.cream, user.rcook.sugar}), price, payMethod, user.id, int64(price))
	_, err := tb.g.DB.Exec(q, userId, cl.Balance)
	tb.g.Alive.Done()
	if err != nil {
		tb.g.Log.Errorf("db query=%s chatid=%v err=%v", q, userId, err)
	}
	var msg string
	if len(addMsg) != 0 {
		msg = addMsg[0]
	}
	msg = msg + balance(cl.Balance)
	tb.tgSend(userId, msg)
}

func TgSendError(s string) {
	if tb.admin > 0 {
		tb.tgSend(int64(tb.admin), s)
		tb.g.Log.Error(s)
	}
}

func (tb *tgbotapiot) tgSend(chatid int64, s string) {
	if s == "" {
		return
	}
	msg := tgbotapi.NewMessage(chatid, s)
	m, err := tb.bot.Send(msg)
	if err != nil {
		tb.g.Log.Errorf("error send telegramm message (%v)", err)
		return
	}
	tb.g.Log.Infof("send telegram message userid: %d text: %s", m.Chat.ID, m.Text)
	tb.logTgDb(m)
}

func (tb *tgbotapiot) getClient(c int64) (tgUser, error) {
	tb.g.Alive.Add(1)
	db := tb.g.DB.Conn()
	var cl tgUser
	_, err := db.QueryOne(&cl, `SELECT Ban, Balance, Credit, Diskont FROM tg_user WHERE userid = ?;`, c)
	_ = db.Close()
	tb.g.Alive.Done()
	if err == pg.ErrNoRows {
		return cl, errors.Annotate(err, "client not found in db")
	} else if err != nil {
		return cl, errors.Annotate(err, "telegram client db read error ")
	}
	if cl.Ban {
		tb.g.Log.Infof("message from banned user id:%d", c)
		return cl, errors.New("user banned")
	}
	cl.id = c
	cl.MoneyAvalible = cl.Balance + int64(cl.Credit*100)
	return cl, nil
}

func (tb *tgbotapiot) addUserToDB(c *tgbotapi.Contact, dt int) error {
	const q = `INSERT INTO tg_user ( userId, firstName, lastName, phoneNumber, registerDate ) values (?0, ?1, ?2, ?3, ?4);`
	_, err := tb.g.DB.Exec(q, c.UserID, c.FirstName, c.LastName, c.PhoneNumber, dt)
	return err
}

func (tb *tgbotapiot) registerNewUser(m tgbotapi.Update) error {
	const regMess = "Для работы с роботом, нужно зарегестрироваться в системе."
	var msg tgbotapi.MessageConfig
	var err error

	if m.Message.Text == "/start" {
		msg = tgbotapi.NewMessage(m.Message.Chat.ID, regMess)
		btn := tgbotapi.KeyboardButton{
			Text:           "запрос номера телефона",
			RequestContact: true,
		}
		msg.ReplyMarkup = tgbotapi.NewReplyKeyboard([]tgbotapi.KeyboardButton{btn})
		_, err = tb.bot.Send(msg)
		return err
	}
	// if m.Message.Contact == nil || m.Message.Contact.UserID != m.Message.From.ID || m.Message.ReplyToMessage.Text != regMess {
	if m.Message.Contact == nil || m.Message.Contact.UserID != m.Message.From.ID {
		return nil
	}
	if err = tb.addUserToDB(m.Message.Contact, m.Message.Date); err != nil {
		return err
	}
	complitMessage := "регистрация завершена. \n/help - получить справку."
	msg = tgbotapi.NewMessage(m.Message.Chat.ID, complitMessage)
	msg.ReplyMarkup = tgbotapi.NewRemoveKeyboard(true)
	_, _ = tb.bot.Send(msg)
	return nil
}

func (tb *tgbotapiot) RunCookTimer(cl int64) {
	time.Sleep(200 * time.Second)
	c := tb.chatId[cl]
	if c.id == cl {
		c.tOut = true
		tb.chatId[cl] = c
	}
}

func (tb *tgbotapiot) replayCommandHelp(cl int64) error {
	msg := "пока это работает так:\n" +
		"для заказа напитка нужно указать номер автомата (номер указан в право верхнем углу автомата), код напитка и, если требуется, тюнинг сливок и сахара\n" +
		"пример: хочется заказать атомату 5 напиток с кодом 23. для этого боту надо написать\n" +
		"/5_23\n" +
		"для тюнинга сливок и сахара надо добавить _с (это cream = сливки) и/или _s (это sugar = сахар ) например:\n" +
		"/5_23_c3_s2\n" +
		"или теперь можно так\n" +
		"/5_23c3s2\n" +
		"это означает, автомату=5, приготовить код=23, сливки=3, сахар=2\n" +
		"если непонятно, позвоните/напишите @Alexey_Milko, он расскажет.\n" +
		"\n" +
		"PS позднее будет сделан более удобный механизм заказа"
	tb.tgSend(cl, msg)
	return nil
}
