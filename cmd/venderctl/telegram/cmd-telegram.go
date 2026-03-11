package telegramm

import (
	"context"
	"flag"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	vender_api "github.com/AlexTransit/vender/tele"
	"github.com/AlexTransit/venderctl/cmd/internal/cli"
	"github.com/AlexTransit/venderctl/internal/state"
	tele_api "github.com/AlexTransit/venderctl/internal/tele/api"
	"github.com/coreos/go-systemd/daemon"
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
	tgCommandWebInvite
	tgCommandWeb
)

type tgbotapiot struct {
	forvardMsg   []string
	updateConfig tgbotapi.UpdateConfig
	bot          *tgbotapi.BotAPI
	g            *state.Global
	admin        int64
}

type telegramBotLogger struct {
	g *state.Global
}

func (l telegramBotLogger) Println(v ...interface{}) {
	msg := strings.TrimSpace(fmt.Sprintln(v...))
	l.log(msg)
}

func (l telegramBotLogger) Printf(format string, v ...interface{}) {
	msg := strings.TrimSpace(fmt.Sprintf(format, v...))
	l.log(msg)
}

func (l telegramBotLogger) log(msg string) {
	lmsg := strings.ToLower(msg)
	if isTelegramTransientNetError(lmsg) || strings.Contains(lmsg, "failed to get updates") {
		l.g.Log.Debugf("telegram api: %s", msg)
		return
	}
	l.g.Log.Errorf("telegram api: %s", msg)
}

func isTelegramTransientNetError(msg string) bool {
	transientErrors := []string{
		"connection reset by peer",
		"context deadline exceeded",
		"i/o timeout",
		"timeout awaiting response headers",
		"unexpected eof",
		"use of closed network connection",
		"tls handshake timeout",
		"server sent goaway",
	}
	for _, em := range transientErrors {
		if strings.Contains(msg, em) {
			return true
		}
	}
	return false
}

type tgUser struct {
	state.Client
	rcook cookSruct
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
	if err = tgbotapi.SetLogger(telegramBotLogger{g: tb.g}); err != nil {
		return errors.Annotate(err, "SetTelegramLogger")
	}

	// if tb.bot, err = tgbotapi.NewBotAPI(tb.g.Config.Telegram.TelegrammBotApi); err != nil {
	// 	tb.g.Log.Fatalf("Bot connect fail :%s ", err)
	// 	os.Exit(1)
	// }

	for {
		tb.bot, err = tgbotapi.NewBotAPI(tb.g.Config.Telegram.TelegrammBotApi)
		if err == nil {
			break // Успешное подключение, выходим из цикла
		}
		tb.g.Log.Errorf("Bot connect fail: %s. Retrying in 3 seconds...", err)
		time.Sleep(3 * time.Second) // Ждем перед следующей попыткой
	}

	tb.setBotMenu()
	tb.bot.Debug = tb.g.Config.Telegram.DebugMessages
	tb.admin = tb.g.Config.Telegram.TelegramAdmin

	cli.SdNotify(daemon.SdNotifyReady)
	tb.g.Log.Infof("telegram init complete")
	return nil
}

func (tb *tgbotapiot) setBotMenu() {
	configs := []tgbotapi.BotCommand{
		{Command: "balance", Description: "Посмотреть текущий баланс"},
		{Command: "help", Description: "Помощь"},
		{Command: "web", Description: "перейти в браузер"},
		{Command: "invite", Description: "получить одноразовую ссылку приглашение"},
	}

	// Создаем конфиг установки команд
	setCommandsConfig := tgbotapi.NewSetMyCommands(configs...)

	// Отправляем запрос
	if _, err := tb.bot.Request(setCommandsConfig); err != nil {
		tb.g.Log.Errorf("Ошибка при установке меню: %v", err)
	}
}

