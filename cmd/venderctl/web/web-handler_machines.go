package web

// import (
// 	"net/http"

// 	"github.com/gin-gonic/gin"
// )

// func (h *WebHandler) GetMachines(c *gin.Context) {
// 	// machines := []gin.H{
// 	// 	{"id": 1, "name": "Автомат на Ленина", "lat": 55.123, "lon": 37.456},
// 	// 	{"id": 2, "name": "Автомат в ТЦ", "lat": 55.789, "lon": 37.012},
// 	// }

// 	var machines []struct {
// 		Vmid  int `pg:"vmid" json:"id"`
// 		Vmnum int `pg:"vmnum" json:"vmnumber"`
// 	}

// 	query := `SELECT vmid, vmnum FROM robot ORDER BY vmid ASC;`

// 	_, err := h.App.DB.Query(&machines, query)
// 	if err != nil {
// 		h.App.Log.Errorf("Ошибка получения списка роботов: %v", err)
// 		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
// 		return
// 	}

// 	c.JSON(http.StatusOK, machines)
// }
