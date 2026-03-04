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

	var drinks []PopularDrink

	query := `SELECT menu_code as drink, (CASE WHEN options[1] = 0 THEN 4 ELSE options[1] - 1 END) as cream, (CASE WHEN options[2] = 0 THEN 4 ELSE options[2] - 1 END) as sugar FROM trans WHERE executer = ?0 GROUP BY 1,2,3 ORDER BY COUNT(*) DESC LIMIT 5`

	_, err := h.App.DB.Query(&drinks, query, userId)

	if err != nil || len(drinks) == 0 {
		drinks = []PopularDrink{
			{"00", 4, 4}, {"00.", 4, 4}, {"6", 2, 6}, {"6", 6, 4}, {"6", 6, 6},
		}
	}
	c.JSON(200, drinks)
}

func (h *WebHandler) MakeOrder(c *gin.Context) {
	userId := c.MustGet("user_id").(int64)
	var req struct {
		VMID  int    `json:"vmid"`
		Drink string `json:"drink"`
		Cream int    `json:"cream"`
		Sugar int    `json:"sugar"`
	}
	c.ShouldBindJSON(&req)

	// ВАЛИДАЦИЯ (как ты просил)
	if req.Cream < 0 || req.Cream > 6 || req.Sugar < 0 || req.Sugar > 8 {
		c.JSON(400, gin.H{"error": "Неверные параметры добавок"})
		return
	}

	// 1. Списываем баланс (нужен SQL запрос цены по vmid и drink)
	// 2. Формируем строку команды
	cmd := fmt.Sprintf("/%d_%sc%ds%d", req.VMID, req.Drink, req.Cream, req.Sugar)

	h.App.Log.Infof("ПОЛЬЗОВАТЕЛЬ %d ЗАКАЗАЛ: %s", userId, cmd)

	// 3. Отправляем в MQTT (топик vmc/ID/cmd)
	// h.App.Mqtt.Publish(...)

	c.JSON(200, gin.H{"status": "ok"})
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

	cl, err := h.App.ClientGet(userId, int32(userType))
	if err != nil {
		c.JSON(500, gin.H{"error": "db error"})
		return
	}

	// Проверка баланса
	if int32(cl.Balance)+int32(cl.Credit*100) <= 0 {
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
	cl, err1 := h.App.ClientGet(userId, int32(userType))
	if err1 != nil {
		c.JSON(500, gin.H{"error": "db error"})
		return
	}
	available := cl.Balance + int64(cl.Credit)*100
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
			// OwnerType:     vender_api.OwnerType_webUser,
			OwnerType: vender_api.OwnerType(userType),
			Cream:     encodeTuning(req.Cream),
			Sugar:     encodeTuning(req.Sugar),
		},
	}

	// 4. Отправляем команду автомату и ждём ответ через MQTT
	h.App.Tele.SendToRobo(vmid, &trm)

	action := fmt.Sprintf("заказ автомат №%d код:%s сливки:%d сахар:%d",
		req.VMID, req.DrinkCode, req.Cream, req.Sugar)
	h.logOrder(userId, int32(userType), action)

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
func (h *WebHandler) logOrder(userId int64, userType int32, action string) {
	_, err := h.App.DB.Exec(
		`INSERT INTO user_orders (userid, user_type, action) VALUES (?0, ?1, ?2)`,
		userId, userType, action)
	if err != nil {
		h.App.Log.Errorf("logOrder userId=%d err=%v", userId, err)
	}
}

// // creditCashbackToDb начисляет кэшбек по скидке клиента. Не начисляет если diskont = 0.
// func (h *WebHandler) creditCashbackToDb(userId int64, price uint32) uint32 {
// 	const q = `UPDATE tg_user SET balance = balance + ?1 * diskont / 100 WHERE userid = ?0 AND diskont > 0 RETURNING diskont;`
// 	var diskont int
// 	h.App.Alive.Add(1)
// 	_, err := h.App.DB.QueryOne(&diskont, q, userId, price)
// 	h.App.Alive.Done()
// 	if err != nil {
// 		// pg.ErrNoRows — скидки нет, это нормально
// 		return 0
// 	}
// 	cashback := price * uint32(diskont) / 100
// 	h.App.Log.Infof("кэшбек userId=%d сумма=%d diskont=%d%%", userId, cashback, diskont)
// 	return cashback
// }
