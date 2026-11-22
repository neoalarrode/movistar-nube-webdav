package notifier

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"movistar-webdav-proxy/internal/logger"
)

type TelegramNotifier struct {
	botToken string
	chatID   string
	client   *http.Client
	mu       sync.Mutex

	lastNotificationAt time.Time
	//lastSessionExpiredAt time.Time
	lastAlerts map[string]time.Time // <--- CAMBIADO (antes lastSessionExpiredAt)
	lastError  string
}

// sendRetryDelay: espera antes de reintentar un envío que falló por la red.
var sendRetryDelay = 3 * time.Second

// apiBase es la API de Telegram (variable para los tests).
var apiBase = "https://api.telegram.org"

// Default es la instancia global compartida por toda la aplicación.
var Default *TelegramNotifier

func init() {
	Default = &TelegramNotifier{
		botToken:   strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")),
		chatID:     strings.TrimSpace(os.Getenv("TELEGRAM_CHAT_ID")),
		client:     newClient(10 * time.Second),
		lastAlerts: make(map[string]time.Time),
	}
}

func NewTelegramNotifier(botToken, chatID string) *TelegramNotifier {
	token := strings.TrimSpace(botToken)
	chat := strings.TrimSpace(chatID)

	// cuando no tiene valor, buscamos en las variables del entorno
	if token == "" {
		token = strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	}
	if chat == "" {
		chat = strings.TrimSpace(os.Getenv("TELEGRAM_CHAT_ID"))
	}

	// actualizamos si precisa
	if Default != nil {
		if token != "" && chat != "" {
			Default.UpdateCredentials(token, chat)
		}
		return Default
	}

	Default = &TelegramNotifier{
		botToken:   token,
		chatID:     chat,
		client:     newClient(10 * time.Second),
		lastAlerts: make(map[string]time.Time),
	}
	return Default
}

func (t *TelegramNotifier) IsConfigured() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.botToken != "" && t.chatID != ""
}

