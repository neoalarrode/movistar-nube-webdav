package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	encryptedPrefix  = "enc:v1:"
	tokensEncryptEnv = "TOKENS_ENCRYPTION_KEY"
)

var (
	// Version se inyecta en tiempo de compilación mediante -ldflags "-X movistar-webdav-proxy/internal/config.Version=..."
	Version       = "v1.0.0"
	TokensDir     = "/app/tokens"
	fileLock      sync.Mutex
	botTokenRegex = regexp.MustCompile(`^\d+:[A-Za-z0-9_-]+$`)
	chatIDRegex   = regexp.MustCompile(`^-?\d+$|^@[A-Za-z0-9_]{5,}$`)
)

func init() {
	if custom := os.Getenv("TOKENS_DIR"); custom != "" {
		TokensDir = custom
	} else if _, err := os.Stat("/app/tokens"); err == nil {
		TokensDir = "/app/tokens"
	} else if _, err := os.Stat("tokens"); err == nil {
		TokensDir = "tokens"
	} else if _, err := os.Stat("/.dockerenv"); err == nil {
		TokensDir = "/app/tokens"
	} else {
		TokensDir = "tokens"
	}
}

type AccountConfig struct {
	JSessionID    string `json:"jsessionid"`
	ValidationKey string `json:"validationkey"`
	DeviceID      string `json:"device_id"`
	DeviceName    string `json:"device_name"`
}

// TransferLimits devuelve las descargas y subidas simultáneas (WEBDAV_MAX_DOWNLOADS,
// WEBDAV_MAX_UPLOADS). Por defecto 4 descargas y 2 subidas (la web sube de 2 en 2).
func TransferLimits() (downloads, uploads int) {
	downloads, uploads = 4, 2
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("WEBDAV_MAX_DOWNLOADS"))); err == nil && n > 0 {
		downloads = n
	}
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("WEBDAV_MAX_UPLOADS"))); err == nil && n > 0 {
		uploads = n
	}
	return downloads, uploads
}

// GetSyncInterval returns the metadata delta sync interval configured via SYNC_INTERVAL_SEC.
// Default: 60s; minimum: 15s.
func GetSyncInterval() time.Duration {
	if env := strings.TrimSpace(os.Getenv("SYNC_INTERVAL_SEC")); env != "" {
		if sec, err := strconv.Atoi(env); err == nil {
			if sec < 15 {
				sec = 15
			}
			return time.Duration(sec) * time.Second
		}
	}
	return 60 * time.Second
}

func ValidateBotToken(token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return true
	}
	return botTokenRegex.MatchString(token)
}

func ValidateChatID(chatID string) bool {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return true
	}
	return chatIDRegex.MatchString(chatID)
}

func getEncryptionKey() ([]byte, bool) {
	rawKey := strings.TrimSpace(os.Getenv(tokensEncryptEnv))
	if rawKey == "" {
		return nil, false
	}
	sum := sha256.Sum256([]byte(rawKey))
	return sum[:], true
}

func encryptValueIfNeeded(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	key, enabled := getEncryptionKey()
	if !enabled {
		return plain, nil
	}
	if strings.HasPrefix(plain, encryptedPrefix) {
		return plain, nil
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return encryptedPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

func decryptValueIfNeeded(val string) string {
	if !strings.HasPrefix(val, encryptedPrefix) {
		return val
	}
	key, enabled := getEncryptionKey()
	if !enabled {
		return ""
	}
	rawB64 := strings.TrimPrefix(val, encryptedPrefix)
	data, err := base64.StdEncoding.DecodeString(rawB64)
	if err != nil {
		return ""
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ""
	}
	if len(data) < gcm.NonceSize() {
		return ""
	}
	nonce := data[:gcm.NonceSize()]
	ciphertext := data[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return ""
	}
	return string(plain)
}

// EncryptSecret cifra un secreto para guardarlo en disco con TOKENS_ENCRYPTION_KEY
// (si no está definida, se guarda tal cual). DecryptSecret lo recupera.
func EncryptSecret(plain string) (string, error) { return encryptValueIfNeeded(plain) }

// DecryptSecret recupera un secreto guardado con EncryptSecret.
func DecryptSecret(val string) string { return decryptValueIfNeeded(val) }

// SecretsEncrypted indica si los secretos en disco van cifrados (TOKENS_ENCRYPTION_KEY).
func SecretsEncrypted() bool { _, ok := getEncryptionKey(); return ok }
