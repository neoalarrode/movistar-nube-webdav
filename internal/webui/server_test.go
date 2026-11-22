package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"movistar-webdav-proxy/internal/config"
	"movistar-webdav-proxy/internal/notifier"
	"movistar-webdav-proxy/internal/sapi"
	"movistar-webdav-proxy/internal/updater"
	"movistar-webdav-proxy/internal/webdav"
)

func setupTestWebUI(t *testing.T, authCfg config.AuthConfig) (http.Handler, string) {
	t.Helper()
	tmpDir := t.TempDir()
	origTokensDir := config.TokensDir
	config.TokensDir = tmpDir
	t.Cleanup(func() { config.TokensDir = origTokensDir })

	cfg := config.AccountConfig{
		DeviceID: "fol-secretdevice1234",
	}

	client, err := sapi.NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	davSrv := webdav.NewServerWithAuth(client, authCfg)
	uiSrv := NewServerWithAuth(client, davSrv, authCfg)
	return uiSrv.Routes(), tmpDir
}

func TestWebUI_AuthAndHealthExemption(t *testing.T) {
	handler, _ := setupTestWebUI(t, config.AuthConfig{
		Username: "admin",
		Password: "secretpassword",
	})

	// 1. /health no requiere autenticación
	healthReq := httptest.NewRequest("GET", "/health", nil)
	healthRec := httptest.NewRecorder()
	handler.ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("expected /health 200 without auth, got %d", healthRec.Code)
	}

	// 1b. GET / sirve index.html con la versión inyectada dinámicamente desde config.Version
	indexReq := httptest.NewRequest("GET", "/", nil)
	indexReq.SetBasicAuth("admin", "secretpassword")
	indexRec := httptest.NewRecorder()
	handler.ServeHTTP(indexRec, indexReq)
	if indexRec.Code != http.StatusOK {
		t.Fatalf("expected / 200 with auth, got %d", indexRec.Code)
	}
	if !strings.Contains(indexRec.Body.String(), config.Version+" (Go)") {
		t.Errorf("expected index.html to contain dynamic version %s (Go), got: %s", config.Version, indexRec.Body.String()[:500])
	}
	if strings.Contains(indexRec.Body.String(), "{{.Version}}") {
		t.Errorf("index.html contains unrendered template placeholder {{.Version}}")
	}

	// 2. /status sin credenciales -> 401
	statusReq := httptest.NewRequest("GET", "/status", nil)
	statusRec := httptest.NewRecorder()
	handler.ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected /status 401 without auth, got %d", statusRec.Code)
	}

	// 3. /status con credenciales -> 200 y teléfono enmascarado
	authReq := httptest.NewRequest("GET", "/status", nil)
	authReq.SetBasicAuth("admin", "secretpassword")
	authRec := httptest.NewRecorder()
	handler.ServeHTTP(authRec, authReq)
	if authRec.Code != http.StatusOK {
		t.Fatalf("expected /status 200 with auth, got %d", authRec.Code)
	}
	if strings.Contains(authRec.Body.String(), "612345678") {
		t.Errorf("expected phone to be masked in /status, got: %s", authRec.Body.String())
	}

	// 4. Ruta inexistente -> 404 (no devuelve index.html)
	notFoundReq := httptest.NewRequest("GET", "/nonexistent", nil)
	notFoundReq.SetBasicAuth("admin", "secretpassword")
	notFoundRec := httptest.NewRecorder()
	handler.ServeHTTP(notFoundRec, notFoundReq)
	if notFoundRec.Code != http.StatusNotFound {
		t.Errorf("expected 404 on unknown route, got %d", notFoundRec.Code)
	}
	if notFoundRec.Header().Get("X-Frame-Options") != "DENY" || notFoundRec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("missing security headers: %+v", notFoundRec.Header())
	}
}

