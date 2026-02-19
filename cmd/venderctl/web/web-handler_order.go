package web

import (
	"fmt"
	"time"

	vender_api "github.com/AlexTransit/vender/tele"
	"github.com/gin-gonic/gin"
)

// Получаем ТОП-5 популярных напитков из истории заказов
func (h *WebHandler) GetPopular(c *gin.Context) {
	type PopularDrink struct {
		Drink int `json:"drink"`
		Cream int `json:"cream"`
		Sugar int `json:"sugar"`
	}

	var drinks []PopularDrink
	db := h.App.DB.Conn()
	defer db.Close()

	// Группируем по трем параметрам сразу
	query := `SELECT drink_id as drink, cream, sugar 
              FROM order_log 
              GROUP BY drink_id, cream, sugar 
              ORDER BY COUNT(*) DESC LIMIT 5`

	_, err := h.App.DB.Query(&drinks, query)

	// Если логов нет, даем стандартные "вкусные" пресеты
	if err != nil || len(drinks) == 0 {
		drinks = []PopularDrink{
			{55, 4, 4}, {43, 4, 4}, {6, 2, 6}, {6, 6, 4}, {6, 6, 6},
		}
	}
	c.JSON(200, drinks)
}

func (h *WebHandler) MakeOrder(c *gin.Context) {
	userId := c.MustGet("user_id").(int64)
	var req struct {
		VMID  int `json:"vmid"`
		Drink int `json:"drink"`
		Cream int `json:"cream"`
		Sugar int `json:"sugar"`
	}
	c.ShouldBindJSON(&req)

	// ВАЛИДАЦИЯ (как ты просил)
	if req.Cream < 0 || req.Cream > 6 || req.Sugar < 0 || req.Sugar > 8 {
		c.JSON(400, gin.H{"error": "Неверные параметры добавок"})
		return
	}

	// 1. Списываем баланс (нужен SQL запрос цены по vmid и drink)
	// 2. Формируем строку команды
	cmd := fmt.Sprintf("/%d_%dc%ds%d", req.VMID, req.Drink, req.Cream, req.Sugar)

	h.App.Log.Infof("ПОЛЬЗОВАТЕЛЬ %d ЗАКАЗАЛ: %s", userId, cmd)

	// 3. Отправляем в MQTT (топик vmc/ID/cmd)
	// h.App.Mqtt.Publish(...)

	c.JSON(200, gin.H{"status": "ok"})
}

func (h *WebHandler) CheckOrder(c *gin.Context) {
	userId := c.MustGet("user_id").(int64)

	var req struct {
		VMID  int `json:"vmid"`
		Drink int `json:"drink"`
		Cream int `json:"cream"`
		Sugar int `json:"sugar"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "invalid json"})
		return
	}

	vmid := int32(req.VMID)

	// 1. Проверяем доступность автомата
	ok, text := h.App.СheckRobotMakeState(vmid)
	if !ok {
		c.JSON(400, gin.H{"ошибка": text})
		return
	}

	// 2. Получаем клиента
	cl, err := h.App.ClientGet(userId)
	if err != nil {
		c.JSON(500, gin.H{"error": "db error"})
		return
	}

	_ = cl
	// 3. Формируем команду проверки напитка
	trm := vender_api.ToRoboMessage{
		ServerTime: time.Now().Unix(),
		Cmd:        vender_api.MessageType_makeOrder,
		MakeOrder: &vender_api.Order{
			// MenuCode:      req.Drink,
			Amount:        cl.Credit + uint32(cl.Balance),
			OrderStatus:   vender_api.OrderStatus_doTransferred,
			PaymentMethod: vender_api.PaymentMethod_Balance,
			OwnerInt:      userId,
			OwnerType:     3, //FIXME надо сделать тип WEB
			Cream:         encodeTuning(req.Cream),
			Sugar:         encodeTuning(req.Sugar),
		},
	}

	// 4. Отправляем команду автомату и ждём ответ через MQTT
	h.App.Tele.SendToRobo(vmid, &trm)

	// price, err := h.App.RobotCheckDrink(vmid, checkCmd)
	// if err != nil {
	// 	c.JSON(400, gin.H{"error": err.Error()})
	// 	return
	// }

	// // 5. Проверяем баланс
	// if int64(price) > cl.Balance+cl.Credit {
	// 	c.JSON(400, gin.H{"error": "Недостаточно средств"})
	// 	return
	// }

	// // 6. Получаем название напитка
	// drinkName := h.App.GetDrinkName(req.Drink)

	// // 7. Возвращаем данные для подтверждения
	// c.JSON(200, gin.H{
	// 	"vmid":       req.VMID,
	// 	"drink":      req.Drink,
	// 	"drink_name": drinkName,
	// 	"cream":      req.Cream,
	// 	"sugar":      req.Sugar,
	// 	"price":      price,
	// 	"can_make":   true,
	// })
}

func encodeTuning(v int) []byte {
	if v == 4 {
		return []byte{0}
	}
	v++
	return []byte{uint8(v)}
}
