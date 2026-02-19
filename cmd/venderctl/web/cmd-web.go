package web

import (
	"context"
	"flag"
	"net/http"

	"github.com/AlexTransit/venderctl/cmd/internal/cli"
	"github.com/AlexTransit/venderctl/internal/state"
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
	App *state.Global
}

func webApp(ctx context.Context, flags *flag.FlagSet) error {
	g := state.GetGlobal(ctx)
	configPath := flags.Lookup("config").Value.String()
	g.Config = state.MustReadConfig(g.Log, state.NewOsFullReader(), configPath)
	g.Config.Tele.SetMode("web")

	if err := g.InitDB(CmdName); err != nil {
		return errors.Annotate(err, "db_init")
	}

	h := &WebHandler{App: g}
	r := gin.Default()

	// маршруты
	r.GET("/auth/callback", h.HandleAuth)
	r.GET("/auth/logout", h.Logout)

	r.GET("/api/balance", h.CheckAuth(), h.GetBalance)
	r.POST("/api/favorite", h.CheckAuth(), h.SetFavorite)

	r.GET("/api/machines", h.GetMachines)
	r.GET("/api/drinks/popular", h.GetPopular)

	r.POST("/api/order", h.CheckAuth(), h.MakeOrder)

	r.StaticFile("/", "./web/index.html")
	r.SetTrustedProxies([]string{"192.168.1.3"})

	srv := &http.Server{
		Addr:    ":8085",
		Handler: r,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			g.Log.Errorf("web server listen error: %v", err)
		}
	}()

	<-g.Alive.StopChan()
	return srv.Shutdown(ctx)
}
