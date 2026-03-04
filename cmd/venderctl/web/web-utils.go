package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/gin-gonic/gin"
)

func (h *WebHandler) setAuthCookie(c *gin.Context, userId int64, token string) {
	value := fmt.Sprintf("%d", userId)
	secret := h.App.Config.Web.SecretKey
	sig := signValue(secret, value+token)
	cookie := fmt.Sprintf("%s:%s:%s", value, token, sig)
	c.SetCookie("auth_user_id", cookie, 3600*24*30, "/", "", false, true)
}

func signValue(secret, value string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}
