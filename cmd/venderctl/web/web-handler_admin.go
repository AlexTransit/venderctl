package web

import (
	"fmt"
	"net/http"
	"strings"
	"time"

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
	var messageID int64
	_, err := h.App.DB.QueryOne(&messageID,
		`INSERT INTO web_admin_messages (userid, user_type, message, from_admin) VALUES (?0, ?1, ?2, false) RETURNING id`,
		userID, userType, msg,
	)
	if err != nil {
		h.App.Log.Errorf("web admin message insert error user=%d type=%d err=%v", userID, userType, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}

	h.sendWebPushToUserAnyTypeWithData(adminID, title, body, map[string]any{
		"sender_id":   userID,
		"sender_type": userType,
		"message":     msg,
		"message_id":  messageID,
		"click_api":   h.App.Config.WebPathWithPrefix("/api/admin/notification-click"),
	})

	h.App.Log.Infof("web admin message sent from user=%d type=%d to admin=%d message_id=%d", userID, userType, adminID, messageID)

	c.JSON(http.StatusOK, gin.H{"status": "ok", "id": messageID})
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

func (h *WebHandler) AdminReplyMessage(c *gin.Context) {
	var req struct {
		MessageID int64  `json:"message_id"`
		Reply     string `json:"reply"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	reply := strings.TrimSpace(req.Reply)
	if req.MessageID <= 0 || reply == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message_id and reply are required"})
		return
	}
	if len([]rune(reply)) > 1000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reply too long"})
		return
	}

	adminID := h.App.Config.Telegram.TelegramAdmin
	userID := c.MustGet("user_id").(int64)
	if adminID <= 0 || userID != adminID {
		c.JSON(http.StatusForbidden, gin.H{"error": "admin only"})
		return
	}

	res, err := h.App.DB.Exec(
		`UPDATE web_admin_messages SET reply = ?0, replied_at = now(), read_at = NULL WHERE id = ?1 AND from_admin = false`,
		reply, req.MessageID,
	)
	if err != nil {
		h.App.Log.Errorf("admin reply update error message_id=%d err=%v", req.MessageID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	if res.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "message not found"})
		return
	}

	var msg struct {
		UserID   int64  `pg:"userid"`
		UserType int32  `pg:"user_type"`
		Message  string `pg:"message"`
		Reply    string `pg:"reply"`
	}
	_, err = h.App.DB.QueryOne(&msg, `SELECT userid, user_type, message, reply FROM web_admin_messages WHERE id = ?0 AND from_admin = false`, req.MessageID)
	if err != nil {
		h.App.Log.Errorf("admin reply lookup error message_id=%d err=%v", req.MessageID, err)
	} else {
		h.sendWebPushToUserWithData(msg.UserID, msg.UserType, "Ответ администратора", msg.Reply, map[string]any{
			"kind":       "admin_reply",
			"message_id": req.MessageID,
			"reply":      msg.Reply,
			"message":    msg.Message,
		})
	}

	h.App.Log.Infof("admin replied message_id=%d by admin=%d", req.MessageID, adminID)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *WebHandler) AdminReplyAck(c *gin.Context) {
	var req struct {
		MessageID int64 `json:"message_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	if req.MessageID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message_id is required"})
		return
	}

	userID := c.MustGet("user_id").(int64)
	userType := c.MustGet("user_type").(int)

	res, err := h.App.DB.Exec(
		`UPDATE web_admin_messages SET read_at = now() WHERE id = ?0 AND userid = ?1 AND user_type = ?2 AND from_admin = false`,
		req.MessageID, userID, userType,
	)
	if err != nil {
		h.App.Log.Errorf("admin reply ack error message_id=%d user=%d type=%d err=%v", req.MessageID, userID, userType, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	if res.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "message not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *WebHandler) GetAdminMessages(c *gin.Context) {
	adminID := h.App.Config.Telegram.TelegramAdmin
	userID := c.MustGet("user_id").(int64)
	if adminID <= 0 || userID != adminID {
		c.JSON(http.StatusForbidden, gin.H{"error": "admin only"})
		return
	}

	offset := 0
	if v := c.Query("offset"); v != "" {
		fmt.Sscan(v, &offset)
	}
	hideAnswered := false
	if v := strings.ToLower(strings.TrimSpace(c.Query("hide_answered"))); v == "1" || v == "true" || v == "yes" {
		hideAnswered = true
	}

	type AdminMessageRecord struct {
		ID        int64      `pg:"id" json:"id"`
		UserID    int64      `pg:"userid" json:"user_id"`
		UserType  int        `pg:"user_type" json:"user_type"`
		Message   string     `pg:"message" json:"message"`
		Reply     *string    `pg:"reply" json:"reply"`
		FromAdmin bool       `pg:"from_admin" json:"from_admin"`
		CreatedAt time.Time  `pg:"created_at" json:"created_at"`
		RepliedAt *time.Time `pg:"replied_at" json:"replied_at"`
	}

	var messages []AdminMessageRecord
	query := `SELECT id, userid, user_type, message, reply, from_admin, created_at, replied_at
		   FROM web_admin_messages`
	if hideAnswered {
		query += ` WHERE replied_at IS NULL`
	}
	query += ` ORDER BY created_at DESC LIMIT 10 OFFSET ?0`
	_, err := h.App.DB.Query(&messages, query, offset)
	if err != nil {
		h.App.Log.Errorf("admin messages query error err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	if messages == nil {
		messages = []AdminMessageRecord{}
	}
	c.JSON(http.StatusOK, messages)
}

func (h *WebHandler) AdminSendMessage(c *gin.Context) {
	var req struct {
		UserID   int64  `json:"user_id"`
		UserType int    `json:"user_type"`
		Message  string `json:"message"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	msg := strings.TrimSpace(req.Message)
	if req.UserID <= 0 || msg == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id and message are required"})
		return
	}
	if len([]rune(msg)) > 1000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message too long"})
		return
	}

	adminID := h.App.Config.Telegram.TelegramAdmin
	userID := c.MustGet("user_id").(int64)
	if adminID <= 0 || userID != adminID {
		c.JSON(http.StatusForbidden, gin.H{"error": "admin only"})
		return
	}
	if !h.webPushConfigured() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "web push not configured"})
		return
	}

	var messageID int64
	_, err := h.App.DB.QueryOne(&messageID,
		`INSERT INTO web_admin_messages (userid, user_type, message, from_admin) VALUES (?0, ?1, ?2, true) RETURNING id`,
		req.UserID, req.UserType, msg,
	)
	if err != nil {
		h.App.Log.Errorf("admin send insert error admin=%d user=%d type=%d err=%v", adminID, req.UserID, req.UserType, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}

	h.sendWebPushToUserWithData(req.UserID, int32(req.UserType), "Сообщение администратора", msg, map[string]any{
		"kind":       "admin_message",
		"message_id": messageID,
		"message":    msg,
	})

	c.JSON(http.StatusOK, gin.H{"status": "ok", "id": messageID})
}

func (h *WebHandler) UserReplyAdminMessage(c *gin.Context) {
	var req struct {
		MessageID int64  `json:"message_id"`
		Reply     string `json:"reply"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	reply := strings.TrimSpace(req.Reply)
	if req.MessageID <= 0 || reply == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message_id and reply are required"})
		return
	}
	if len([]rune(reply)) > 1000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reply too long"})
		return
	}

	userID := c.MustGet("user_id").(int64)
	userType := c.MustGet("user_type").(int)

	res, err := h.App.DB.Exec(
		`UPDATE web_admin_messages SET reply = ?0, replied_at = now(), read_at = now()
		  WHERE id = ?1 AND userid = ?2 AND user_type = ?3 AND from_admin = true`,
		reply, req.MessageID, userID, userType,
	)
	if err != nil {
		h.App.Log.Errorf("user reply update error message_id=%d user=%d type=%d err=%v", req.MessageID, userID, userType, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	if res.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "message not found"})
		return
	}

	adminID := h.App.Config.Telegram.TelegramAdmin
	if adminID > 0 && h.webPushConfigured() {
		h.sendWebPushToUserAnyTypeWithData(adminID, "Ответ пользователю", reply, map[string]any{
			"kind":       "user_reply",
			"message_id": req.MessageID,
			"user_id":    userID,
			"user_type":  userType,
			"reply":      reply,
		})
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
