package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Сохранение фаворита
func (h *WebHandler) SetFavorite(c *gin.Context) {
	userId := c.MustGet("user_id").(int64)
	var req struct {
		VMID int `json:"vmid"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		return
	}
	// cl := new(client)
	_, err := h.App.DB.Exec(
		"UPDATE tg_user SET defaultrobot = ?0 WHERE userid = ?1",
		req.VMID, userId,
	)
	if err != nil {
		h.App.Log.Errf("change default robot error:%v", err)
		c.JSON(http.StatusOK, gin.H{"status": "Falce"})
		return
	}

	// Твой SQL: UPDATE tg_user SET favorite_machine_id = $1 WHERE userid = $2
	_ = userId
	// err := h.App.SetFavoriteMachine(userId, req.MachineID)

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *WebHandler) GetBalance(c *gin.Context) {
	userId := c.MustGet("user_id").(int64)
	cl, err := h.App.ClientGet(userId)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"user_id":   userId,
		"user_name": cl.Name,
		"balance":   float64(cl.Balance) / 100, //баланс в копейках
		// "favorite_machine_name": "user.MachineName", // Название из таблицы автоматов.
		"vm_id":    cl.Defaultrobot,
		"discount": cl.Diskont, // например, 10 (значит 10%)
		"credit":   cl.Credit,  // лимит в рублях
	})
}