func TestWebUI_MethodsCSRFAndSecretsMasking(t *testing.T) {
	handler, _ := setupTestWebUI(t, config.AuthConfig{AuthDisabled: true})

	// 1. Métodos GET en endpoints mutadores -> 405 Method Not Allowed
	postOnlyEndpoints := []string{"/start", "/stop", "/save_telegram", "/test_telegram"}
	for _, ep := range postOnlyEndpoints {
		req := httptest.NewRequest("GET", ep, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: expected 405, got %d", ep, rec.Code)
		}
	}

	// 2. CSRF: Origin distinto a Host -> 403 Forbidden
	csrfReq := httptest.NewRequest("POST", "/start", strings.NewReader("{}"))
	csrfReq.Host = "localhost:5000"
	csrfReq.Header.Set("Origin", "http://evil.example.com")
	csrfReq.Header.Set("Content-Type", "application/json")
	csrfRec := httptest.NewRecorder()
	handler.ServeHTTP(csrfRec, csrfReq)
	if csrfRec.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden on cross-origin POST, got %d", csrfRec.Code)
	}

	// 4. Guardar en /save_telegram dejando bot_token vacío conserva el token activo y cambia el chat
	notifier.Default.UpdateCredentials("123456789:SecretBotTokenValueXYZ", "-100123456")
	saveReq := httptest.NewRequest("POST", "/save_telegram", strings.NewReader(`{"bot_token":"","chat_id":"-100999999"}`))
	saveReq.Header.Set("Content-Type", "application/json")
	saveRec := httptest.NewRecorder()
	handler.ServeHTTP(saveRec, saveReq)
	if saveRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on /save_telegram, got %d: %s", saveRec.Code, saveRec.Body.String())
	}
	if tok, chat := notifier.Default.Credentials(); tok != "123456789:SecretBotTokenValueXYZ" || chat != "-100999999" {
		t.Errorf("expected existing bot token to be preserved and chat_id updated, got: %s %s", tok, chat)
	}
	stRec := httptest.NewRecorder()
	handler.ServeHTTP(stRec, httptest.NewRequest("GET", "/telegram_status", nil))
	if strings.Contains(stRec.Body.String(), "SecretBotTokenValueXYZ") {
		t.Errorf("/telegram_status leaked plaintext bot token: %s", stRec.Body.String())
	}

	// 5. Intentar guardar bot_token con comillas o salto de línea -> 400 Bad Request
	badSaveReq := httptest.NewRequest("POST", "/save_telegram", strings.NewReader(`{"bot_token":"123:abc\"def","chat_id":"-100999999"}`))
	badSaveReq.Header.Set("Content-Type", "application/json")
	badSaveRec := httptest.NewRecorder()
	handler.ServeHTTP(badSaveRec, badSaveReq)
	if badSaveRec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 on invalid bot_token with quotes, got %d", badSaveRec.Code)
	}

	// 6. Límite de body (> 1 MB) -> 400 Bad Request
	hugeJSON := `{"chat_id":"` + strings.Repeat("6", (1<<20)+100) + `"}`
	hugeReq := httptest.NewRequest("POST", "/save_telegram", bytes.NewReader([]byte(hugeJSON)))
	hugeReq.Header.Set("Content-Type", "application/json")
	hugeRec := httptest.NewRecorder()
	handler.ServeHTTP(hugeRec, hugeReq)
	if hugeRec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 on >1MB JSON body, got %d", hugeRec.Code)
	}
}

