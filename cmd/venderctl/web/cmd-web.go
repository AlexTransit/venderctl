package web

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"net/http"

	vender_api "github.com/AlexTransit/vender/tele"
	"github.com/AlexTransit/venderctl/cmd/internal/cli"
	"github.com/AlexTransit/venderctl/internal/state"
	tele_api "github.com/AlexTransit/venderctl/internal/tele/api"
	"github.com/gin-gonic/gin"
	"github.com/juju/errors"
)

const CmdName = "web"

//go:embed index.html
var indexHTML []byte

//go:embed manifest.webmanifest
var manifestJSON []byte

//go:embed sw.js
var serviceWorkerJS []byte

//go:embed icon-192.png
var icon192 []byte

//go:embed icon-512.png
var icon512 []byte

//go:embed apple-touch-icon.png
var appleTouchIcon []byte

//go:embed app.js
var appJS []byte

//go:embed app.css
var appCSS []byte

var Cmd = cli.Cmd{
	Name:   CmdName,
	Desc:   "web. control vmc via web browser",
	Action: webApp,
}

type WebHandler struct {
	App         *state.Global
	OrderEvents *EventBus
	authStore   webAuthStore
}

func webApp(ctx context.Context, flags *flag.FlagSet) (err error) {
	g := state.GetGlobal(ctx)
	g.InitVMC()
	configPath := flags.Lookup("config").Value.String()
	g.Config = state.MustReadConfig(g.Log, state.NewOsFullReader(), configPath)
	g.Config.Tele.SetMode("web")

	g.InitDB(CmdName)
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

	web := r.Group(g.Config.WebRoutePrefix())

	// маршруты
	web.GET("/app.js", func(c *gin.Context) {
		c.Data(http.StatusOK, "application/javascript; charset=utf-8", appJS)
	})
	web.GET("/app.css", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/css; charset=utf-8", appCSS)
	})

	web.GET("/auth/callback", h.HandleAuth)
	web.POST("/auth/callback", h.HandleAuth)
	web.GET("/auth/logout", h.Logout)

	web.GET("/api/admin/messages", h.CheckAuth(), h.GetAdminMessages)
	web.POST("/api/admin/send", h.CheckAuth(), h.AdminSendMessage)
	web.POST("/api/admin/reply", h.CheckAuth(), h.AdminReplyMessage)
	web.POST("/api/admin/notification-click", h.AdminNotificationClick)
	web.POST("/api/admin/reply/ack", h.CheckAuth(), h.AdminReplyAck)
	web.POST("/api/admin/message", h.CheckAuth(), h.SendAdminMessage)
	web.GET("/api/admin/users", h.CheckAuth(), h.AdminGetUsers)

	web.GET("/api/balance", h.CheckAuth(), h.GetBalance)
	web.POST("/api/favorite", h.CheckAuth(), h.SetFavorite)
	web.POST("/api/user/admin-reply", h.CheckAuth(), h.UserReplyAdminMessage)
	web.GET("/api/push/public-key", h.CheckAuth(), h.PushPublicKey)
	web.POST("/api/push/subscribe", h.CheckAuth(), h.PushSubscribe)
	web.POST("/api/push/unsubscribe", h.CheckAuth(), h.PushUnsubscribe)

	// web.GET("/api/machines", h.GetMachines)
	web.GET("/api/drinks/popular", h.CheckAuth(), h.GetPopular)

	web.POST("/api/order/check", h.CheckAuth(), h.PreviewOrder)
	web.POST("/api/order/start", h.CheckAuth(), h.StartOrder)
	web.GET("/api/order/ws", h.CheckAuth(), h.OrderWS)

	web.GET("/api/orders", h.CheckAuth(), h.GetOrders)

	web.GET("/open", h.OpenInvite)

	serveIndex := func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
	}
	serveManifest := func(c *gin.Context) {
		c.Data(http.StatusOK, "application/manifest+json; charset=utf-8", manifestJSON)
	}
	serveSW := func(c *gin.Context) {
		c.Header("Service-Worker-Allowed", h.App.Config.WebRootPath())
		c.Data(http.StatusOK, "application/javascript; charset=utf-8", serviceWorkerJS)
	}
	serveIcon192 := func(c *gin.Context) {
		c.Data(http.StatusOK, "image/png", icon192)
	}
	serveIcon512 := func(c *gin.Context) {
		c.Data(http.StatusOK, "image/png", icon512)
	}
	serveAppleTouchIcon := func(c *gin.Context) {
		c.Data(http.StatusOK, "image/png", appleTouchIcon)
	}
	web.GET("/", serveIndex)
	web.GET("/index.html", serveIndex)
	web.GET("/manifest.webmanifest", serveManifest)
	web.GET("/sw.js", serveSW)
	web.GET("/icon-192.png", serveIcon192)
	web.GET("/icon-512.png", serveIcon512)
	web.GET("/apple-touch-icon.png", serveAppleTouchIcon)

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
					action := fmt.Sprintf("приготовил №%d код:%s цена:%.2f",
						p.VmId, order.MenuCode,
						float64(order.Amount)/100)
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
					pushBody := "Ваш напиток готов. Приятного аппетита."
					if cashback, ok := event["cashback"].(float64); ok && cashback > 0 {
						pushBody = fmt.Sprintf("Напиток готов. Кэшбек: %.2f ₽", cashback)
					}
					h.sendWebPushToUser(userId, int32(userType), "Vender Web", pushBody)
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
