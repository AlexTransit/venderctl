package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AlexTransit/venderctl/internal/state"
	"github.com/gin-gonic/gin"
)

type fakeSession struct {
	Token      string
	UserID     int64
	UserType   int32
	DeviceInfo string
	Approved   bool
	Revoked    bool
	CreatedAt  time.Time
}

type fakeAuthStore struct {
	tokens   map[string]authTokenRecord
	sessions map[string]*fakeSession
}

func newFakeAuthStore() *fakeAuthStore {
	return &fakeAuthStore{
		tokens:   make(map[string]authTokenRecord),
		sessions: make(map[string]*fakeSession),
	}
}

func (s *fakeAuthStore) GetAuthToken(token string) (authTokenRecord, error) {
	rec, ok := s.tokens[token]
	if !ok {
		return authTokenRecord{}, io.EOF
	}
	return rec, nil
}

func (s *fakeAuthStore) MarkAuthTokenUsed(token string) (int, error) {
	rec, ok := s.tokens[token]
	if !ok {
		return 0, nil
	}
	if rec.Used {
		return 0, nil
	}
	rec.Used = true
	s.tokens[token] = rec
	return 1, nil
}

func (s *fakeAuthStore) CountApprovedActiveSessions(userID int64, userType int32) (int, error) {
	n := 0
	for _, sess := range s.sessions {
		if sess.UserID == userID && sess.UserType == userType && sess.Approved && !sess.Revoked {
			n++
		}
	}
	return n, nil
}

func (s *fakeAuthStore) InsertUserSession(token string, userID int64, userType int32, device string, approved bool) error {
	s.sessions[token] = &fakeSession{
		Token:      token,
		UserID:     userID,
		UserType:   userType,
		DeviceInfo: device,
		Approved:   approved,
		Revoked:    false,
		CreatedAt:  time.Now(),
	}
	return nil
}

func (s *fakeAuthStore) RevokeSession(token string) error {
	if sess, ok := s.sessions[token]; ok {
		sess.Revoked = true
	}
	return nil
}

func newTestWebHandler(t *testing.T, store *fakeAuthStore) *WebHandler {
	t.Helper()
	cfg := &state.Config{}
	cfg.Web.SecretKey = "test-secret"
	return &WebHandler{
		App: &state.Global{
			Config: cfg,
		},
		authStore: store,
	}
}

func runAuthRequest(h *WebHandler, token, userAgent string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?token="+token, nil)
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	c.Request = req
	h.HandleAuth(c)
	return w
}

func runLogoutRequest(h *WebHandler, authCookie *http.Cookie) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/auth/logout", nil)
	if authCookie != nil {
		req.AddCookie(authCookie)
	}
	c.Request = req
	h.Logout(c)
	return w
}

func findSessionByDevice(s *fakeAuthStore, userID int64, userType int32, device string) *fakeSession {
	var selected *fakeSession
	for _, sess := range s.sessions {
		if sess.UserID == userID && sess.UserType == userType && sess.DeviceInfo == device {
			if selected == nil || sess.CreatedAt.After(selected.CreatedAt) {
				selected = sess
			}
		}
	}
	return selected
}

func TestAuth_FirstDeviceLogoutThenNewLinkWorks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newFakeAuthStore()
	store.tokens["t1"] = authTokenRecord{Userid: 1001, UserType: 1, Used: false, CreatedAt: time.Now()}
	store.tokens["t2"] = authTokenRecord{Userid: 1001, UserType: 1, Used: false, CreatedAt: time.Now()}
	h := newTestWebHandler(t, store)

	w1 := runAuthRequest(h, "t1", "device-A")
	if w1.Code != http.StatusFound {
		t.Fatalf("first auth code=%d body=%s", w1.Code, w1.Body.String())
	}
	firstSession := findSessionByDevice(store, 1001, 1, "device-A")
	if firstSession == nil || !firstSession.Approved || firstSession.Revoked {
		t.Fatalf("first session invalid: %#v", firstSession)
	}

	var authCookie *http.Cookie
	for _, c := range w1.Result().Cookies() {
		if c.Name == "auth_user_id" {
			authCookie = c
			break
		}
	}
	if authCookie == nil {
		t.Fatal("auth cookie not set")
	}

	wLogout := runLogoutRequest(h, authCookie)
	if wLogout.Code != http.StatusFound {
		t.Fatalf("logout code=%d", wLogout.Code)
	}
	if !firstSession.Revoked {
		t.Fatal("first session should be revoked after logout")
	}

	w2 := runAuthRequest(h, "t2", "device-A")
	if w2.Code != http.StatusFound {
		t.Fatalf("second auth code=%d body=%s", w2.Code, w2.Body.String())
	}
	secondSession := findSessionByDevice(store, 1001, 1, "device-A")
	if secondSession == nil || !secondSession.Approved || secondSession.Revoked {
		t.Fatalf("second session invalid: %#v", secondSession)
	}
}

