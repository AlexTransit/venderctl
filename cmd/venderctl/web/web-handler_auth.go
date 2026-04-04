package web

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v9"
)

type userRecord struct {
	Name string `pg:"name"`
	Id   int64  `pg:"userid"`
	Type int32  `pg:"user_type"`
}

func isLinkPreviewRequest(c *gin.Context) bool {
	ua := strings.ToLower(c.GetHeader("User-Agent"))
	if strings.Contains(ua, "telegrambot") || strings.Contains(ua, "twitterbot") {
		return true
	}
	purpose := strings.ToLower(c.GetHeader("Purpose") + " " + c.GetHeader("X-Purpose"))
	return strings.Contains(purpose, "preview") || strings.Contains(purpose, "prefetch")
}

type authTokenRecord struct {
	Userid    int64     `pg:"userid"`
	UserType  int32     `pg:"user_type"`
	Used      bool      `pg:"used"`
	CreatedAt time.Time `pg:"created_at"`
}

type webAuthStore interface {
	GetAuthToken(token string) (authTokenRecord, error)
	MarkAuthTokenUsed(token string) (int, error)
	CountApprovedActiveSessions(userID int64, userType int32) (int, error)
	InsertUserSession(token string, userID int64, userType int32, device string, approved bool) error
	RevokeSession(token string) error
}

type pgWebAuthStore struct {
	db *pg.DB
}

func (s *pgWebAuthStore) GetAuthToken(token string) (authTokenRecord, error) {
	var rec authTokenRecord
	_, err := s.db.QueryOne(&rec,
		`SELECT userid, user_type, used, created_at FROM web_auth_tokens WHERE token = ?`,
		token)
	return rec, err
}

func (s *pgWebAuthStore) MarkAuthTokenUsed(token string) (int, error) {
	res, err := s.db.Exec(`UPDATE web_auth_tokens SET used = true WHERE token = ? AND used = false`, token)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected(), nil
}

func (s *pgWebAuthStore) CountApprovedActiveSessions(userID int64, userType int32) (int, error) {
	var approvedCount int
	_, err := s.db.QueryOne(&approvedCount,
		`SELECT COUNT(*)
		 FROM user_sessions
		 WHERE userid = ?0 AND user_type = ?1 AND approved = true AND revoked = false`,
		userID, userType)
	return approvedCount, err
}

func (s *pgWebAuthStore) InsertUserSession(token string, userID int64, userType int32, device string, approved bool) error {
	_, err := s.db.Exec(
		"INSERT INTO user_sessions (token, userid, user_type, device_info, approved, revoked) VALUES (?0, ?1, ?2, ?3, ?4, false)",
		token, userID, userType, device, approved,
	)
	return err
}

func (s *pgWebAuthStore) RevokeSession(token string) error {
	_, err := s.db.Exec(`UPDATE user_sessions SET revoked = true WHERE token = ?0`, token)
	return err
}

func (h *WebHandler) getAuthStore() webAuthStore {
	if h.authStore != nil {
		return h.authStore
	}
	return &pgWebAuthStore{db: h.App.DB}
}

func (h *WebHandler) HandleAuth(c *gin.Context) {
	// Telegram link preview requests should not consume one-time auth tokens.
	if isLinkPreviewRequest(c) {
		c.String(http.StatusOK, "Ссылка подтверждена. Откройте ее в браузере.")
		return
	}

	store := h.getAuthStore()
	oneTimeToken := c.Query("token")

	if oneTimeToken == "" {
		c.String(400, "Для входа нужна ссылка-приглашение с токеном")
		return
	}

	rec, err := store.GetAuthToken(oneTimeToken)
	if err != nil {
		c.String(403, "Ссылка недействительна")
		return
	}

	// Ссылка одноразовая: если токен уже использован, второй раз не пускаем.
	if rec.Used {
		c.String(403, "Ссылка уже использована")
		return
	}
	if time.Since(rec.CreatedAt) > 30*time.Minute {
		c.String(403, "Ссылка устарела")
		return
	}
	// Атомарно помечаем токен как использованный, чтобы исключить повторное применение.
	rowsAffected, err := store.MarkAuthTokenUsed(oneTimeToken)
	if err != nil {
		c.String(500, "session error")
		return
	}
	if rowsAffected != 1 {
		c.String(403, "Ссылка уже использована")
		return
	}
	h.deleteInviteMessage(c.Query("tg_chat_id"), c.Query("tg_message_id"))

	user := userRecord{Id: rec.Userid, Type: int32(rec.UserType)}
	approved, err := h.createSession(c, &user)
	if err != nil {
		c.String(500, "session error")
		return
	}
	if !approved {
		c.String(http.StatusOK, "Вход с дополнительных устройств разрешен только после подтверждения администратора. \nСвяжитесь с ним. он разрулит Вашу проблему.")
		return
	}
	c.Redirect(http.StatusFound, h.App.Config.WebRootPath())
}

