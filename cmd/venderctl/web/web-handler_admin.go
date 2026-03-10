package web

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func (h *WebHandler) SendAdminMessage(c *gin.Context) {
	var req struct {
		Message string `json:"message"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}

	msg := strings.TrimSpace(req.Message)
	if msg == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "empty message"})
		return
	}
	if len([]rune(msg)) > 1000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message too long"})
		return
	}

	adminID := h.App.Config.Telegram.TelegramAdmin
	if adminID <= 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "admin id is not configured"})
		return
	}
	if !h.webPushConfigured() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "web push not configured"})
		return
	}

	userID := c.MustGet("user_id").(int64)
	userType := c.MustGet("user_type").(int)

	title := "Сообщение администратору"
	body := fmt.Sprintf("От %d (type %d): %s", userID, userType, msg)
	h.sendWebPushToUserAnyTypeWithData(adminID, title, body, map[string]any{
		"sender_id":   userID,
		"sender_type": userType,
		"message":     msg,
		"click_api":   h.App.Config.WebPathWithPrefix("/api/admin/notification-click"),
	})

	h.App.Log.Infof("web admin message sent from user=%d type=%d to admin=%d", userID, userType, adminID)

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *WebHandler) AdminNotificationClick(c *gin.Context) {
	var req struct {
		UserID   int64  `json:"user_id"`
		UserType int    `json:"user_type"`
		Message  string `json:"message"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}

	h.App.Log.Infof("admin clicked notification: user_id=%d user_type=%d message=%q", req.UserID, req.UserType, strings.TrimSpace(req.Message))
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
