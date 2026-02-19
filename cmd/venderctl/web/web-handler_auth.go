package web

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func (h *WebHandler) HandleAuth(c *gin.Context) {
	userIdStr := c.Query("id")
	password := c.Query("pass")
	hash := c.Query("hash")

	if userIdStr == "" {
		c.String(400, "ID is required")
		return
	}

	uid, err := strconv.ParseInt(userIdStr, 10, 64)
	if err != nil {
		c.String(400, "Invalid ID")
		return
	}

	if password != "" {
		if password != h.App.Config.Web.DebugPassword {
			c.String(403, "Wrong password")
			return
		}
		h.App.Log.WarningF("Admin login by password: ID %d", uid)

	} else if hash != "" {
		if !checkTelegramAuth(c.Request.URL.Query(), h.App.Config.Telegram.TelegrammBotApi) {
			c.String(403, "Invalid Telegram hash")
			return
		}
		h.App.Log.Infof("Telegram login: ID %d", uid)

	} else {
		c.String(403, "Auth method not allowed")
		return
	}

	h.setAuthCookie(c, uid)
	c.Redirect(302, "/")
}

func (h *WebHandler) Logout(c *gin.Context) {
	c.SetCookie("auth_user_id", "", -1, "/", "", false, true)
	c.Redirect(http.StatusFound, "/")
}
