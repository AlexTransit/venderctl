package web

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"sync"

	vender_api "github.com/AlexTransit/vender/tele"
	"github.com/AlexTransit/venderctl/cmd/internal/cli"
	"github.com/AlexTransit/venderctl/internal/state"
	tele_api "github.com/AlexTransit/venderctl/internal/tele/api"
	"github.com/gin-gonic/gin"
	"github.com/juju/errors"
)

const CmdName = "web"

var Cmd = cli.Cmd{
	Name:   CmdName,
	Desc:   "web. control vmc via web browser",
	Action: webApp,
}

type WebHandler struct {
	App         *state.Global
	OrderEvents *EventBus
	orderTypes  *orderTypeStore
}

type orderTypeKey struct {
	userId int64
	vmid   int32
	drink  string
}

type orderTypeStore struct {
	mu sync.RWMutex
	m  map[orderTypeKey]int32
}

func webApp(ctx context.Context, flags *flag.FlagSet) (err error) {
	g := state.GetGlobal(ctx)
	g.InitVMC()
	configPath := flags.Lookup("config").Value.String()
	g.Config = state.MustReadConfig(g.Log, state.NewOsFullReader(), configPath)
	g.Config.Tele.SetMode("web")

	if err = g.InitDB(CmdName); err != nil {
		return errors.Annotate(err, "db_init")
	}
	if err = g.Tele.Init(ctx, g.Log, g.Config.Tele); err != nil {
		return errors.Annotate(err, "MQTT.Init")
	}

	h := &WebHandler{App: g}
	h.OrderEvents = NewEventBus()
	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.CustomRecovery(func(c *gin.Context, recovered interface{}) {
		g.Log.Errorf("web panic: %v", recovered)
		c.JSON(500, gin.H{"error": "internal server error"})
	}))

	// маршруты
	r.GET("/auth/callback", h.HandleAuth)
	r.POST("/auth/callback", h.HandleAuth)
	r.GET("/auth/logout", h.Logout)

	r.GET("/api/balance", h.CheckAuth(), h.GetBalance)
	r.POST("/api/favorite", h.CheckAuth(), h.SetFavorite)

	r.GET("/api/machines", h.GetMachines)
	r.GET("/api/drinks/popular", h.CheckAuth(), h.GetPopular)

	r.POST("/api/order/check", h.CheckAuth(), h.PreviewOrder)
	r.POST("/api/order/start", h.CheckAuth(), h.StartOrder)
	r.GET("/api/order/ws", h.CheckAuth(), h.OrderWS)

	r.GET("/api/orders", h.CheckAuth(), h.GetOrders)

	r.StaticFile("/", "./web/index.html")
	r.SetTrustedProxies([]string{"127.0.0.1"})
	r.NoRoute(func(c *gin.Context) {
		g.Log.Errorf("problematic action. ip=%s host=%s path=%s", c.ClientIP(), c.Request.Host, c.Request.RequestURI)
	})
	srv := &http.Server{
		Addr:    ":8085",
		Handler: r,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			g.Log.Errorf("web server listen error: %v", err)
		}
	}()
	go h.ListenMQTT(ctx)
	<-g.Alive.StopChan()
	return srv.Shutdown(ctx)
}
func (h *WebHandler) ListenMQTT(ctx context.Context) {
	mqttch := h.App.Tele.Chan()
	stopch := h.App.Alive.StopChan()

	for {
		select {
		case p := <-mqttch:
			rm := h.App.ParseFromRobo(p)

			if p.Kind == tele_api.FromRobo && rm.Order != nil {
				order := rm.Order

				// Фильтруем — только для web клиентов
				if order.OwnerType >= 0 {
					break
				}

				userId := int64(order.OwnerInt)
				userType := -order.OwnerType
				h.App.Log.Infof("order response from robot (%v)", rm)

				// Формируем ответ для фронта
				event := gin.H{
					"status": order.OrderStatus.String(),
					"vmid":   p.VmId,
					"drink":  order.MenuCode,
					"amount": float64(order.Amount) / 100,
				}

				// Добавляем дополнительные данные в зависимости от статуса
				switch order.OrderStatus {
				case vender_api.OrderStatus_executionStart:
					event["message"] = "начинаю готовить"
				case vender_api.OrderStatus_complete:
					event["message"] = "готово"
					h.App.ClientUpdateBalance(userId, userType, int64(order.Amount))

					cl, _ := h.App.ClientGet(userId, userType)
					action := fmt.Sprintf("приготовил №%d код:%s цена:%.2f баланс:%.2f",
						p.VmId, order.MenuCode,
						float64(order.Amount)/100,
						float64(cl.Balance)/100)
					h.App.LogUserOrder("Web", userId, int32(userType), action, cl.Balance)
					if cl.Diskont > 0 {
						bonus := int64(order.Amount) * int64(cl.Diskont) / 100
						if bonus > 0 {
							h.App.ClientUpdateBalance(userId, userType, -bonus)
							clAfterBonus, err := h.App.ClientGet(userId, userType)
							if err != nil {
								h.App.Log.Errorf("web bonus ClientGet userId=%d err=%v", userId, err)
							} else {
								bonusMsg := fmt.Sprintf("начислен бонус: %.2f", float64(bonus)/100)
								h.App.LogUserOrder("Web", userId, int32(userType), bonusMsg, clAfterBonus.Balance)
								event["cashback"] = float64(bonus) / 100
							}
						}
					}
				case vender_api.OrderStatus_executionInaccessible:
					event["message"] = "код недоступен"
				case vender_api.OrderStatus_overdraft:
					event["message"] = "недостаточно средств"
				case vender_api.OrderStatus_orderError:
					event["message"] = "ошибка приготовления"
				}

				// Отправляем событие подписанному клиенту
				h.OrderEvents.Publish(userId, int32(userType), event)
			}

		case <-stopch:
			return
		}
	}
}