func TestWebUI_LoginForm_PasswordManagerSupport(t *testing.T) {
	handler, _ := setupTestWebUI(t, config.AuthConfig{
		Username: "admin",
		Password: "secretpassword",
	})

	// 1. GET /login renderiza el formulario con soporte para gestores de contraseñas
	req := httptest.NewRequest("GET", "/login", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on GET /login, got %d", rec.Code)
	}

	body := rec.Body.String()

	// Verificación de estructura óptima para Password Managers (Bitwarden, 1Password, Chrome, etc.)
	checks := []struct {
		pattern string
		desc    string
	}{
		{`<form id="loginForm" method="POST" action="/login" autocomplete="on"`, "Formulario estándar con autocomplete=on y POST a /login"},
		{`id="username"`, "Input de usuario con id='username'"},
		{`name="username"`, "Input de usuario con name='username'"},
		{`autocomplete="username"`, "Input de usuario con autocomplete='username'"},
		{`<label for="username"`, "Etiqueta label asociada for='username'"},
		{`id="password"`, "Input de contraseña con id='password'"},
		{`name="password"`, "Input de contraseña con name='password'"},
		{`autocomplete="current-password"`, "Input de contraseña con autocomplete='current-password'"},
		{`<label for="password"`, "Etiqueta label asociada for='password'"},
		{`name="remember"`, "Checkbox recordatorio de sesión"},
		{`type="submit"`, "Botón de envío tipo submit"},
		{config.Version + " (Go)", "Versión inyectada dinámicamente en el login"},
	}

	for _, c := range checks {
		if !strings.Contains(body, c.pattern) {
			t.Errorf("falta en login.html: %s (buscado '%s')", c.desc, c.pattern)
		}
	}

	// 2. HEAD /login devuelve 200 con cabecera Referrer-Policy: same-origin
	headReq := httptest.NewRequest("HEAD", "/login", nil)
	headRec := httptest.NewRecorder()
	handler.ServeHTTP(headRec, headReq)
	if headRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on HEAD /login, got %d", headRec.Code)
	}
	if refPol := headRec.Header().Get("Referrer-Policy"); refPol != "same-origin" {
		t.Errorf("expected Referrer-Policy 'same-origin', got '%s'", refPol)
	}
}

func TestWebUI_UnauthenticatedRedirectAndAPI401(t *testing.T) {
	handler, _ := setupTestWebUI(t, config.AuthConfig{
		Username: "admin",
		Password: "secretpassword",
	})

	// 1. Petición web sin autenticación redirige a /login (303 See Other)
	navReq := httptest.NewRequest("GET", "/", nil)
	navReq.Header.Set("Accept", "text/html,application/xhtml+xml")
	navRec := httptest.NewRecorder()
	handler.ServeHTTP(navRec, navReq)

	if navRec.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect on unauthenticated GET /, got %d", navRec.Code)
	}
	if navRec.Header().Get("Location") != "/login" {
		t.Errorf("expected redirect location /login, got %s", navRec.Header().Get("Location"))
	}

	// 2. Petición API sin credenciales devuelve 401 JSON SIN encabezado WWW-Authenticate (no modal nativo de navegador)
	apiReq := httptest.NewRequest("GET", "/status", nil)
	apiReq.Header.Set("Accept", "application/json")
	apiRec := httptest.NewRecorder()
	handler.ServeHTTP(apiRec, apiReq)

	if apiRec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 on unauthenticated API call, got %d", apiRec.Code)
	}
	if apiRec.Header().Get("WWW-Authenticate") != "" {
		t.Errorf("API 401 response should not have WWW-Authenticate header to prevent native popup, got: %s", apiRec.Header().Get("WWW-Authenticate"))
	}
	var apiData map[string]interface{}
	if err := json.Unmarshal(apiRec.Body.Bytes(), &apiData); err != nil {
		t.Errorf("expected valid JSON 401 response: %v", err)
	}
}

