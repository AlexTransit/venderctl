package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/gin-gonic/gin"
)

func (h *WebHandler) setAuthCookie(c *gin.Context, userId int64) {
	value := fmt.Sprintf("%d", userId)

	// Подписываем userId
	secret := h.App.Config.Web.SecretKey
	sig := signValue(secret, value)

	cookie := fmt.Sprintf("%s:%s", value, sig)

	c.SetCookie(
		"auth_user_id",
		cookie,
		3600*24,
		"/",
		"",
		false, // временно HTTP
		true,  // httpOnly
	)
}

func signValue(secret, value string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}