func (t *TelegramNotifier) UpdateCredentials(botToken, chatID string) {
	token := strings.TrimSpace(botToken)
	chat := strings.TrimSpace(chatID)

	// Permite que las notificaciones y alertas sigan funcionando con la cuenta desvinculada
	// Si se limpian las credenciales en la web, volver a las variables de entorno del Docker si existen
	if token == "" {
		token = strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	}
	if chat == "" {
		chat = strings.TrimSpace(os.Getenv("TELEGRAM_CHAT_ID"))
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.botToken = token
	t.chatID = chat
}

func (t *TelegramNotifier) Status() (bool, string, string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	lastAt := ""
	if !t.lastNotificationAt.IsZero() {
		lastAt = t.lastNotificationAt.Format("2006-01-02 15:04:05")
	}
	return t.botToken != "" && t.chatID != "", lastAt, t.lastError
}

func sanitizeTransportError(err error, token string) string {
	if err == nil {
		return ""
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		err = urlErr.Err
	}
	msg := err.Error()
	if token != "" {
		msg = strings.ReplaceAll(msg, token, logger.Mask(token))
	}
	return msg
}

// SendMessage envía texto tal cual (sin formato).
func (t *TelegramNotifier) SendMessage(text string) error { return t.send(text, false) }

// sendHTML envía con formato HTML de Telegram: el texto dinámico tiene que ir
// escapado (html.EscapeString), si no Telegram rechaza el mensaje.
func (t *TelegramNotifier) sendHTML(text string) error { return t.send(text, true) }

// sendHTMLWith es sendHTML con campos extra en la petición (p. ej. reply_markup).
func (t *TelegramNotifier) sendHTMLWith(text string, extra map[string]interface{}) error {
	return t.sendExtra(text, true, extra)
}

func (t *TelegramNotifier) send(text string, asHTML bool) error {
	return t.sendExtra(text, asHTML, nil)
}

func (t *TelegramNotifier) sendExtra(text string, asHTML bool, extra map[string]interface{}) error {
	t.mu.Lock()
	token := t.botToken
	chatID := t.chatID
	t.mu.Unlock()

	if token == "" || chatID == "" {
		return fmt.Errorf("telegram not configured")
	}

	payload := map[string]interface{}{
		"chat_id": chatID,
		"text":    text,
		"link_preview_options": map[string]bool{
			"is_disabled": true,
		},
	}
	if asHTML {
		payload["parse_mode"] = "HTML"
	}
	for k, v := range extra {
		payload[k] = v
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	apiURL := fmt.Sprintf("%s/bot%s/sendMessage", apiBase, token)
	post := func() (*http.Response, error) {
		req, err := http.NewRequest("POST", apiURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return t.client.Do(req)
	}
	resp, err := post()
	if err != nil {
		// Un fallo de red suele ser una conexión que quedó en la red anterior: se
		// reintenta una vez con conexiones nuevas.
		transport.CloseIdleConnections()
		time.Sleep(sendRetryDelay)
		resp, err = post()
	}
	if err != nil {
		safeErr := sanitizeTransportError(err, token)
		t.mu.Lock()
		t.lastError = fmt.Sprintf("Error de red: %s", safeErr)
		t.mu.Unlock()
		logger.Log("[Telegram] ❌ Error conectando con Telegram: %s", safeErr)
		return fmt.Errorf("error de red contactando con Telegram: %s", safeErr)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		safeBody := strings.ReplaceAll(string(respBody), token, logger.Mask(token))
		errMsg := fmt.Sprintf("Telegram respondió HTTP %d: %s", resp.StatusCode, safeBody)
		t.mu.Lock()
		t.lastError = errMsg
		t.mu.Unlock()
		logger.Log("[Telegram] ❌ %s", errMsg)
		return fmt.Errorf("Telegram respondió HTTP %d", resp.StatusCode)
	}

	t.mu.Lock()
	t.lastNotificationAt = time.Now()
	t.lastError = ""
	t.mu.Unlock()

	logger.Log("[Telegram] ✉️ Notificación enviada con éxito a chat %s.", logger.Mask(chatID))
	return nil
}

func (t *TelegramNotifier) SendSessionExpiredAlert(details string) error {
	t.mu.Lock()
	if t.lastAlerts == nil {
		t.lastAlerts = make(map[string]time.Time)
	}
	key := "session_expired"
	if lastTime, exists := t.lastAlerts[key]; exists && time.Since(lastTime) < time.Hour {
		t.mu.Unlock()
		return nil
	}
	t.lastAlerts[key] = time.Now()
	t.mu.Unlock()

	msg := fmt.Sprintf(
		"⚠️ <b>Sesión de Movistar Cloud caducada</b>\n\n"+
			"El proxy WebDAV no puede acceder a tus archivos porque la sesión ha expirado.\n\n"+
			"Detalle: %s\n"+
			"Hora: %s\n\n"+
			"Por favor, abre el panel Web para revincular tu dispositivo.",
		html.EscapeString(details), time.Now().Format("2006-01-02 15:04:05"),
	)
	return t.sendHTML(msg)
}

func (t *TelegramNotifier) SendTestAlert() error {
	return t.sendHTML("🔔 <b>Notificaciones WebDAV de Movistar Cloud activas</b>\n\n" +
		"El proxy WebDAV está conectado con Telegram y listo para avisarte ante cualquier evento o caducidad de sesión.")
}

// Para que se use de forma global
// Send envía un mensaje directo usando el notificador global.
func Send(text string) error {
	if Default == nil || !Default.IsConfigured() {
		return nil
	}
	return Default.SendMessage(text)
}

// Alert envía una alerta genérica con control antispam de 1 hora por cada alertKey.
// Si alertKey es "", se envía siempre sin esperar.
func Alert(alertKey, icon, title, description, details string) error {
	if Default == nil || !Default.IsConfigured() {
		return nil
	}
	return Default.SendAlert(alertKey, icon, title, description, details)
}

func (t *TelegramNotifier) SendAlert(alertKey, icon, title, description, details string) error {
	t.mu.Lock()
	if t.lastAlerts == nil {
		t.lastAlerts = make(map[string]time.Time)
	}
	if alertKey != "" {
		if lastTime, exists := t.lastAlerts[alertKey]; exists && time.Since(lastTime) < time.Hour {
			t.mu.Unlock()
			return nil
		}
		t.lastAlerts[alertKey] = time.Now()
	}
	t.mu.Unlock()

	// Construir el mensaje dinámicamente
	if strings.TrimSpace(icon) == "" {
		icon = "⚠️"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s <b>%s</b>\n\n", icon, html.EscapeString(title)))
	sb.WriteString(fmt.Sprintf("%s\n\n", html.EscapeString(description)))

	if details != "" {
		sb.WriteString(fmt.Sprintf("Detalle: %s\n", html.EscapeString(details)))
	}
	sb.WriteString(fmt.Sprintf("Hora: %s", time.Now().Format("2006-01-02 15:04:05")))

	if m, ok := alertMarkup.Load().(map[string]interface{}); ok && m != nil {
		return t.sendHTMLWith(sb.String(), map[string]interface{}{"reply_markup": m})
	}
	return t.sendHTML(sb.String())
}

// Credentials devuelve el token y chatID activos actualmente en memoria.
func (t *TelegramNotifier) Credentials() (string, string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.botToken, t.chatID
}

// SendTestWith manda el mensaje de prueba con unas credenciales concretas sin
// cambiar las activas (probar no debe dejar puesto lo que no se ha guardado).
func SendTestWith(botToken, chatID string) error {
	t := &TelegramNotifier{botToken: strings.TrimSpace(botToken), chatID: strings.TrimSpace(chatID),
		client: newClient(10 * time.Second), lastAlerts: map[string]time.Time{}}
	return t.SendTestAlert()
}

// DetectedChat es un chat que ha escrito al bot (para no tener que buscar el ID a mano).
type DetectedChat struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// DetectChats pide a Telegram los mensajes recientes del bot y devuelve los chats que
// le han escrito (p. ej. tras mandarle /start).
func DetectChats(botToken string) ([]DetectedChat, error) {
	token := strings.TrimSpace(botToken)
	client := newClient(10 * time.Second)
	resp, err := client.Get(fmt.Sprintf("%s/bot%s/getUpdates", apiBase, token))
	if err != nil {
		return nil, fmt.Errorf("error de red contactando con Telegram: %s", sanitizeTransportError(err, token))
	}
	defer resp.Body.Close()
	var out struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      []struct {
			Message *struct {
				Chat struct {
					ID        int64  `json:"id"`
					Title     string `json:"title"`
					FirstName string `json:"first_name"`
					Username  string `json:"username"`
				} `json:"chat"`
			} `json:"message"`
		} `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("respuesta de Telegram no válida")
	}
	if !out.OK {
		return nil, fmt.Errorf("Telegram: %s", out.Description)
	}
	seen := map[int64]bool{}
	var chats []DetectedChat
	for _, u := range out.Result {
		if u.Message == nil || seen[u.Message.Chat.ID] {
			continue
		}
		c := u.Message.Chat
		seen[c.ID] = true
		title := c.Title
		if title == "" {
			title = strings.TrimSpace(c.FirstName + " @" + c.Username)
		}
		chats = append(chats, DetectedChat{ID: c.ID, Title: title})
	}
	return chats, nil
}
