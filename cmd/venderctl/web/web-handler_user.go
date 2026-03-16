package web

import (
	"fmt"
	"net/http"
	"time"

	vender_api "github.com/AlexTransit/vender/tele"
	"github.com/gin-gonic/gin"
)

// Сохранение фаворита
func (h *WebHandler) SetFavorite(c *gin.Context) {
	userId := c.MustGet("user_id").(int64)
	userType := c.MustGet("user_type").(int)
	var req struct {
		VMID int      `json:"vmid"`
		Lat  *float64 `json:"lat"`
		Lon  *float64 `json:"lon"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	vmid := req.VMID
	if vmid == 0 {
		if req.Lat == nil || req.Lon == nil {
			c.JSON(http.StatusOK, gin.H{"status": "need_machine_select"})
			return
		}

		var nearestVMID int
		_, err := h.App.DB.QueryOne(&nearestVMID,
			`SELECT vmid FROM public.robot WHERE sqrt(pow((geolocation->'lat')::float - ?0, 2) + pow((geolocation->'lon')::float - ?1, 2)) < 1.06 LIMIT 1;`,
			*req.Lat, *req.Lon)
		if err != nil || nearestVMID <= 0 {
			h.App.Log.Errorf("auto favorite machine detect error userId=%d err=%v", userId, err)
			c.JSON(http.StatusOK, gin.H{"status": "need_machine_select"})
			return
		}
		vmid = nearestVMID
	}

	_, err := h.App.DB.Exec(
		"UPDATE users SET defaultrobot = ?0 WHERE userid = ?1 AND user_type = ?2",
		vmid, userId, userType,
	)
	if err != nil {
		h.App.Log.Errorf("change default robot error:%v", err)
		c.JSON(http.StatusOK, gin.H{"status": "false"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "vmid": vmid})
}

func (h *WebHandler) GetBalance(c *gin.Context) {
	userId := c.MustGet("user_id").(int64)
	userType := c.MustGet("user_type").(int)

	cl, err := h.App.ClientGet(userId, vender_api.OwnerType(userType))
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}

	var adminReply struct {
		ID      int64  `pg:"id"`
		Message string `pg:"message"`
		Reply   string `pg:"reply"`
	}
	_, _ = h.App.DB.QueryOne(&adminReply,
		`SELECT id, message, reply FROM web_admin_messages
		  WHERE userid = ?0 AND user_type = ?1 AND from_admin = false AND reply IS NOT NULL AND replied_at IS NOT NULL AND read_at IS NULL
		  ORDER BY replied_at DESC LIMIT 1`,
		userId, userType,
	)

	c.JSON(http.StatusOK, gin.H{
		"user_id":   userId,
		"user_name": cl.Name,
		"balance":   float64(cl.Balance) / 100, // баланс в рублях
		"vm_id":     cl.Defaultrobot,
		"discount":  cl.Diskont,               // например, 10 (значит 10%)
		"credit":    float64(cl.Credit) / 100, // кредит в рублях
		"is_admin":  userId == h.App.Config.Telegram.TelegramAdmin,
		"admin_reply": func() gin.H {
			if adminReply.ID == 0 || adminReply.Reply == "" {
				return gin.H{}
			}
			return gin.H{
				"id":      adminReply.ID,
				"message": adminReply.Message,
				"reply":   adminReply.Reply,
			}
		}(),
		"admin_message": func() gin.H {
			var adminMsg struct {
				ID      int64  `pg:"id"`
				Message string `pg:"message"`
			}
			_, _ = h.App.DB.QueryOne(&adminMsg,
				`SELECT id, message FROM web_admin_messages
				  WHERE userid = ?0 AND user_type = ?1 AND from_admin = true AND reply IS NULL AND read_at IS NULL
				  ORDER BY created_at DESC LIMIT 1`,
				userId, userType,
			)
			if adminMsg.ID == 0 || adminMsg.Message == "" {
				return gin.H{}
			}
			return gin.H{
				"id":      adminMsg.ID,
				"message": adminMsg.Message,
			}
		}(),
	})
}

func (h *WebHandler) GetOrders(c *gin.Context) {
	userId := c.MustGet("user_id").(int64)
	userType := c.MustGet("user_type").(int)

	offset := 0
	if v := c.Query("offset"); v != "" {
		fmt.Sscan(v, &offset)
	}

	type OrderRecord struct {
		Date        time.Time `pg:"date" json:"date"`
		Action      string    `pg:"action" json:"action"`
		BalanceInfo float64   `pg:"balance_info" json:"balance_info"`
	}

	var orders []OrderRecord
	_, err := h.App.DB.Query(&orders,
		`SELECT date, action, balance_info FROM user_orders 
         WHERE userid = ?0 AND user_type = ?1 
         ORDER BY date DESC LIMIT 10 OFFSET ?2`,
		userId, userType, offset)
	if err != nil {
		c.JSON(500, gin.H{"error": "db error"})
		return
	}
	if orders == nil {
		orders = []OrderRecord{}
	}
	c.JSON(200, orders)
}
