package telegramm

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

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

type tgbotapiot struct {
	bot          *tgbotapi.BotAPI
	updateConfig tgbotapi.UpdateConfig
	admin        int64
	g            *state.Global
	chatId       map[int64]client
}

type client struct {
	Id      uint32
	Balance int32
	Credit  uint32
	vmid    int32
	rcook   cookSrruct
}

type cookSrruct struct {
	// price uint32
	code  string
	sugar uint8
	cream uint8
}

// type tgRemoteMakeStruct struct {
// 	rvmid int32
// 	rcode string
// }

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

	tb.bot.Debug = false
	tb.chatId = make(map[int64]client)
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
			if tgm.Message == nil {
				fmt.Printf("\n\033[41m (%v) \033[0m\n\n", tgm.EditedMessage)
				break
			}
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
func (tb *tgbotapiot) onTeleBot(m tgbotapi.Update) error {
	cl, err := tb.getClient(m.Message.From.ID)
	if err != nil {
		return err
	}
	//parse command
	cl.vmid = 88
	cl.rcook.code = "1"
	cl.rcook.cream = 4
	cl.rcook.sugar = 4
	if !tb.g.Vmc[cl.vmid].Connect {
		return tb.tgSend(m.Message.From.ID, "автомат не в сети.")
	}
	if tb.g.Vmc[cl.vmid].State == vender_api.State_Invalid {
		cmd := &vender_api.Command{
			Task: &vender_api.Command_GetState{},
		}
		_ = tb.g.Tele.SendCommand(cl.vmid, cmd)
	}
	if tb.g.Vmc[cl.vmid].State != vender_api.State_Nominal {
		return tb.tgSend(m.Message.From.ID, "автомат сейчас не может выполнить заказ.")
	}

	tb.chatId[m.Message.Chat.ID] = cl

	return tb.sendCookCmd(m.Message.Chat.ID)
}

func (tb *tgbotapiot) sendCookCmd(chatId int64) error {
	cl := tb.chatId[chatId]
	cmd := &vender_api.Command{
		Executer: chatId,
		Lock:     false,
		Task: &vender_api.Command_Cook{
			Cook: &vender_api.Command_ArgCook{
				Menucode:      cl.rcook.code,
				Cream:         []byte{cl.rcook.cream},
				Sugar:         []byte{cl.rcook.sugar},
				Balance:       cl.Balance + int32(cl.Credit),
				PaymentMethod: vender_api.PaymentMethod_Balance,
			}},
	}
	tb.g.Log.Infof("client id:%d send remote cook code:%s", cl.Id, cl.rcook.code)
	return tb.g.Tele.SendCommand(cl.vmid, cmd)
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
			if err := tb.cookResponse(rm); err != nil {
				return err
			}
		}
		return nil

	default:
		// TgSendError(fmt.Sprintf("code error invalid packet=%s", p.Kind.String()))
		return nil
	}
	return nil
}
func (tb *tgbotapiot) cookResponse(rm *vender_api.Response) error {
	var msg string
	switch rm.CookReplay {
	case vender_api.CookReplay_vmcbusy:
		msg = "автомат в данный момент обрабатывает другой заказ. попробуйте позднее."
	case vender_api.CookReplay_cookStart:
		msg = "начинаю готовить"
		err := tb.tgSend(int64(rm.Executer), msg)
		return err
	case vender_api.CookReplay_cookFinish:
		msg = "заказ выполнен. приятного аппетита."
		// tb.chatId[rm.Executer].Balance - rm.ValidateReplay
		msg = msg + "\nбаланс :" + fmt.Sprintf("%d", tb.rcookWriteDb(rm))
	case vender_api.CookReplay_cookInaccessible:
		msg = "код недоступен"
	case vender_api.CookReplay_cookOverdraft:
		msg = "недостаточно средств. пополните баланс и попробуйте снова."
	case vender_api.CookReplay_cookError:
		msg = "ошибка приготовления."
	default:
		msg = "что то пошло не так. без паники. хозяину уже в сообщили."
		TgSendError(fmt.Sprintf("vmid=%d code error invalid packet=%s", tb.chatId[rm.Executer].vmid, rm.String()))
	}
	err := tb.tgSend(int64(rm.Executer), msg)
	delete(tb.chatId, rm.Executer)
	return err
}

