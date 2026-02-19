package web

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v9"
)

func (h *WebHandler) CheckAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		cookie, err := c.Cookie("auth_user_id")
		if err != nil {
			c.AbortWithStatusJSON(401, gin.H{"error": "Unauthorized"})
			return
		}

		parts := strings.Split(cookie, ":")
		if len(parts) != 2 {
			c.AbortWithStatusJSON(401, gin.H{"error": "Invalid cookie"})
			return
		}

		userIdStr := parts[0]
		sig := parts[1]

		expected := signValue(h.App.Config.Web.SecretKey, userIdStr)
		if sig != expected {
			c.AbortWithStatusJSON(401, gin.H{"error": "Invalid signature"})
			return
		}

		uid, err := strconv.ParseInt(userIdStr, 10, 64)
		if err != nil {
			c.AbortWithStatusJSON(401, gin.H{"error": "Invalid user id"})
			return
		}

		var exists bool
		_, err = h.App.DB.QueryOne(pg.Scan(&exists),
			"SELECT EXISTS(SELECT 1 FROM tg_user WHERE userid = ?)", uid)

		if err != nil || !exists {
			c.AbortWithStatusJSON(401, gin.H{"error": "User not found"})
			return
		}

		c.Set("user_id", uid)
		c.Next()
	}
}