func TestWebUI_LoginFlow_SuccessAndSession(t *testing.T) {
	handler, _ := setupTestWebUI(t, config.AuthConfig{
		Username: "admin",
		Password: "secretpassword",
	})

	// 1. Login con credenciales válidas mediante formulario estándar POST (x-www-form-urlencoded)
	formData := url.Values{
		"username": {"admin"},
		"password": {"secretpassword"},
		"remember": {"1"},
	}
	loginReq := httptest.NewRequest("POST", "/login", strings.NewReader(formData.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)

	if loginRec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect on successful login, got %d", loginRec.Code)
	}
	if loginRec.Header().Get("Location") != "/" {
		t.Errorf("expected redirect to /, got %s", loginRec.Header().Get("Location"))
	}

	// Verificar cookie de sesión
	cookies := loginRec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == sessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatalf("session cookie %s was not set", sessionCookieName)
	}
	if !sessionCookie.HttpOnly {
		t.Errorf("session cookie must be HttpOnly")
	}
	if sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("expected SameSite=Lax, got %v", sessionCookie.SameSite)
	}
	if sessionCookie.MaxAge <= 0 {
		t.Errorf("expected positive MaxAge for session cookie, got %d", sessionCookie.MaxAge)
	}

	// 2. Acceder a GET / con la cookie de sesión -> 200 OK
	indexReq := httptest.NewRequest("GET", "/", nil)
	indexReq.AddCookie(sessionCookie)
	indexRec := httptest.NewRecorder()
	handler.ServeHTTP(indexRec, indexReq)

	if indexRec.Code != http.StatusOK {
		t.Errorf("expected 200 on GET / with session cookie, got %d", indexRec.Code)
	}

	// 3. Acceder a GET /status con la cookie de sesión -> 200 OK
	statusReq := httptest.NewRequest("GET", "/status", nil)
	statusReq.AddCookie(sessionCookie)
	statusRec := httptest.NewRecorder()
	handler.ServeHTTP(statusRec, statusReq)

	if statusRec.Code != http.StatusOK {
		t.Errorf("expected 200 on /status with session cookie, got %d", statusRec.Code)
	}
	var statusData map[string]interface{}
	_ = json.Unmarshal(statusRec.Body.Bytes(), &statusData)
	if statusData["auth_enabled"] != true {
		t.Errorf("expected auth_enabled=true, got %v", statusData["auth_enabled"])
	}

	// 4. GET /login cuando ya está autenticado -> redirige a / (303)
	loginAgainReq := httptest.NewRequest("GET", "/login", nil)
	loginAgainReq.AddCookie(sessionCookie)
	loginAgainRec := httptest.NewRecorder()
	handler.ServeHTTP(loginAgainRec, loginAgainReq)

	if loginAgainRec.Code != http.StatusSeeOther || loginAgainRec.Header().Get("Location") != "/" {
		t.Errorf("expected redirect to / when already authenticated, got %d -> %s", loginAgainRec.Code, loginAgainRec.Header().Get("Location"))
	}

	// 5. Cerrar sesión con POST /logout
	logoutReq := httptest.NewRequest("POST", "/logout", nil)
	logoutReq.AddCookie(sessionCookie)
	logoutRec := httptest.NewRecorder()
	handler.ServeHTTP(logoutRec, logoutReq)

	if logoutRec.Code != http.StatusSeeOther || logoutRec.Header().Get("Location") != "/login" {
		t.Errorf("expected redirect to /login on logout, got %d -> %s", logoutRec.Code, logoutRec.Header().Get("Location"))
	}

	// Comprobar que la cookie se invalida (MaxAge < 0)
	logoutCookies := logoutRec.Result().Cookies()
	var clearedCookie *http.Cookie
	for _, c := range logoutCookies {
		if c.Name == sessionCookieName {
			clearedCookie = c
			break
		}
	}
	if clearedCookie == nil || clearedCookie.MaxAge >= 0 {
		t.Errorf("expected cookie to be cleared with negative MaxAge")
	}

	// 6. Verificar que la sesión destruida ya no permite acceso
	afterReq := httptest.NewRequest("GET", "/status", nil)
	afterReq.AddCookie(sessionCookie)
	afterRec := httptest.NewRecorder()
	handler.ServeHTTP(afterRec, afterReq)

	if afterRec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 after logout with old session token, got %d", afterRec.Code)
	}
}

