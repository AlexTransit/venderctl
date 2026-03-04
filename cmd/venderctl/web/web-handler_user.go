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
		VMID int `json:"vmid"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		return
	}
	var table string
	switch vender_api.OwnerType(userType) {
	case vender_api.OwnerType_webUser:
		table = "web_user"
	case vender_api.OwnerType_telegramUser:
		table = "tg_user"
	}

	_, err := h.App.DB.Exec(
		fmt.Sprintf("UPDATE %s SET defaultrobot = ?0 WHERE userid = ?1", table),
		req.VMID, userId,
	)

	if err != nil {
		h.App.Log.Errorf("change default robot error:%v", err)
		c.JSON(http.StatusOK, gin.H{"status": "Falce"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *WebHandler) GetBalance(c *gin.Context) {
	userId := c.MustGet("user_id").(int64)
	userType := c.MustGet("user_type").(int)

	cl, err := h.App.ClientGet(userId, int32(userType))

	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"user_id":   userId,
		"user_name": cl.Name,
		"balance":   float64(cl.Balance) / 100, //баланс в копейках
		"vm_id":     cl.Defaultrobot,
		"discount":  cl.Diskont, // например, 10 (значит 10%)
		"credit":    cl.Credit,  // кредит в рублях
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
		Date   time.Time `pg:"date" json:"date"`
		Action string    `pg:"action" json:"action"`
	}

	var orders []OrderRecord
	_, err := h.App.DB.Query(&orders,
		`SELECT date, action FROM user_orders 
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
