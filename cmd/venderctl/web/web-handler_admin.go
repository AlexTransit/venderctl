package web

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/AlexTransit/vender/tele"
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

	// Get user name and memo from database
	var user struct {
		Name string `pg:"name"`
		Memo string `pg:"memo"`
	}
	_, err := h.App.DB.QueryOne(&user, `SELECT name, memo FROM users WHERE userid = ?0 and user_type = ?1`, userID, userType)
	if err != nil {
		h.App.Log.Errorf("failed to get user info user_id=%d err=%v", userID, err)
		user.Name = fmt.Sprintf("User %d", userID)
		user.Memo = ""
	}

	title := "Сообщение администратору"
	body := fmt.Sprintf("от %s (%s): %s", user.Name, user.Memo, msg)
	var messageID int64
	_, err = h.App.DB.QueryOne(&messageID,
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
		"url":         h.App.Config.WebRootPath() + fmt.Sprintf("?open_user=%d&open_user_type=%d", userID, userType),
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
		Reply     string `json:"reply"`
		MessageID int64  `json:"message_id"`
		UserID    int64  `json:"user_id"`
		UserType  int    `json:"user_type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	if req.MessageID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message_id is required"})
		return
	}
	reply := strings.TrimSpace(req.Reply)
	// пустой ответ — юзер закрыл модалку не отвечая, пишем только read_at
	if reply == "" {
		_, _ = h.App.DB.Exec(
			`UPDATE web_admin_messages SET read_at = now() WHERE id = ?0 AND from_admin = true`,
			req.MessageID,
		)
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
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

	h.App.AdminReplayAutoAction(&reply, req.UserID, tele.OwnerType(req.UserType))

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

	var origMessage string
	_, _ = h.App.DB.QueryOne(&origMessage,
		`SELECT message FROM web_admin_messages WHERE id = ?0`,
		req.MessageID,
	)

	cl, _ := h.App.ClientGet(req.UserID, tele.OwnerType(req.UserType))
	h.sendWebPushToUserWithData(cl.Id, int32(cl.ClientType), "Ответ администратора", reply, map[string]any{
		"kind":       "admin_reply",
		"message_id": req.MessageID,
		"reply":      reply,
		"message":    origMessage,
	})

	h.App.LogUserOrder("Admin ", cl.Id, int32(cl.ClientType), reply, cl.Balance)

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
	hideRead := false
	if v := strings.ToLower(strings.TrimSpace(c.Query("hide_read"))); v == "1" || v == "true" || v == "yes" {
		hideRead = true
	}
	var filterUserID int64
	var filterUserType int
	fmt.Sscan(c.Query("user_id"), &filterUserID)
	fmt.Sscan(c.Query("user_type"), &filterUserType)

	type AdminMessageRecord struct {
		ID        int64      `pg:"id" json:"id"`
		UserID    int64      `pg:"userid" json:"user_id"`
		UserType  int        `pg:"user_type" json:"user_type"`
		Name      string     `pg:"name" json:"name"`
		Message   string     `pg:"message" json:"message"`
		Reply     *string    `pg:"reply" json:"reply"`
		FromAdmin bool       `pg:"from_admin" json:"from_admin"`
		CreatedAt time.Time  `pg:"created_at" json:"created_at"`
		RepliedAt *time.Time `pg:"replied_at" json:"replied_at"`
	}

	conditions := []string{}
	if !hideAnswered {
		conditions = append(conditions, "replied_at IS NULL")
	}
	if hideRead && filterUserID > 0 {
		conditions = append(conditions, "read_at IS NULL")
	}
	if filterUserID > 0 {
		conditions = append(conditions, fmt.Sprintf("web_admin_messages.userid = %d AND web_admin_messages.user_type = %d", filterUserID, filterUserType))
	}

	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}

	var messages []AdminMessageRecord
	query := `SELECT web_admin_messages.id, web_admin_messages.userid, web_admin_messages.user_type,
		   concat(users.name,' (',users.memo,')') as name, message, reply, from_admin, created_at, replied_at
		   FROM web_admin_messages
		   JOIN users ON (users.userid = web_admin_messages.userid) AND (users.user_type = web_admin_messages.user_type)` +
		where + ` ORDER BY created_at DESC LIMIT 10 OFFSET ?0`

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

	h.App.AdminReplayAutoAction(&msg, req.UserID, tele.OwnerType(req.UserType))

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

	cl, _ := h.App.ClientGet(req.UserID, tele.OwnerType(req.UserType))
	h.App.LogUserOrder("Admin ", cl.Id, int32(cl.ClientType), msg, cl.Balance)

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
	if req.MessageID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message_id is required"})
		return
	}
	reply := strings.TrimSpace(req.Reply)
	// пустой ответ — юзер закрыл модалку не отвечая, пишем только read_at
	if reply == "" {
		_, _ = h.App.DB.Exec(
			`UPDATE web_admin_messages SET read_at = now() WHERE id = ?0 AND from_admin = true`,
			req.MessageID,
		)
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
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
			"kind":        "user_reply",
			"message_id":  req.MessageID,
			"sender_id":   userID,
			"sender_type": userType,
			"reply":       reply,
		})
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *WebHandler) AdminRenameUser(c *gin.Context) {
	adminID := h.App.Config.Telegram.TelegramAdmin
	userID := c.MustGet("user_id").(int64)
	if adminID <= 0 || userID != adminID {
		c.JSON(http.StatusForbidden, gin.H{"error": "admin only"})
		return
	}
	var req struct {
		UserID   int64  `json:"user_id"`
		UserType int    `json:"user_type"`
		Name     string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	name := strings.TrimSpace(req.Name)
	if req.UserID <= 0 || name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id and name are required"})
		return
	}
	_, err := h.App.DB.Exec(
		`UPDATE users SET name = ?0 WHERE userid = ?1 AND user_type = ?2`,
		name, req.UserID, req.UserType,
	)
	if err != nil {
		h.App.Log.Errorf("rename user error user=%d type=%d err=%v", req.UserID, req.UserType, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *WebHandler) AdminMemoUser(c *gin.Context) {
	adminID := h.App.Config.Telegram.TelegramAdmin
	userID := c.MustGet("user_id").(int64)
	if adminID <= 0 || userID != adminID {
		c.JSON(http.StatusForbidden, gin.H{"error": "admin only"})
		return
	}
	var req struct {
		UserID   int64  `json:"user_id"`
		UserType int    `json:"user_type"`
		Memo     string `json:"memo"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	if req.UserID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id is required"})
		return
	}
	_, err := h.App.DB.Exec(
		`UPDATE users SET memo = ?0 WHERE userid = ?1 AND user_type = ?2`,
		strings.TrimSpace(req.Memo), req.UserID, req.UserType,
	)
	if err != nil {
		h.App.Log.Errorf("memo user error user=%d type=%d err=%v", req.UserID, req.UserType, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *WebHandler) AdminInviteUser(c *gin.Context) {
	adminID := h.App.Config.Telegram.TelegramAdmin
	userID := c.MustGet("user_id").(int64)
	if adminID <= 0 || userID != adminID {
		c.JSON(http.StatusForbidden, gin.H{"error": "admin only"})
		return
	}
	var req struct {
		UserID   int64 `json:"user_id"`
		UserType int   `json:"user_type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	if req.UserID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id is required"})
		return
	}
	token, err := h.App.CreateWebAuthToken(req.UserID, req.UserType)
	if err != nil {
		h.App.Log.Errorf("create invite token error user=%d type=%d err=%v", req.UserID, req.UserType, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	origin, _ := h.App.Config.ParseWebURL()
	inviteURL := origin + h.App.Config.WebPathWithPrefix("/open") + "?token=" + token
	c.JSON(http.StatusOK, gin.H{"status": "ok", "url": inviteURL})
}

func (h *WebHandler) AdminGetUsers(c *gin.Context) {
	adminID := h.App.Config.Telegram.TelegramAdmin
	userID := c.MustGet("user_id").(int64)
	if adminID <= 0 || userID != adminID {
		c.JSON(http.StatusForbidden, gin.H{"error": "admin only"})
		return
	}
	type UserRecord struct {
		UserID   int64  `pg:"userid" json:"user_id"`
		UserType int    `pg:"user_type" json:"user_type"`
		Name     string `pg:"name" json:"name"`
		Memo     string `pg:"memo" json:"memo"`
		Phone    string `pg:"phone" json:"phone"`
	}
	var users []UserRecord
	_, err := h.App.DB.Query(&users,
		`SELECT userid, user_type, name, memo, phone as phone FROM users ORDER BY name ASC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	if users == nil {
		users = []UserRecord{}
	}
	c.JSON(http.StatusOK, users)
}