func (tb *tgbotapiot) rcookWriteDb(rm *vender_api.Response) (bal int32) {
	tb.g.Log.Infof("cooking finished client:%d code:%s", rm.Executer, tb.chatId[rm.Executer].rcook.code)
	c := tb.chatId[rm.Executer].rcook
	nb := tb.chatId[rm.Executer].Balance - int32(rm.ValidateReplay/100)
	const q = `insert into trans (vmid,received,menu_code,options,price,method,executer) values (?0,current_timestamp,?1,?2,?3,?4,?5);
	UPDATE vmc_user set balance = ?6 WHERE idtelegram = ?5;`
	_, err := tb.g.DB.Exec(q, tb.chatId[rm.Executer].vmid, c.code, pg.Array([2]uint8{c.cream, c.sugar}), rm.ValidateReplay, vender_api.PaymentMethod_Balance, rm.Executer, nb)
	if err != nil {
		tb.g.Log.Errorf("db query=%s chatid=%v err=%v", q, tb.chatId[rm.Executer], err)
	}
	return nb
}

func TgSendError(s string) {
	if tb.admin > 0 {
		_ = tb.tgSend(int64(tb.admin), s)
		tb.g.Log.Error(s)
	}
}

func (tb *tgbotapiot) tgSend(chatid int64, s string) error {
	msg := tgbotapi.NewMessage(chatid, s)
	// msg.ReplyToMessageID = t.replayMessageID
	_, err := tb.bot.Send(msg)
	// return m.Date, err
	return err
}

func (tb *tgbotapiot) getClient(c int64) (client, error) {
	tb.g.Alive.Add(1)
	db := tb.g.DB.Conn()
	var cl client
	_, err := db.QueryOne(&cl, `SELECT id, balance,	credit FROM vmc_user WHERE idtelegram = ?`, c)
	// _ = db.Close()
	tb.g.Alive.Done()
	if err == pg.ErrNoRows {
		return cl, errors.Annotate(err, "client not found in db")
	} else if err != nil {
		return cl, errors.Annotate(err, "telegram client db read error")
	}
	return cl, nil
}

// func tgResponseMessage(ctx context.Context, vmc int32, m vender_api.Response) error {
// 	msg := tgbotapi.NewMessage(t.chatID, t.replayMessage)
// 	msg.ReplyToMessageID = t.replayMessageID
// 	m, err := tb.bot.Send(msg)
// 	// return m.Date, err

// 	return err
// }

// func chatWork(ctx context.Context, t tgTaskStruct) {
// 	defer delete(tb.chatId, t.chatID)
// 	g := state.GetGlobal(ctx)
// 	t.replayMessage = "не понял"

// 	switch t.command {
// 	case "info":
// 		t.replayMessage = fmt.Sprintf("баланс = %d у.е.", t.Credit)
// 	case "new":
// 		t.replayMessage = "пока не умею. надо просить хозяина. что бы добавил."
// 	}

// 	// команда '/r1_12'  робот 1 сделать код 12
// 	reCmdMake := regexp.MustCompile(`^r(-?\d+)_(\d+)$`)
// 	parts := reCmdMake.FindStringSubmatch(t.command)
// 	if len(parts) == 3 {
// 		var remMake tgRemoteMakeStruct
// 		i, err := strconv.ParseInt(parts[1], 10, 32)
// 		remMake.rvmid = int32(i)
// 		if err != nil {
// 			g.Log.Errorf("telegramm remore parse vmID")
// 		}
// 		remMake.rcode = parts[2]
// 		t.replayMessage = remoteMake(ctx, t, remMake)
// 	}
// 	dt, err := tgSend(t)
// 	if err != nil {
// 		g.Log.Errorf("telegram send message error (%v)", err)
// 	}
// 	g.Log.Infof("complete task time:%s task:(%v)", time.Unix(int64(dt), 0), t)
// }

// func remoteMake(ctx context.Context, t tgTaskStruct, r tgRemoteMakeStruct) string {
// 	g := state.GetGlobal(ctx)
// 	if !g.Vmc[r.rvmid].Connect {
// 		return "Автомат не подключен к сети"
// 	}
// 	// if g.Vmc[r.rvmid].State != tele.State_Nominal {
// 	// 	return "Автомат занят."
// 	// }

// 	cmd := &vender_api.Command{
// 		Executer: t.Id,
// 		Task: &vender_api.Command_Exec{Exec: &vender_api.Command_ArgExec{
// 			Scenario: "menu." + r.rcode,
// 			Lock:     true,
// 		}},
// 	}
// 	// save task to database
// 	// a, err := g.Tele.CommandTx(r.rvmid, cmd)
// 	// mqttch1 := g.Tele.Chan()
// err := g.Tele.SendCommand(r.rvmid, cmd)
// 	fmt.Printf("\n\033[41m err(%v) \033[0m\n\n", err)

// 	// pp := <-mqttch1
// 	// fmt.Printf("\n\033[41m REM(%v) \033[0m\n\n", pp)

// 	// tmr := time.NewTimer(5 * time.Second)
// 	// defer tmr.Stop()

// 	return ""
// }