func (h *WebHandler) deleteInviteMessage(chatIDRaw string, messageIDRaw string) {
	chatIDStr := strings.TrimSpace(chatIDRaw)
	messageIDStr := strings.TrimSpace(messageIDRaw)
	if chatIDStr == "" || messageIDStr == "" {
		return
	}

	chatID, err := strconv.ParseInt(chatIDStr, 10, 64)
	if err != nil {
		h.App.Log.Errorf("invalid tg_chat_id=%q", chatIDStr)
		return
	}
	messageID, err := strconv.Atoi(messageIDStr)
	if err != nil {
		h.App.Log.Errorf("invalid tg_message_id=%q", messageIDStr)
		return
	}

	botToken := h.App.Config.Telegram.TelegrammBotApi
	go func(chatID int64, messageID int, token string) {
		client := &http.Client{Timeout: 3 * time.Second}
		form := url.Values{}
		form.Set("chat_id", strconv.FormatInt(chatID, 10))
		form.Set("message_id", strconv.Itoa(messageID))
		endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/deleteMessage", token)
		resp, err := client.PostForm(endpoint, form)
		if err != nil {
			h.App.Log.Errorf("telegram delete invite message error chat=%d message=%d err=%v", chatID, messageID, err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(resp.Body)
			h.App.Log.Errorf("telegram delete invite message status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
	}(chatID, messageID, botToken)
}

func (h *WebHandler) Logout(c *gin.Context) {
	store := h.getAuthStore()
	if cookie, err := c.Cookie("auth_user_id"); err == nil {
		parts := strings.Split(cookie, ":")
		if len(parts) == 3 {
			userIDStr := parts[0]
			token := parts[1]
			sig := parts[2]
			expected := signValue(h.App.Config.Web.SecretKey, userIDStr+token)
			if sig == expected {
				_ = store.RevokeSession(token)
			}
		}
	}
	c.SetCookie("auth_user_id", "", -1, h.App.Config.WebCookiePath(), "", false, true)
	c.Redirect(http.StatusFound, h.App.Config.WebRootPath())
}

func (h *WebHandler) createSession(c *gin.Context, user *userRecord) (bool, error) {
	store := h.getAuthStore()
	approvedCount, err := store.CountApprovedActiveSessions(user.Id, user.Type)
	if err != nil {
		return false, err
	}

	// Генерируем токен
	b := make([]byte, 16)
	rand.Read(b)
	token := hex.EncodeToString(b)

	device := c.GetHeader("User-Agent")

	// подключениче с двуих и более устройств - только с разрешения
	approved := approvedCount == 0
	// подлючения юзера с разных устрройст не ограничего
	// approved, _ := true, approvedCount

	err = store.InsertUserSession(token, user.Id, user.Type, device, approved)
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

func (h *WebHandler) OpenInvite(c *gin.Context) {
	token := c.Query("token")
	target := h.App.Config.Web.BaseURL + "/auth/callback?token=" + token

	html := `
<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"><title>Открываю...</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
</head>
<body style="font-family:sans-serif; padding:20px; text-align:center;">
<img src="` + h.App.Config.Web.BaseURL + `/icon-192.png" style="width:80px; border-radius:15px; margin-bottom:20px;">
<h2>Добро пожаловать!</h2>
<div id="copy-block" style="display:none;">
<p>Для возможности получения уведомлений о готовности напитка
откройте эту ссылку во внешнем браузере. нажав на три точки в правом верхнем углу. 
выберите открыть в ... или открыть в сафари.

если открыть ссыку в браузере хром, то появиться возможность сделать типа "приложение"
память занимать не будет, но можно вывести ярлык на главный экран телефона и тогда входить будет удобнее.
</p>
    <input id="link" type="text" value="` + target + `" 
        style="width:100%; padding:10px; border:1px solid #ccc; border-radius:8px; font-size:14px; box-sizing:border-box;"
        onclick="this.select();" readonly>
    <button onclick="navigator.clipboard.writeText('` + target + `').then(()=>alert('Скопировано!'))"
        style="margin-top:15px; width:100%; padding:15px; background:#0088cc; color:white; border:none; border-radius:10px; font-size:16px;">
        📋 Скопировать ссылку
    </button>
    <p style="color:#888; font-size:13px;">Откройте Chrome и вставьте в адресную строку</p>
	<button onclick="window.location.href='` + target + `'"
    style="margin-top:10px; width:100%; padding:15px; background:#27ae60; color:white; border:none; border-radius:10px; font-size:16px;">
    🌐 Или можете продолжить в текущем браузере
	</button>
</div>
<script>
var target = "` + target + `";
var ua = navigator.userAgent.toLowerCase();
var isIOS = /iphone|ipad|ipod/.test(ua);
var chromeScheme = isIOS ? "googlechromes" : "googlechrome";
var chromeURL = chromeScheme + "://navigate?url=" + encodeURIComponent(target);
var isTelegramWebView = /telegram/i.test(navigator.userAgent) || typeof window.TelegramWebviewProxy !== 'undefined' || / wv\)/.test(navigator.userAgent);
var isChrome = /chrome/.test(ua) && !/edg|opr|brave/.test(ua) && !isTelegramWebView;

if (isChrome) {
    window.location.href = target;
} else {
    var chromeScheme = isIOS ? "googlechromes" : "googlechrome";
    window.location.href = chromeScheme + "://navigate?url=" + encodeURIComponent(target);
    setTimeout(function() {
        document.getElementById('copy-block').style.display = 'block';
        document.querySelector('h2').style.display = 'none';
    }, 2000);
}

</script>
</body>
</html>`
	c.Data(200, "text/html; charset=utf-8", []byte(html))
}

func (h *WebHandler) notifyAdmin(uid int64, token string, device string) {
	msg := fmt.Sprintf(
		"Новое устройство для пользователя %d\nУстройство: %s\n/approve_%s\n/deny_%s",
		uid, device, token, token,
	)
	// FIXME сделать оповещение
	h.App.Log.Info(msg)
	// h.App.TgSendToAdmin(msg)
}
