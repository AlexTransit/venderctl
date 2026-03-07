package web

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v9"
)

type userRecord struct {
	Name string `pg:"name"`
	Id   int64  `pg:"userid"`
	Type int32  `pg:"user_type"`
}

func (h *WebHandler) HandleAuth(c *gin.Context) {
	oneTimeToken := c.Query("token")

	if oneTimeToken == "" {
		c.String(400, "Для входа нужна ссылка-приглашение с токеном")
		return
	}

	var rec struct {
		Userid    int64     `pg:"userid"`
		UserType  int32     `pg:"user_type"`
		Used      bool      `pg:"used"`
		CreatedAt time.Time `pg:"created_at"`
	}
	_, err := h.App.DB.QueryOne(&rec,
		`SELECT userid, user_type, used, created_at FROM web_auth_tokens WHERE token = ?`,
		oneTimeToken)
	if err != nil {
		c.String(403, "Ссылка недействительна")
		return
	}

	// Если токен уже использован — проверяем есть ли сессия
		if rec.Used {
			var existingToken string
			_, err = h.App.DB.QueryOne(pg.Scan(&existingToken),
				"SELECT token FROM user_sessions WHERE userid = ?0 AND user_type = ?1 AND approved = true LIMIT 1",
				rec.Userid, rec.UserType)
			if err == nil && existingToken != "" {
				h.setAuthCookie(c, rec.Userid, existingToken)
				c.Redirect(302, "/")
			return
		}
		c.String(403, "Ссылка уже использована")
		return
	}
	if time.Since(rec.CreatedAt) > 5*time.Minute {
		c.String(403, "Ссылка устарела")
		return
	}
	if rec.Used {
		c.String(403, "Ссылка уже использована")
		return
	}

	// Атомарно помечаем токен как использованный, чтобы исключить повторное применение.
	res, err := h.App.DB.Exec(`UPDATE web_auth_tokens SET used = true WHERE token = ? AND used = false`, oneTimeToken)
	if err != nil {
		c.String(500, "session error")
		return
	}
	if res.RowsAffected() != 1 {
		c.String(403, "Ссылка уже использована")
		return
	}

	// Проверяем не создана ли уже сессия для этого пользователя
	var existingSession string
	_, err = h.App.DB.QueryOne(pg.Scan(&existingSession),
		"SELECT token FROM user_sessions WHERE userid = ?0 AND user_type = ?1 AND approved = true LIMIT 1",
		rec.Userid, rec.UserType)
	if err == nil && existingSession != "" {
		// Сессия уже есть — просто выдаём cookie
		h.setAuthCookie(c, rec.Userid, existingSession)
		c.Redirect(302, "/")
		return
	}

	user := userRecord{Id: rec.Userid, Type: int32(rec.UserType)}
	approved, err := h.createSession(c, &user)
	if err != nil {
		c.String(500, "session error")
		return
	}
	if !approved {
		c.String(200, "Ожидайте подтверждения администратора. Можете закрыть эту страницу.")
		return
	}
	c.Redirect(302, "/")
}

func (h *WebHandler) Logout(c *gin.Context) {
	c.SetCookie("auth_user_id", "", -1, "/", "", false, true)
	c.Redirect(http.StatusFound, "/")
}

func (h *WebHandler) createSession(c *gin.Context, user *userRecord) (bool, error) {
	// Проверяем есть ли уже одобренные сессии
	var count int
	_, err := h.App.DB.QueryOne(pg.Scan(&count),
		"SELECT COUNT(*) FROM user_sessions WHERE userid = ? AND approved = true", user.Id)
	if err != nil {
		fmt.Printf("\033[41m %v \033[0m\n", err)
		return false, err
	}

	// Генерируем токен
	b := make([]byte, 16)
	rand.Read(b)
	token := hex.EncodeToString(b)

	device := c.GetHeader("User-Agent")

	approved := count == 0 // первое устройство одобряется автоматически

	_, err = h.App.DB.Exec(
		"INSERT INTO user_sessions (token, userid, user_type, device_info, approved) VALUES (?0, ?1, ?2, ?3, ?4)",
		token, user.Id, user.Type, device, approved,
	)
	if err != nil {
		return false, err
	}

	if !approved {
		// Уведомляем админа в Telegram
		h.notifyAdmin(user.Id, token, device)
	}

	h.setAuthCookie(c, user.Id, token)
	return approved, nil
}

func (h *WebHandler) notifyAdmin(uid int64, token string, device string) {
	msg := fmt.Sprintf(
		"Новое устройство для пользователя %d\nУстройство: %s\n/approve_%s\n/deny_%s",
		uid, device, token, token,
	)
	// FIXME сделать оповещение
	h.App.Log.Error(msg)
	// h.App.TgSendToAdmin(msg)
}
