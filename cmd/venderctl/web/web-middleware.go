package web

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

func (h *WebHandler) CheckAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		cookie, err := c.Cookie("auth_user_id")
		if err != nil {
			c.AbortWithStatusJSON(401, gin.H{"error": "Unauthorized"})
			return
		}

		parts := strings.Split(cookie, ":")
		if len(parts) != 3 {
			c.AbortWithStatusJSON(401, gin.H{"error": "Invalid cookie"})
			return
		}

		userIdStr := parts[0]
		token := parts[1]
		sig := parts[2]

		expected := signValue(h.App.Config.Web.SecretKey, userIdStr+token)
		if sig != expected {
			c.AbortWithStatusJSON(401, gin.H{"error": "Invalid signature"})
			return
		}

		uid, err := strconv.ParseInt(userIdStr, 10, 64)
		if err != nil {
			c.AbortWithStatusJSON(401, gin.H{"error": "Invalid user id"})
			return
		}

		var session struct {
			Approved bool  `pg:"approved"`
			Revoked  bool  `pg:"revoked"`
			UserType int   `pg:"user_type"`
		}

		_, err = h.App.DB.QueryOne(&session,
			"SELECT approved, revoked, user_type FROM user_sessions WHERE token = ? AND userid = ?", token, uid)
		if err != nil || !session.Approved || session.Revoked {
			c.AbortWithStatusJSON(401, gin.H{"error": "Session not approved"})
			return
		}

		c.Set("user_id", uid)
		c.Set("user_type", session.UserType)
		c.Next()
	}
}
