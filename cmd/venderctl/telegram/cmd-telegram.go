package telegramm

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
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
	Ban     bool
	Credit  int32
	Balance int64
	id      int64
	rcook   cookSruct
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
	return tb.telegramLoop()

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
	return tb.telegramLoop()

}

func (tb *tgbotapiot) telegramLoop() error {
	mqttch := tb.g.Tele.Chan()
	stopch := tb.g.Alive.StopChan()
	tb.updateConfig = tgbotapi.NewUpdate(10)
	tb.updateConfig.Timeout = 60

	tgch := tb.bot.GetUpdatesChan(tb.updateConfig)

	for {
		select {
		case p := <-mqttch:
			tb.g.Alive.Add(1)
			err := tb.onMqtt(p)
			tb.g.Alive.Done()
			if err != nil {
				tb.g.Log.Error(errors.ErrorStack(err))
			}

		case tgm := <-tgch:
			if tgm.Message == nil && tgm.EditedMessage != nil {
				tb.g.Log.Infof("telegramm message change (%v)", tgm.EditedMessage)
				tb.logTgDbChange(*tgm.EditedMessage)
				break
			}
			if tgm.Message.From.IsBot {
				break
			}

			if int(time.Now().Unix())-tgm.Message.Date > 10 {
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

func balance(b int64) string {
	return fmt.Sprintf("баланс: %.2f ", float64(b)/100)
}

func (tb *tgbotapiot) onTeleBot(m tgbotapi.Update) error {
	cl, err := tb.getClient(m.Message.From.ID)
	if err != nil {
		return tb.registerNewUser(m)
	}
	//parse command
	switch parseCommad(m.Message.Text) {
	case tgCommandInvalid:
		return nil
	case tgCommandBalance:
		msg := balance(cl.Balance)
		if cl.Credit != 0 {
			msg = msg + fmt.Sprintf("кредит: %d \nдоступно: %.2f", cl.Credit, float64(cl.Balance+int64(cl.Credit)*100)/100)
		}
		tb.tgSend(cl.id, msg)
	case tgCommandCook:
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
		parts := regexp.MustCompile(`^(bablo|credit)(\d+)$`).FindStringSubmatch(m.Message.Text)
		if len(parts) != 0 {
			tb.forvardMsg = parts
			break
		}
		if m.Message.ForwardFrom != nil && len(tb.forvardMsg) != 0 {
			client, err := tb.getClient(m.Message.ForwardFrom.ID)
			if err != nil {
				TgSendError(fmt.Sprintf("error get client for put bablo (%v)", err))
			}
			switch {
			case tb.forvardMsg[1] == "bablo":
				client.rcook.code = "bablo"
				bablo, _ := strconv.Atoi(tb.forvardMsg[2])
				tb.tgSend(client.id, fmt.Sprintf("пополнение баланса на: %d", bablo))
				tb.tgSend(tb.admin, fmt.Sprintf("баланс: %d пополнен на: %d", client.id, bablo))
				tb.rcookWriteDb(client, -bablo*100, vender_api.PaymentMethod_Balance)
				fmt.Printf("i=%d, type: %T\n", bablo, bablo)
			case tb.forvardMsg[1] == "credit":
				credit, _ := strconv.Atoi(tb.forvardMsg[2])
				tb.setCredit(client.id, credit)
				tb.tgSend(client.id, fmt.Sprintf("появилась возможность делать отрицательный баланс на : %d", credit))
				tb.tgSend(tb.admin, fmt.Sprintf("установлен кредит на: %d для: %d", credit, client.id))
			default:
				break
			}
			tb.forvardMsg = nil
		}
	}

	return nil
}

func parseCommad(cmd string) tgCommand {
	// https://extendsclass.com/regex-tester.html#js
	rm := `((/\d+_m.+)|` +
		`(/balance)|` +
		`(/help)|` +
		`(.+)|` +
		`)$`
	cmdR := regexp.MustCompile(rm)
	parts := cmdR.FindStringSubmatch(cmd)
	if len(parts) == 0 {
		return tgCommandInvalid
	}

	switch {
	case parts[2] != "":
		return tgCommandCook
	case parts[3] != "":
		return tgCommandBalance
	case parts[4] != "":
		return tgCommandHelp
	case parts[5] != "":
		return tgCommandOther
	default:
		return tgCommandInvalid
	}
}

func (tb *tgbotapiot) commandCook(m tgbotapi.Message, client tgUser) {
	// cook commands
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
	if err := tb.sendCookCmd(client.id); err != nil {
		tb.g.Log.Errorf("send cook error (%v", err)
	}
}

func parseCookCommand(cmd string) (cs cookSruct, resultFunction bool) {
	// команда /88_m3_c4_s4
	// приготовить робот:88 код:3 cream:4 sugar:4 (сливики/сахар необязательные)
	// 1 - robo, 2 - code , 3 - valid creame, 4 - value creme, 5 - valid sugar, 6 value sugar
	// var cs cookSruct
	reCmdMake := regexp.MustCompile(`^/(-?\d+)_m(.+)(_c([0-6]))?(_s([0-8]))?$`)
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
	if !tb.g.Vmc[vmid].Connect {
		tb.tgSend(user, "автомат не в сети.")
		return false
	}
	if tb.g.Vmc[vmid].State == vender_api.State_Invalid {
		cmd := &vender_api.Command{
			Task: &vender_api.Command_GetState{},
		}
		_ = tb.g.Tele.SendCommand(vmid, cmd)
	}
	if tb.g.Vmc[vmid].State != vender_api.State_Nominal {
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

func (tb *tgbotapiot) sendCookCmd(chatId int64) error {
	cl := tb.chatId[chatId]

	Cook := &vender_api.Command_ArgCook{
		Menucode:      cl.rcook.code,
		Balance:       int32(cl.Balance) + int32(cl.Credit),
		PaymentMethod: vender_api.PaymentMethod_Balance,
	}
	if cl.rcook.cream != 0 {
		Cook.Cream = []byte{cl.rcook.cream}
	}
	if cl.rcook.sugar != 0 {
		Cook.Sugar = []byte{cl.rcook.sugar}
	}

	cmd := &vender_api.Command{
		Executer: chatId,
		Lock:     false,
		Task: &vender_api.Command_Cook{
			Cook: Cook,
		},
	}
	tb.g.Log.Infof("client (%v) send remote cook code:%s", cl, cl.rcook.code)
	return tb.g.Tele.SendCommand(cl.rcook.vmid, cmd)
}

func (tb *tgbotapiot) onMqtt(p tele_api.Packet) error {
	vmcid := p.VmId
	r := tb.g.Vmc[vmcid]
	switch p.Kind {
	case tele_api.PacketConnect:
		c := false
		if p.Payload[0] == 1 {
			c = true
		}
		r.Connect = c
		tb.g.Vmc[vmcid] = r
	case tele_api.PacketState:
		s, err := p.State()
		if err != nil {
			return err
		}
		r.State = s
		tb.g.Vmc[vmcid] = r
	case tele_api.PacketCommandReply:
		rm, _ := p.CommandResponse()
		if rm.CookReplay > 0 {
			if !tb.cookResponse(rm) {
				break
			}
		}
		return nil

	default:
		// TgSendError(fmt.Sprintf("code error invalid packet=%s", p.Kind.String()))
		return nil
	}
	return nil
}
func (tb *tgbotapiot) cookResponse(rm *vender_api.Response) bool {
	var msg string
	switch rm.CookReplay {
	case vender_api.CookReplay_vmcbusy:
		msg = "автомат в данный момент обрабатывает другой заказ. попробуйте позднее."
	case vender_api.CookReplay_cookStart:
		msg = "начинаю готовить"
		tb.tgSend(int64(rm.Executer), msg)
		return false
	case vender_api.CookReplay_cookFinish:
		msg = "заказ выполнен. приятного аппетита."
		user := tb.chatId[rm.Executer]
		tb.rcookWriteDb(user, int(rm.ValidateReplay), vender_api.PaymentMethod_Balance)
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
	return true
}

func (tb *tgbotapiot) rcookWriteDb(user tgUser, price int, payMethod vender_api.PaymentMethod) {
	tb.g.Log.Infof("cooking finished client:%d code:%s", user.id, tb.chatId[user.id].rcook.code)
	nb := user.Balance - int64(price)
	const q = `insert into trans (vmid,received,menu_code,options,price,method,executer) values (?0,current_timestamp,?1,?2,?3,?4,?5);
	UPDATE tg_user set balance = ?6 WHERE userid = ?5;`
	tb.g.Alive.Add(1)
	_, err := tb.g.DB.Exec(q, user.rcook.vmid, user.rcook.code, pg.Array([2]uint8{user.rcook.cream, user.rcook.sugar}), price, payMethod, user.id, nb)
	tb.g.Alive.Done()
	if err != nil {
		tb.g.Log.Errorf("db query=%s chatid=%v err=%v", q, user.id, err)
	}
	msg := balance(nb)
	tb.tgSend(user.id, msg)
}

func TgSendError(s string) {
	if tb.admin > 0 {
		tb.tgSend(int64(tb.admin), s)
		tb.g.Log.Error(s)
	}
}

func (tb *tgbotapiot) tgSend(chatid int64, s string) {
	msg := tgbotapi.NewMessage(chatid, s)
	m, err := tb.bot.Send(msg)
	if err != nil {
		tb.g.Log.Errorf("error send telegramm message (%v)", err)
		return
	}
	tb.logTgDb(m)
}

func (tb *tgbotapiot) getClient(c int64) (tgUser, error) {
	tb.g.Alive.Add(1)
	db := tb.g.DB.Conn()
	var cl tgUser
	_, err := db.QueryOne(&cl, `SELECT Ban, Balance, Credit FROM tg_user WHERE userid = ?;`, c)
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
			Text:           "регистрация в системе",
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

func (tb *tgbotapiot) replayCommandHelp(cl int64) error {
	msg := "пока это работает так:\n" +
		"для заказа напитка нужно указать номер автомата (номер указан в право верхнем углу автомата), код напитка и, если требуется, тюнинг сливок и сахара\n" +
		"пример: хочется заказать атомату 5 напиток с кодом 23. для этого боту надо написать (используя латиницу)\n" +
		"/5_m23\n" +
		"для тюнинга сливок и сахара надо добавить _с (это cream = сливки) и/или _s (это sugar = сахар ) например:\n" +
		"/5_m23_c3_s2\n" +
		"это означает, автомату=5, приготовить код=23, сливки=3, сахар=2\n" +
		"если непонятно, позвоните/напишите @Alexey_Milko, он расскажет.\n" +
		"\n" +
		"PS позднее будет сделан более удобный механизм заказа"
	tb.tgSend(cl, msg)
	return nil
}