func TestAuth_NewDeviceRequiresApprovalWhenOldActive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newFakeAuthStore()
	store.tokens["ta"] = authTokenRecord{Userid: 2002, UserType: 1, Used: false, CreatedAt: time.Now()}
	store.tokens["tb"] = authTokenRecord{Userid: 2002, UserType: 1, Used: false, CreatedAt: time.Now()}
	h := newTestWebHandler(t, store)

	w1 := runAuthRequest(h, "ta", "device-A")
	if w1.Code != http.StatusFound {
		t.Fatalf("first auth code=%d body=%s", w1.Code, w1.Body.String())
	}
	oldSession := findSessionByDevice(store, 2002, 1, "device-A")
	if oldSession == nil || !oldSession.Approved || oldSession.Revoked {
		t.Fatalf("old session invalid: %#v", oldSession)
	}

	w2 := runAuthRequest(h, "tb", "device-B")
	if w2.Code != http.StatusOK {
		t.Fatalf("second device code=%d body=%s", w2.Code, w2.Body.String())
	}
	if !strings.Contains(w2.Body.String(), "только после подтверждения") {
		t.Fatalf("unexpected body: %s", w2.Body.String())
	}
	newSession := findSessionByDevice(store, 2002, 1, "device-B")
	if newSession == nil {
		t.Fatal("new device session not created")
	}
	if newSession.Approved {
		t.Fatal("new device session must be pending approval")
	}
	if newSession.Revoked {
		t.Fatal("new device session should not be revoked")
	}
}

func TestAuth_TelegramPreviewDoesNotConsumeToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newFakeAuthStore()
	store.tokens["preview-token"] = authTokenRecord{Userid: 3003, UserType: 1, Used: false, CreatedAt: time.Now()}
	h := newTestWebHandler(t, store)

	wPreview := runAuthRequest(h, "preview-token", "TelegramBot (like TwitterBot)")
	if wPreview.Code != http.StatusOK {
		t.Fatalf("preview code=%d body=%s", wPreview.Code, wPreview.Body.String())
	}
	if store.tokens["preview-token"].Used {
		t.Fatal("telegram preview should not consume token")
	}
	if len(store.sessions) != 0 {
		t.Fatalf("preview must not create sessions, got=%d", len(store.sessions))
	}

	wReal := runAuthRequest(h, "preview-token", "Mozilla/5.0")
	if wReal.Code != http.StatusFound {
		t.Fatalf("real open code=%d body=%s", wReal.Code, wReal.Body.String())
	}
	if !store.tokens["preview-token"].Used {
		t.Fatal("real open should consume token")
	}
}

func TestAuth_HeaderPreviewDoesNotConsumeToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newFakeAuthStore()
	store.tokens["preview-header-token"] = authTokenRecord{Userid: 4004, UserType: 1, Used: false, CreatedAt: time.Now()}
	h := newTestWebHandler(t, store)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?token=preview-header-token", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("X-Purpose", "preview")
	c.Request = req
	h.HandleAuth(c)

	if w.Code != http.StatusOK {
		t.Fatalf("preview header code=%d body=%s", w.Code, w.Body.String())
	}
	if store.tokens["preview-header-token"].Used {
		t.Fatal("preview header must not consume token")
	}
}
