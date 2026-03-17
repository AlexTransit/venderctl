package web

import (
	"fmt"
	"math"
	"time"

	vender_api "github.com/AlexTransit/vender/tele"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// Получаем ТОП-5 популярных напитков из истории заказов текущего пользователя
func (h *WebHandler) GetPopular(c *gin.Context) {
	type PopularDrink struct {
		Drink string `json:"drink"`
		Cream int    `json:"cream"`
		Sugar int    `json:"sugar"`
	}

	userId := c.MustGet("user_id").(int64)
	userType := c.MustGet("user_type").(int)

	var drinks []PopularDrink

	query := `SELECT menu_code as drink, (CASE WHEN options[1] = 0 THEN 4 ELSE options[1] - 1 END) as cream, (CASE WHEN options[2] = 0 THEN 4 ELSE options[2] - 1 END) as sugar FROM trans WHERE executer = ?0 AND (executer_type = ?1 or executer_type = -?1)  GROUP BY 1,2,3 ORDER BY COUNT(*) DESC LIMIT 5`

	h.App.DB.Query(&drinks, query, userId, userType)

	c.JSON(200, drinks)
}

func encodeTuning(v int) []byte {
	if v == 4 {
		return []byte{0}
	}
	v++
	return []byte{uint8(v)}
}

func (h *WebHandler) PreviewOrder(c *gin.Context) {
	userId := c.MustGet("user_id").(int64)
	userType := c.MustGet("user_type").(int)

	var req struct {
		VMID      int    `json:"vmid"`
		DrinkCode string `json:"drink"`
		Cream     int    `json:"cream"`
		Sugar     int    `json:"sugar"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "invalid json"})
		return
	}

	cl, err := h.App.ClientGet(userId, vender_api.OwnerType(userType))
	if err != nil {
		c.JSON(500, gin.H{"error": "db error"})
		return
	}

	// Проверка баланса
	if int32(cl.Balance)+int32(cl.Credit) <= 0 {
		c.JSON(400, gin.H{"error": "недостаточно средств"})
		return
	}

	vmid := int32(req.VMID)

	ok, err1 := h.App.СheckRobotMakeState(vmid)
	if !ok {
		c.JSON(400, gin.H{"error": err1})
		return
	}

	drinkName := h.App.GetDrinkName(vmid, req.DrinkCode)

	c.JSON(200, gin.H{
		"userId":     userId,
		"vmid":       req.VMID,
		"drink":      req.DrinkCode,
		"drink_name": drinkName,
		"cream":      req.Cream,
		"sugar":      req.Sugar,
	})
}

func (h *WebHandler) StartOrder(c *gin.Context) {
	userId := c.MustGet("user_id").(int64)
	userType := c.MustGet("user_type").(int)

	var req struct {
		VMID      int    `json:"vmid"`
		DrinkCode string `json:"drink_code"`
		Cream     int    `json:"cream"`
		Sugar     int    `json:"sugar"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "invalid json"})
		return
	}
	if req.DrinkCode == "" {
		c.JSON(400, gin.H{"error": "drink_code is required"})
		return
	}
	if req.Cream < 0 || req.Cream > 6 || req.Sugar < 0 || req.Sugar > 8 {
		c.JSON(400, gin.H{"error": "Неверные параметры добавок"})
		return
	}

	vmid := int32(req.VMID)

	ok, errText := h.App.СheckRobotMakeState(vmid)
	if !ok {
		c.JSON(400, gin.H{"error": errText})
		return
	}
	cl, err1 := h.App.ClientGet(userId, vender_api.OwnerType(userType))
	if err1 != nil {
		c.JSON(500, gin.H{"error": "db error"})
		return
	}
	available := cl.Balance + int64(cl.Credit)
	if available <= 0 {
		c.JSON(400, gin.H{"error": "недостаточно средств"})
		return
	}
	if available > math.MaxUint32 {
		c.JSON(500, gin.H{"error": "balance overflow"})
		return
	}

	trm := vender_api.ToRoboMessage{
		ServerTime: time.Now().Unix(),
		Cmd:        vender_api.MessageType_makeOrder,
		MakeOrder: &vender_api.Order{
			MenuCode:      req.DrinkCode,
			Amount:        uint32(available),
			OrderStatus:   vender_api.OrderStatus_doTransferred,
			PaymentMethod: vender_api.PaymentMethod_Balance,
			OwnerInt:      userId,
			OwnerType:     vender_api.OwnerType(-userType),
			Cream:         encodeTuning(req.Cream),
			Sugar:         encodeTuning(req.Sugar),
		},
	}

	// 4. Отправляем команду автомату и ждём ответ через MQTT
	h.App.Tele.SendToRobo(vmid, &trm)

	action := fmt.Sprintf("заказ: робот №%d код:%s сливки:%d сахар:%d",
		req.VMID, req.DrinkCode, req.Cream, req.Sugar)
	h.App.LogUserOrder("Web", userId, int32(userType), action, cl.Balance)

	c.JSON(200, gin.H{
		"status":    "sent",
		"vmid":      req.VMID,
		"drinkcode": req.DrinkCode,
	})
}

func (h *WebHandler) OrderWS(c *gin.Context) {
	userId := c.MustGet("user_id").(int64)
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	userType := int32(c.MustGet("user_type").(int))
	ch := h.OrderEvents.Subscribe(userId, userType)
	defer h.OrderEvents.Unsubscribe(userId, userType, ch)

	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case event, ok := <-ch:
			if !ok {
				return
			}
			h.App.Log.Infof("OrderWS WriteJSON userId=%d event=%v", userId, event)
			if err := conn.WriteJSON(event); err != nil {
				return
			}
		case <-ticker.C:
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
