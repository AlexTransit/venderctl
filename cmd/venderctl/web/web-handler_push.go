package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/gin-gonic/gin"
)

type pushSubscriptionRecord struct {
	Endpoint string `pg:"endpoint"`
	P256DH   string `pg:"p256dh"`
	Auth     string `pg:"auth"`
}

type pushSubscribeRequest struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256DH string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

func (h *WebHandler) webPushConfigured() bool {
	cfg := h.App.Config.Web
	return cfg.VAPIDPublicKey != "" && cfg.VAPIDPrivateKey != "" && cfg.VAPIDSubject != ""
}

func (h *WebHandler) PushPublicKey(c *gin.Context) {
	if !h.webPushConfigured() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "web push not configured"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"publicKey": h.App.Config.Web.VAPIDPublicKey})
}

func (h *WebHandler) PushSubscribe(c *gin.Context) {
	if !h.webPushConfigured() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "web push not configured"})
		return
	}

	var req pushSubscribeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	if req.Endpoint == "" || req.Keys.P256DH == "" || req.Keys.Auth == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid subscription"})
		return
	}

	userID := c.MustGet("user_id").(int64)
	userType := c.MustGet("user_type").(int)

	_, err := h.App.DB.Exec(
		`INSERT INTO web_push_subscriptions (endpoint, userid, user_type, p256dh, auth, revoked, updated_at)
		 VALUES (?0, ?1, ?2, ?3, ?4, false, now())
		 ON CONFLICT (endpoint) DO UPDATE SET
		   userid = EXCLUDED.userid,
		   user_type = EXCLUDED.user_type,
		   p256dh = EXCLUDED.p256dh,
		   auth = EXCLUDED.auth,
		   revoked = false,
		   updated_at = now()`,
		req.Endpoint, userID, userType, req.Keys.P256DH, req.Keys.Auth,
	)
	if err != nil {
		h.App.Log.Errorf("push subscribe db error user=%d type=%d err=%v", userID, userType, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *WebHandler) PushUnsubscribe(c *gin.Context) {
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	if req.Endpoint == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "endpoint is required"})
		return
	}

	userID := c.MustGet("user_id").(int64)
	userType := c.MustGet("user_type").(int)

	_, err := h.App.DB.Exec(
		`UPDATE web_push_subscriptions
		   SET revoked = true, updated_at = now()
		 WHERE endpoint = ?0 AND userid = ?1 AND user_type = ?2`,
		req.Endpoint, userID, userType,
	)
	if err != nil {
		h.App.Log.Errorf("push unsubscribe db error user=%d type=%d err=%v", userID, userType, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *WebHandler) sendWebPushToUser(userID int64, userType int32, title string, body string) {
	if !h.webPushConfigured() {
		return
	}

	var subscriptions []pushSubscriptionRecord
	_, err := h.App.DB.Query(
		&subscriptions,
		`SELECT endpoint, p256dh, auth
		   FROM web_push_subscriptions
		  WHERE userid = ?0 AND user_type = ?1 AND revoked = false`,
		userID, userType,
	)
	if err != nil {
		h.App.Log.Errorf("push query error user=%d type=%d err=%v", userID, userType, err)
		return
	}

	if len(subscriptions) == 0 {
		return
	}

	h.sendWebPushSubscriptions(userID, subscriptions, title, body, nil)
}

func (h *WebHandler) sendWebPushToUserWithData(userID int64, userType int32, title string, body string, data map[string]any) {
	if !h.webPushConfigured() {
		return
	}

	var subscriptions []pushSubscriptionRecord
	_, err := h.App.DB.Query(
		&subscriptions,
		`SELECT endpoint, p256dh, auth
		   FROM web_push_subscriptions
		  WHERE userid = ?0 AND user_type = ?1 AND revoked = false`,
		userID, userType,
	)
	if err != nil {
		h.App.Log.Errorf("push query error user=%d type=%d err=%v", userID, userType, err)
		return
	}

	if len(subscriptions) == 0 {
		return
	}

	h.sendWebPushSubscriptions(userID, subscriptions, title, body, data)
}

// func (h *WebHandler) sendWebPushToUserAnyType(userID int64, title string, body string) {
// 	if !h.webPushConfigured() {
// 		return
// 	}

// 	var subscriptions []pushSubscriptionRecord
// 	_, err := h.App.DB.Query(
// 		&subscriptions,
// 		`SELECT endpoint, p256dh, auth
// 		   FROM web_push_subscriptions
// 		  WHERE userid = ?0 AND revoked = false`,
// 		userID,
// 	)
// 	if err != nil {
// 		h.App.Log.Errorf("push query error user=%d err=%v", userID, err)
// 		return
// 	}

// 	if len(subscriptions) == 0 {
// 		return
// 	}

// 	h.sendWebPushSubscriptions(userID, subscriptions, title, body, nil)
// }

func (h *WebHandler) sendWebPushToUserAnyTypeWithData(userID int64, title string, body string, data map[string]any) {
	if !h.webPushConfigured() {
		return
	}

	var subscriptions []pushSubscriptionRecord
	_, err := h.App.DB.Query(
		&subscriptions,
		`SELECT endpoint, p256dh, auth
		   FROM web_push_subscriptions
		  WHERE userid = ?0 AND revoked = false`,
		userID,
	)
	if err != nil {
		h.App.Log.Errorf("push query error user=%d err=%v", userID, err)
		return
	}

	if len(subscriptions) == 0 {
		return
	}

	h.sendWebPushSubscriptions(userID, subscriptions, title, body, data)
}

func (h *WebHandler) sendWebPushSubscriptions(userID int64, subscriptions []pushSubscriptionRecord, title string, body string, data map[string]any) {
	if len(subscriptions) == 0 {
		return
	}

	payloadData := gin.H{
		"title": title,
		"body":  body,
		"url":   h.App.Config.WebRootPath(),
		"ts":    time.Now().Unix(),
	}
	for k, v := range data {
		payloadData[k] = v
	}
	payload, _ := json.Marshal(payloadData)

	options := &webpush.Options{
		Subscriber:      h.App.Config.Web.VAPIDSubject,
		VAPIDPublicKey:  h.App.Config.Web.VAPIDPublicKey,
		VAPIDPrivateKey: h.App.Config.Web.VAPIDPrivateKey,
		TTL:             120,
	}

	for _, s := range subscriptions {
		sub := &webpush.Subscription{
			Endpoint: s.Endpoint,
			Keys: webpush.Keys{
				P256dh: s.P256DH,
				Auth:   s.Auth,
			},
		}
		resp, err := webpush.SendNotification(payload, sub, options)
		if err != nil {
			h.App.Log.Errorf("push send error user=%d endpoint=%s err=%v", userID, s.Endpoint, err)
			continue
		}
		_ = resp.Body.Close()

		// Expired/invalid subscription; mark revoked to stop retry noise.
		if resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusNotFound {
			_, _ = h.App.DB.Exec(
				`UPDATE web_push_subscriptions SET revoked = true, updated_at = now() WHERE endpoint = ?0`,
				s.Endpoint,
			)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			h.App.Log.Errorf("push send status=%d endpoint=%s", resp.StatusCode, s.Endpoint)
			// Unauthorized VAPID should stop all sends until config fixed.
			if resp.StatusCode == http.StatusUnauthorized || strings.Contains(strings.ToLower(resp.Status), "unauthorized") {
				return
			}
		}
	}
}