func TestWebUI_Login_InvalidCredentials(t *testing.T) {
	handler, _ := setupTestWebUI(t, config.AuthConfig{
		Username: "admin",
		Password: "secretpassword",
	})

	// Intento fallido con contraseña incorrecta
	formData := url.Values{
		"username": {"admin"},
		"password": {"wrongpassword"},
	}
	req := httptest.NewRequest("POST", "/login", strings.NewReader(formData.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on bad password, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Usuario o contraseña incorrectos") {
		t.Errorf("expected error message in body, got: %s", body)
	}
	if !strings.Contains(body, `value="admin"`) {
		t.Errorf("expected entered username to be preserved, got: %s", body)
	}
}

func TestWebUI_Login_JSONSupport(t *testing.T) {
	handler, _ := setupTestWebUI(t, config.AuthConfig{
		Username: "admin",
		Password: "secretpassword",
	})

	// Login exitoso vía JSON
	jsonBody := `{"username":"admin","password":"secretpassword","remember":true}`
	req := httptest.NewRequest("POST", "/login", strings.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on JSON login, got %d: %s", rec.Code, rec.Body.String())
	}
	var res map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("expected JSON response: %v", err)
	}
	if res["status"] != "ok" || res["redirect"] != "/" {
		t.Errorf("unexpected JSON login response: %+v", res)
	}

	// Login fallido vía JSON
	badJSON := `{"username":"admin","password":"incorrect"}`
	badReq := httptest.NewRequest("POST", "/login", strings.NewReader(badJSON))
	badReq.Header.Set("Content-Type", "application/json")
	badRec := httptest.NewRecorder()
	handler.ServeHTTP(badRec, badReq)

	if badRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on bad JSON login, got %d", badRec.Code)
	}
}

func TestWebUI_AuthDisabledBehavior(t *testing.T) {
	handler, _ := setupTestWebUI(t, config.AuthConfig{AuthDisabled: true})

	// 1. GET / funciona directamente sin login ni Basic Auth
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 on GET / when auth disabled, got %d", rec.Code)
	}

	// 2. GET /login redirige directamente a /
	loginReq := httptest.NewRequest("GET", "/login", nil)
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)

	if loginRec.Code != http.StatusSeeOther || loginRec.Header().Get("Location") != "/" {
		t.Errorf("expected redirect to / when auth disabled, got %d -> %s", loginRec.Code, loginRec.Header().Get("Location"))
	}

	// 3. /status indica auth_enabled=false
	statusReq := httptest.NewRequest("GET", "/status", nil)
	statusRec := httptest.NewRecorder()
	handler.ServeHTTP(statusRec, statusReq)

	var statusData map[string]interface{}
	_ = json.Unmarshal(statusRec.Body.Bytes(), &statusData)
	if statusData["auth_enabled"] != false {
		t.Errorf("expected auth_enabled=false, got %v", statusData["auth_enabled"])
	}
}

func TestWebUI_Login_CSRFProtection(t *testing.T) {
	handler, _ := setupTestWebUI(t, config.AuthConfig{
		Username: "admin",
		Password: "secretpassword",
	})

	// 1. Origen malicioso (evil.com) -> 403 Forbidden
	evilReq := httptest.NewRequest("POST", "/login", strings.NewReader("username=admin&password=secretpassword"))
	evilReq.Host = "localhost:5000"
	evilReq.Header.Set("Origin", "http://evil.example.com")
	evilReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	evilRec := httptest.NewRecorder()
	handler.ServeHTTP(evilRec, evilReq)

	if evilRec.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden on evil.example.com login attempt, got %d", evilRec.Code)
	}

	// 2. Origen 'null' (iframe aislado / sandbox hostil) -> 403 Forbidden
	nullReq := httptest.NewRequest("POST", "/login", strings.NewReader("username=admin&password=secretpassword"))
	nullReq.Host = "localhost:5000"
	nullReq.Header.Set("Origin", "null")
	nullReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	nullRec := httptest.NewRecorder()
	handler.ServeHTTP(nullRec, nullReq)

	if nullRec.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden on Origin: null, got %d", nullRec.Code)
	}

	// 3. Origen legítimo localhost vs 127.0.0.1 (mismo loopback y puerto) -> 303 Éxito
	loopbackReq := httptest.NewRequest("POST", "/login", strings.NewReader("username=admin&password=secretpassword"))
	loopbackReq.Host = "127.0.0.1:5000"
	loopbackReq.Header.Set("Origin", "http://localhost:5000")
	loopbackReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loopbackRec := httptest.NewRecorder()
	handler.ServeHTTP(loopbackRec, loopbackReq)

	if loopbackRec.Code != http.StatusSeeOther {
		t.Errorf("expected 303 on valid loopback match (localhost vs 127.0.0.1), got %d: %s", loopbackRec.Code, loopbackRec.Body.String())
	}

	// 4. Detrás de proxy inverso (X-Forwarded-Host: proxy.example.com) -> 303 Éxito
	proxyReq := httptest.NewRequest("POST", "/login", strings.NewReader("username=admin&password=secretpassword"))
	proxyReq.Host = "10.0.0.5:5000"
	proxyReq.Header.Set("X-Forwarded-Host", "proxy.example.com")
	proxyReq.Header.Set("Origin", "https://proxy.example.com")
	proxyReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	proxyRec := httptest.NewRecorder()
	handler.ServeHTTP(proxyRec, proxyReq)

	if proxyRec.Code != http.StatusSeeOther {
		t.Errorf("expected 303 on valid X-Forwarded-Host match, got %d", proxyRec.Code)
	}
}