func (tb *tgbotapiot) telegramLoop() error {
	// g := state.GetGlobal(ctx)
	mqttch := tb.g.Tele.Chan()
	stopch := tb.g.Alive.StopChan()
	tb.updateConfig = tgbotapi.NewUpdate(10)
	tb.updateConfig.Timeout = 60

	tgch := tb.bot.GetUpdatesChan(tb.updateConfig)

	for {
		select {
		case p := <-mqttch:
			rm := tb.g.ParseFromRobo(p)
			if p.Kind == tele_api.FromRobo {
				if rm.Order != nil {
					if rm.Order.OwnerType == vender_api.OwnerType_telegramUser {
						tb.g.Log.Infof("order telegramm message from robot (%v)", rm)
						tb.g.Alive.Add(1)
						tb.cookResponseN(rm.Order, p.VmId)
						tb.g.Alive.Done()
					}
				}
			}
			if rm != nil && rm.Err != nil {
				errm := fmt.Sprintf("%v: %v", p.VmId, rm.Err.Message)
				tb.tgSend(tb.admin, errm)
			}
		case tgm := <-tgch:
			// if tgm.CallbackQuery != nil && tgm.CallbackQuery.Data == "invite_url" {

			// 	token, err := tb.g.CreateWebAuthToken(tgm.CallbackQuery.From.ID, int(vender_api.OwnerType_telegramUser))
			// 	if err != nil {
			// 		tb.tgSend(tgm.CallbackQuery.From.ID, "ошибка генерации ссылки")
			// 		return nil
			// 	}
			// 	url := tb.g.Config.WebAuthCallbackURL(token)

			// 	tb.tgSend(tgm.CallbackQuery.From.ID, "Ваша одноразовая ссылка:\n"+url)
			// 	// callbackConfig := tgbotapi.CallbackConfig{
			// 	// 	CallbackQueryID: tgm.CallbackQuery.ID,
			// 	// 	URL:             url,
			// 	// }
			// 	// tb.bot.Request(callbackConfig)

			// 	delMsg := tgbotapi.NewDeleteMessage(tgm.CallbackQuery.Message.Chat.ID, tgm.CallbackQuery.Message.MessageID)
			// 	tb.bot.Request(delMsg)
			// 	// callbackConfig := tgbotapi.CallbackQuery .NewCallbackWithURL(update.CallbackQuery.ID, "https://google.com")
			// 	// bot.Request(callbackConfig)

			// }
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
			if notBot := !tgm.Message.From.IsBot; !notBot {
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
		if len(parts) < 2 {
			responseMessage = fmt.Sprintf("error bot(%v) invalid message format (%s)", m.AuthorSignature, m.Text)
			return
		}
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
	// FIXME затычка. потом переделать телеговский модуль.
	c, err := tb.g.ClientGet(m.Message.From.ID, vender_api.OwnerType_telegramUser)
	if c.Id == 0 && err != nil {
		return tb.registerNewUser(m)
	}
	if err != nil {
		tb.g.Log.Errorf("TG client:%d message:(%s) error(%v)", m.Message.From.ID, m.Message.Text, err)
		return nil
	}
	cl := tgUser{Client: c}

	// if m.Message.From.ID == tb.admin {
	// 	text := m.Message.Text
	// 	if strings.HasPrefix(text, "/approve_") {
	// 		token := strings.TrimPrefix(text, "/approve_")
	// 		tb.approveSession(token)
	// 		return nil
	// 	}
	// 	if strings.HasPrefix(text, "/deny_") {
	// 		token := strings.TrimPrefix(text, "/deny_")
	// 		tb.denySession(token)
	// 		return nil
	// 	}
	// }

	// parse command
	switch parseCommand(m.Message.Text) {
	case tgCommandWeb:
		link := tb.g.Config.Web.BaseURL + tb.g.Config.Web.Path
		tb.tgSend(cl.Id, fmt.Sprintf("ссылка на сайт: %s", link))
	// 	return nil
	case tgCommandWebInvite:
		token, err := tb.g.CreateWebAuthToken(cl.Id, int(vender_api.OwnerType_telegramUser))
		if err != nil {
			tb.tgSend(cl.Id, "ошибка генерации ссылки")
			return nil
		}
		url := tb.g.Config.WebAuthCallbackURL(token)

		// sm := tb.tgSend(cl.Id, "Ваша одноразовая ссылка приглашение:\n"+url+"\n\nСсылка действительна 5 минут.")
		msg := tgbotapi.NewMessage(cl.Id, "Нажмите кнопку ниже для входа.\nЕсли открылся встроенный браузер — нажмите ••• и выберите «Открыть в браузере»")
		btn := tgbotapi.NewInlineKeyboardButtonURL("🌐 Открыть веб", url)
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(btn),
		)
		sm, _ := tb.bot.Send(msg)
		go func() {
			time.Sleep(5 * time.Minute)
			delMsg := tgbotapi.NewDeleteMessage(sm.Chat.ID, sm.MessageID)
			tb.bot.Request(delMsg)
		}()
		return nil
	case tgCommandInvalid:
		return nil
	case tgCommandBalance:
		msg := balance(cl.Balance)
		if cl.Credit != 0 {
			available := cl.Balance + int64(cl.Credit)
			msg = msg + fmt.Sprintf("кредит: %s\nдоступно: %s", amoutToString(int64(cl.Credit)), amoutToString(available))
		}
		tb.tgSend(cl.Id, msg)
	case tgCommandCook:
		tb.commandCook(*m.Message, cl)
	case tgCommandHelp:
		return tb.replayCommandHelp(cl.Id)
	case tgCommandOther:
		if m.Message.From.ID != tb.admin {
			msg := fmt.Sprintf("команда (%s) \nнепонятно что делать.", m.Message.Text)
			tb.tgSend(cl.Id, msg)
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
			client, err := tb.g.ClientGet(m.Message.ForwardFrom.ID, vender_api.OwnerType_telegramUser)
			if err != nil {
				TgSendError(fmt.Sprintf("error get client for put bablo (%v)", err))
				return nil
			}
			switch {
			case tb.forvardMsg[2] == "bablo":
				// client.rcook.code = "bablo"
				bablo, _ := strconv.Atoi(tb.forvardMsg[3])
				tb.addCredit(client.Id, bablo)
			case tb.forvardMsg[2] == "credit":
				credit, _ := strconv.Atoi(tb.forvardMsg[3])
				tb.setCredit(client.Id, credit)
				tb.tgSend(client.Id, fmt.Sprintf("появилась возможность делать отрицательный баланс на : %d", credit))
				tb.tgSend(tb.admin, fmt.Sprintf("установлен кредит на: %d для: %d", credit, client.Id))
			default:
				tb.tgSend(tb.admin, fmt.Sprintf("не пон: %v", tb.forvardMsg))
			}
		}
	}

	return nil
}

// func (tb *tgbotapiot) setWebPassword(userId int64, password string) error {
// 	hash := tb.g.Sha256sum(password)
// 	_, err := tb.g.DB.Exec(`
//         INSERT INTO users (userid, user_type, login, hash)
//         VALUES (?0, ?1, ?2, ?3)
//         ON CONFLICT (login) DO UPDATE SET hash = ?3`,
// 		userId, int32(vender_api.OwnerType_telegramUser), fmt.Sprintf("%d", userId), hash)
// 	return err
// }

// func (tb *tgbotapiot) approveSession(token string) {
// 	_, err := tb.g.DB.Exec(
// 		"UPDATE user_sessions SET approved = true WHERE token = ?", token)
// 	if err != nil {
// 		tb.tgSend(tb.admin, "ошибка подтверждения сессии")
// 		return
// 	}
// 	tb.tgSend(tb.admin, "сессия подтверждена")
// }

// func (tb *tgbotapiot) denySession(token string) {
// 	_, err := tb.g.DB.Exec(
// 		"DELETE FROM user_sessions WHERE token = ?", token)
// 	if err != nil {
// 		tb.tgSend(tb.admin, "ошибка удаления сессии")
// 		return
// 	}
// 	tb.tgSend(tb.admin, "сессия отклонена")
// }

func (tb *tgbotapiot) addCredit(clientId int64, bablo int) {
	msgToUser := fmt.Sprintf("пополнение баланса на: %d\n", bablo)
	tb.tgSend(tb.admin, fmt.Sprintf("баланс: %d пополнен на: %d", clientId, bablo))
	tb.rcookWriteDb(clientId, -bablo*100, msgToUser)
}

func clearForwardMessage() {
	tb.forvardMsg = nil
}

func parseCommand(cmd string) tgCommand {
	// https://extendsclass.com/regex-tester.html#js
	// https://regexr.com/
	// rm := `^((/-?\d+_m?[-.0-9]+(.+?)?)|(/balance)|(/help)|(.+)|)$`
	// cmdR := regexp.MustCompile(`^((/-?\d+_m?[-.0-9]+(.+?)?)|(/balance)|(/help)|(.+)|)$`)
	cmdR := regexp.MustCompile(`^((/-?\d+_m?[-.0-9]+(.+?)?)|(.+))$`)
	parts := cmdR.FindStringSubmatch(cmd)
	// if len(parts) == 0 {
	// 	return tgCommandInvalid
	// }

	switch {
	case cmd == "/balance":
		return tgCommandBalance
	case cmd == "/help":
		return tgCommandHelp
	case cmd == "/invite":
		return tgCommandWebInvite
	case cmd == "/web":
		return tgCommandWeb
	// case strings.HasPrefix(cmd, "/setpassword "):
	// 	return tgCommandSetPassword
	case parts[2] != "":
		return tgCommandCook
	case parts[4] != "":
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
	if (int32(client.Balance) + int32(client.Credit)) <= 0 {
		tb.tgSend(client.Id, "недостаточно средств")
		return
	}
	var ok bool
	if client.rcook, ok = parseCookCommand(m.Text); !ok {
		tb.tgSend(client.Id, "команда приготовления написана с ошибкой.\n почитайте /help и сделайте понятную для меня команду.")
		return
	}
	tb.logTgDb(m)
	if !tb.checkRobo(client.rcook.vmid, client.Id) {
		return
	}
	// tb.chatId[client.Id] = client
	tb.sendCookCmdN(client)
}

func parseCookCommand(cmd string) (cs cookSruct, resultFunction bool) {
	// команда /88_m3_c4_s4
	// приготовить робот:88 код:3 cream:4 sugar:4 (сливики/сахар необязательные)
	// 1 - robo, 2 - code , 3 - valid creame, 4 - value creme, 5 - valid sugar, 6 value sugar
	// var cs cookSruct
	// reCmdMake := regexp.MustCompile(`^/(-?\d+)_m?([-.0-9]+)(_?[c,C,с,С]([0-6]))?(_?[s,S]([0-8]))?$`)
	reCmdMake := regexp.MustCompile(`^/(-?\d+)_m?([-.0-9]+)(_?[c,C,с,С](\d))?(_?[s,S](\d))?$`)
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
	ok, text := tb.g.СheckRobotMakeState(vmid)
	if ok {
		return true
	}
	tb.tgSend(user, text)
	return false
}

func (tb *tgbotapiot) logTgDbChange(m tgbotapi.Message) {
	const q = `UPDATE tg_chat set (changedate, changetext) = (?0,?1) WHERE messageid=?2;`
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
	const q = `UPDATE users SET credit = ?2 WHERE userid = ?0 and user_type = ?1;`
	tb.g.Alive.Add(1)
	_, err := tb.g.DB.Exec(q, id, vender_api.OwnerType_telegramUser, credit)
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

// func (tb *tgbotapiot) logUserOrder(userId int64, userType int32, action string, balanceInfo int64) {
// 	const q = `INSERT INTO user_orders (userid, user_type, action, balance_info) VALUES (?0, ?1, ?2, ?3)`
// 	tb.g.Alive.Add(1)
// 	_, err := tb.g.DB.Exec(q, userId, userType, "TG "+action, float64(balanceInfo)/100)
// 	tb.g.Alive.Done()
// 	if err != nil {
// 		tb.g.Log.Errorf("logUserOrder userId=%d userType=%d err=%v", userId, userType, err)
// 	}
// }

func (tb *tgbotapiot) sendCookCmdN(tgUser tgUser) {
	moneyAvalible := tgUser.Balance + int64(tgUser.Credit)
	if moneyAvalible < 0 {
		moneyAvalible = 0
	}
	trm := vender_api.ToRoboMessage{
		ServerTime: time.Now().Unix(),
		Cmd:        vender_api.MessageType_makeOrder,
		MakeOrder: &vender_api.Order{
			MenuCode:      tgUser.rcook.code,
			Amount:        uint32(moneyAvalible),
			OrderStatus:   vender_api.OrderStatus_doTransferred,
			PaymentMethod: vender_api.PaymentMethod_Balance,
			OwnerInt:      tgUser.Id,
			OwnerType:     vender_api.OwnerType_telegramUser,
		},
	}
	if tgUser.rcook.cream != 0 {
		trm.MakeOrder.Cream = []byte{tgUser.rcook.cream}
	}
	if tgUser.rcook.sugar != 0 {
		trm.MakeOrder.Sugar = []byte{tgUser.rcook.sugar}
	}
	tb.g.Log.Infof("telegram client (%v) send remote cook code:%s", tgUser.Id, tgUser.rcook.code)
	tb.g.Tele.SendToRobo(tgUser.rcook.vmid, &trm)
}

func (tb *tgbotapiot) cookResponseN(ro *vender_api.Order, vmid int32) {
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
		msg = fmt.Sprintf("начинаю готовить. \nкод: %s автомат: %d", ro.MenuCode, vmid)
		cl, err := tb.g.ClientGet(client, vender_api.OwnerType_telegramUser)
		if err != nil {
			tb.g.Log.Errorf("cookResponseN getClient userId=%d err=%v", client, err)
		}
		tb.g.LogUserOrder("TG", client, int32(vender_api.OwnerType_telegramUser), msg, cl.Balance)
		tb.tgSend(int64(client), msg)
		return
	case vender_api.OrderStatus_orderError:
		msg = "ошибка приготовления."
	case vender_api.OrderStatus_complete:
		finMsg := fmt.Sprintf("автомат : %d приготовил код: %s цена: %s\n",
			vmid,
			ro.MenuCode,
			amoutToString(int64(ro.Amount)))
		if c := tb.rcookWriteDb(ro.OwnerInt, int(ro.Amount), finMsg); c.Diskont != 0 {
			go func() {
				bonus := (int(ro.Amount) * c.Diskont) / 100
				time.Sleep(10 * time.Second)
				// cl, _ := tb.g.ClientGet(client, vender_api.OwnerType_telegramUser)
				// // cl.rcook.code = "bonus"
				bunusMgs := fmt.Sprintf("начислен бонус: %s\n", amoutToString(int64(bonus)))
				tb.rcookWriteDb(c.Id, -bonus, bunusMgs)
			}()
		}

		tb.g.Log.Infof("cooking finished telegram client:%d type:%s code:%s", ro.OwnerInt, ro.OwnerType.String(), ro.MenuCode)
	default:
		msg = "что то пошло не так. без паники. хозяину уже в сообщили."
		TgSendError(fmt.Sprintf("cook responce status unknow. vmid=%d code error invalid order=%s", vmid, ro.String()))
	}
	tb.tgSend(client, msg)
}

func (tb *tgbotapiot) rcookWriteDb(userId int64, price int, addMsg ...string) state.Client {
	tb.g.ClientUpdateBalance(userId, vender_api.OwnerType_telegramUser, int64(price))
	cl, _ := tb.g.ClientGet(userId, vender_api.OwnerType_telegramUser)
	var msg string
	if len(addMsg) != 0 {
		msg = addMsg[0]
	}
	tb.g.LogUserOrder("TG", userId, int32(vender_api.OwnerType_telegramUser), msg, cl.Balance)
	msg = msg + balance(cl.Balance)
	tb.tgSend(userId, msg)
	return cl
}

func TgSendError(s string) {
	if tb.admin > 0 {
		tb.tgSend(int64(tb.admin), s)
		tb.g.Log.Error(s)
	}
}

func (tb *tgbotapiot) tgSend(chatid int64, s string) (m tgbotapi.Message) {
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
	return m
}

func (tb *tgbotapiot) addUserToDB(c *tgbotapi.Contact) error {
	memo := fmt.Sprintf("firs name:%s last name:%s", c.FirstName, c.LastName)
	_, err := tb.g.DB.Exec(`INSERT INTO users ( userid, user_type, phone, memo ) values (?0, ?1, ?2, ?3);`,
		c.UserID, vender_api.OwnerType_telegramUser, c.PhoneNumber, memo)
	return err
}

func (tb *tgbotapiot) registerNewUser(m tgbotapi.Update) error {
	const regMess = "Для продолжения работы, нужно боту разрешить увидеть Ваш номер телефона."
	var msg tgbotapi.MessageConfig
	var err error

	if m.Message.Text == "/start" {
		msg = tgbotapi.NewMessage(m.Message.Chat.ID, regMess)
		btn := tgbotapi.KeyboardButton{
			Text:           "разрешить боту увидеть номер телефона",
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
	if err = tb.addUserToDB(m.Message.Contact); err != nil {
		return err
	}
	complitMessage := "теперь можно заказывать. \n/help - получить справку."
	msg = tgbotapi.NewMessage(m.Message.Chat.ID, complitMessage)
	msg.ReplyMarkup = tgbotapi.NewRemoveKeyboard(true)
	_, _ = tb.bot.Send(msg)
	return nil
}

func (tb *tgbotapiot) replayCommandHelp(cl int64) error {
	msg := "пока это работает так:\n" +
		"для заказа напитка нужно указать номер автомата (номер указан в право верхнем углу автомата), код напитка и, если требуется, тюнинг сливок и сахара\n" +
		"пример: хочется заказать атомату 5 напиток с кодом 23. для этого боту надо написать\n" +
		"/5_23\n" +
		"для тюнинга сливок и сахара надо добавить _с (это cream = сливки) и/или _s (это sugar = сахар ) например:\n" +
		"/5_23c3s2\n" +
		"это означает, автомату=5, приготовить код=23, сливки=3, сахар=2\n" +
		"если непонятно, позвоните/напишите @Alexey_Milko, он расскажет.\n"
	tb.tgSend(cl, msg)
	return nil
}
