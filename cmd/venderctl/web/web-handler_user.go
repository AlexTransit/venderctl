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
			`SELECT vmid
			 FROM robot
			 WHERE lat IS NOT NULL AND lon IS NOT NULL
			 ORDER BY ((lat - ?0) * (lat - ?0) + (lon - ?1) * (lon - ?1)) ASC
			 LIMIT 1`,
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

	c.JSON(http.StatusOK, gin.H{
		"user_id":   userId,
		"user_name": cl.Name,
		"balance":   float64(cl.Balance) / 100, // баланс в рублях
		"vm_id":     cl.Defaultrobot,
		"discount":  cl.Diskont, // например, 10 (значит 10%)
		"credit":    float64(cl.Credit) / 100, // кредит в рублях
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