func TestWebUI_Logout_DirectNavigationAndResidualBasicAuth(t *testing.T) {
	handler, _ := setupTestWebUI(t, config.AuthConfig{
		Username: "admin",
		Password: "secretpassword",
	})

	// 1. Iniciar sesión mediante formulario
	loginReq := httptest.NewRequest("POST", "/login", strings.NewReader("username=admin&password=secretpassword"))
	loginReq.Host = "localhost:5000"
	loginReq.Header.Set("Origin", "http://localhost:5000")
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)

	if loginRec.Code != http.StatusSeeOther {
		t.Fatalf("login failed: code %d", loginRec.Code)
	}

	var sessionCookie *http.Cookie
	for _, c := range loginRec.Result().Cookies() {
		if c.Name == sessionCookieName && c.Value != "" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatalf("session cookie not set")
	}

	// 2. Acceso a / con la cookie -> 200 OK
	panelReq := httptest.NewRequest("GET", "/", nil)
	panelReq.Header.Set("Accept", "text/html,application/xhtml+xml")
	panelReq.AddCookie(sessionCookie)
	panelRec := httptest.NewRecorder()
	handler.ServeHTTP(panelRec, panelReq)
	if panelRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on panel with session cookie, got %d", panelRec.Code)
	}

	// 3. Cerrar sesión vía GET /logout (navegación directa del botón)
	logoutReq := httptest.NewRequest("GET", "/logout", nil)
	logoutReq.AddCookie(sessionCookie)
	logoutRec := httptest.NewRecorder()
	handler.ServeHTTP(logoutRec, logoutReq)

	if logoutRec.Code != http.StatusSeeOther || logoutRec.Header().Get("Location") != "/login" {
		t.Errorf("expected 303 -> /login on GET /logout, got %d -> %s", logoutRec.Code, logoutRec.Header().Get("Location"))
	}

	// Verificar cookie eliminada
	var clearedCookie *http.Cookie
	for _, c := range logoutRec.Result().Cookies() {
		if c.Name == sessionCookieName {
			clearedCookie = c
			break
		}
	}
	if clearedCookie == nil || clearedCookie.MaxAge >= 0 {
		t.Errorf("expected session cookie to be cleared on logout")
	}

	// 4. Si el navegador visita /login con cabecera Basic Auth residual -> debe mostrar la página de login (200), NO redirigir a /
	loginPageReq := httptest.NewRequest("GET", "/login", nil)
	loginPageReq.SetBasicAuth("admin", "secretpassword")
	loginPageRec := httptest.NewRecorder()
	handler.ServeHTTP(loginPageRec, loginPageReq)

	if loginPageRec.Code != http.StatusOK {
		t.Errorf("expected 200 on /login despite residual Basic Auth, got %d", loginPageRec.Code)
	}
	if !strings.Contains(loginPageRec.Body.String(), "Iniciar Sesión") {
		t.Errorf("expected login page to be rendered")
	}

	// 5. Si el navegador intenta navegar a / sin cookie pero con Basic Auth residual -> debe redirigir a /login (303)
	blockedPanelReq := httptest.NewRequest("GET", "/", nil)
	blockedPanelReq.Header.Set("Accept", "text/html,application/xhtml+xml")
	blockedPanelReq.SetBasicAuth("admin", "secretpassword")
	blockedPanelRec := httptest.NewRecorder()
	handler.ServeHTTP(blockedPanelRec, blockedPanelReq)

	if blockedPanelRec.Code != http.StatusSeeOther || blockedPanelRec.Header().Get("Location") != "/login" {
		t.Errorf("expected 303 -> /login for browser navigation without session cookie, got %d -> %s", blockedPanelRec.Code, blockedPanelRec.Header().Get("Location"))
	}
}

