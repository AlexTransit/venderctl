package web

import (
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type MachineStatusEvent struct {
	Vmid    int32 `json:"vmid"`
	Connect bool  `json:"connect"`
	State   int32 `json:"state"`
}

type MachineStatusBus struct {
	mu       sync.RWMutex
	channels map[int32][]chan MachineStatusEvent
}

func NewMachineStatusBus() *MachineStatusBus {
	return &MachineStatusBus{
		channels: make(map[int32][]chan MachineStatusEvent),
	}
}

func (b *MachineStatusBus) Subscribe(vmid int32) chan MachineStatusEvent {
	ch := make(chan MachineStatusEvent, 4)
	b.mu.Lock()
	b.channels[vmid] = append(b.channels[vmid], ch)
	b.mu.Unlock()
	return ch
}

func (b *MachineStatusBus) Unsubscribe(vmid int32, ch chan MachineStatusEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	arr := b.channels[vmid]
	for i, c := range arr {
		if c == ch {
			b.channels[vmid] = append(arr[:i], arr[i+1:]...)
			close(c)
			break
		}
	}
}

func (b *MachineStatusBus) Publish(ev MachineStatusEvent) {
	b.mu.RLock()
	arr := b.channels[ev.Vmid]
	b.mu.RUnlock()
	for _, ch := range arr {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (h *WebHandler) MachineStatusWS(c *gin.Context) {
	vmidStr := c.Query("vmid")
	vmid64, err := strconv.ParseInt(vmidStr, 10, 32)
	if err != nil || vmid64 <= 0 {
		c.JSON(400, gin.H{"error": "bad vmid"})
		return
	}
	vmid := int32(vmid64)

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ch := h.MachineStatus.Subscribe(vmid)
	defer h.MachineStatus.Unsubscribe(vmid, ch)

	// Send current snapshot immediately so the bar updates without waiting for next MQTT tick.
	snap := MachineStatusEvent{
		Vmid:    vmid,
		Connect: h.App.RobotConnected(vmid),
		State:   int32(h.App.GetRoboState(vmid)),
	}
	if err := conn.WriteJSON(snap); err != nil {
		return
	}

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
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if err := conn.WriteJSON(ev); err != nil {
				return
			}
		case <-ticker.C:
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