func TestWebUI_UpdateNotificationOncePerSession(t *testing.T) {
	tmpDir := t.TempDir()
	origTokensDir := config.TokensDir
	config.TokensDir = tmpDir
	t.Cleanup(func() { config.TokensDir = origTokensDir })

	releaseServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"tag_name":   "v9.9.9",
			"html_url":   updater.DefaultReleasesPageURL + "/tag/v9.9.9",
			"draft":      false,
			"prerelease": false,
		})
	}))
	defer releaseServer.Close()

	client, err := sapi.NewClient(config.AccountConfig{})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	authCfg := config.AuthConfig{Username: "admin", Password: "secretpassword"}
	davSrv := webdav.NewServerWithAuth(client, authCfg)
	uiSrv := NewServerWithAuth(client, davSrv, authCfg)

	checker := updater.NewCheckerWithOptions(
		releaseServer.URL,
		filepath.Join(tmpDir, ".last_notified_release"),
		func() string { return "v2.6.12" },
		func(latestVer, currentVer, releaseURL string) error { return nil },
	)
	checker.CheckOnce(context.Background())
	uiSrv.SetUpdateChecker(checker)

	handler := uiSrv.Routes()

	formData := url.Values{"username": {"admin"}, "password": {"secretpassword"}}
	loginReq := httptest.NewRequest("POST", "/login", strings.NewReader(formData.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 on login, got %d", loginRec.Code)
	}

	var sessionCookie *http.Cookie
	for _, c := range loginRec.Result().Cookies() {
		if c.Name == sessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatalf("expected session cookie %s", sessionCookieName)
	}

	// First /status in session must set notify_update_session=true.
	req1 := httptest.NewRequest("GET", "/status", nil)
	req1.AddCookie(sessionCookie)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first /status returned %d", rec1.Code)
	}
	var st1 map[string]interface{}
	if err := json.Unmarshal(rec1.Body.Bytes(), &st1); err != nil {
		t.Fatal(err)
	}
	if st1["update_available"] != true || st1["latest_version"] != "v9.9.9" || st1["notify_update_session"] != true {
		t.Fatalf("unexpected first /status payload: %+v", st1)
	}

	// Second /status in same session must keep update_available=true but flip notify_update_session=false.
	req2 := httptest.NewRequest("GET", "/status", nil)
	req2.AddCookie(sessionCookie)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second /status returned %d", rec2.Code)
	}
	var st2 map[string]interface{}
	if err := json.Unmarshal(rec2.Body.Bytes(), &st2); err != nil {
		t.Fatal(err)
	}
	if st2["update_available"] != true || st2["notify_update_session"] != false {
		t.Fatalf("unexpected second /status payload: %+v", st2)
	}

	// /version must also expose release availability fields.
	verReq := httptest.NewRequest("GET", "/version", nil)
	verReq.AddCookie(sessionCookie)
	verRec := httptest.NewRecorder()
	handler.ServeHTTP(verRec, verReq)
	if verRec.Code != http.StatusOK {
		t.Fatalf("/version returned %d", verRec.Code)
	}
	var verData map[string]interface{}
	if err := json.Unmarshal(verRec.Body.Bytes(), &verData); err != nil {
		t.Fatal(err)
	}
	if verData["update_available"] != true || verData["latest_version"] != "v9.9.9" {
		t.Fatalf("unexpected /version payload: %+v", verData)
	}
}
