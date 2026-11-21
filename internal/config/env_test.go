package config

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidateTelegramCredentials(t *testing.T) {
	if !ValidateBotToken("123456789:ABCdefGHI_jkl-MNO") {
		t.Error("expected valid bot token to pass")
	}
	if ValidateBotToken("invalid-token") {
		t.Error("expected invalid bot token to fail")
	}
	if ValidateBotToken("123456:ABC\"DEF\nINJECT") {
		t.Error("expected bot token with quotes/newline to fail")
	}

	if !ValidateChatID("-100123456789") || !ValidateChatID("12345678") || !ValidateChatID("@my_channel_name") {
		t.Error("expected valid chat IDs to pass")
	}
	if ValidateChatID("bad chat id") || ValidateChatID("@ab") {
		t.Error("expected invalid chat IDs to fail")
	}
}

func TestAuthConfigAndRateLimiter(t *testing.T) {
	pass := "s3cr3t-P@ss"
	sum := sha256.Sum256([]byte(pass))
	hashHex := hex.EncodeToString(sum[:])

	cfgPlain := AuthConfig{
		Username: "admin",
		Password: pass,
	}
	if !cfgPlain.IsEnabled() {
		t.Fatal("expected cfgPlain to be enabled")
	}
	if !cfgPlain.Validate("admin", pass) {
		t.Error("expected valid plain credentials to succeed")
	}
	if cfgPlain.Validate("admin", "wrong") || cfgPlain.Validate("other", pass) {
		t.Error("expected wrong credentials to fail")
	}

	cfgHash := AuthConfig{
		Username:     "admin",
		PasswordHash: hashHex,
	}
	if !cfgHash.Validate("admin", pass) {
		t.Error("expected valid sha256 password hash to succeed")
	}
	if cfgHash.Validate("admin", "wrong") {
		t.Error("expected wrong password against hash to fail")
	}

	rl := NewAuthRateLimiter(3, 50*time.Millisecond)
	ip := "192.0.2.10"
	if rl.IsBlocked(ip) {
		t.Error("IP should not be blocked initially")
	}
	rl.RecordFailure(ip)
	rl.RecordFailure(ip)
	if rl.IsBlocked(ip) {
		t.Error("IP should not be blocked after 2 failures (limit 3)")
	}
	if !rl.RecordFailure(ip) || !rl.IsBlocked(ip) {
		t.Error("IP should be blocked on 3rd failure")
	}
	time.Sleep(60 * time.Millisecond)
	if rl.IsBlocked(ip) {
		t.Error("IP should unblock after window expiration")
	}
}

func TestExtractClientIP(t *testing.T) {
	r1 := httptest.NewRequest("GET", "/", nil)
	r1.RemoteAddr = "192.168.1.50:54321"
	if got := ExtractClientIP(r1); got != "192.168.1.50" {
		t.Errorf("ExtractClientIP IPv4 = %q, want 192.168.1.50", got)
	}

	r2 := httptest.NewRequest("GET", "/", nil)
	r2.RemoteAddr = "[2001:db8::1]:8080"
	if got := ExtractClientIP(r2); got != "2001:db8::1" {
		t.Errorf("ExtractClientIP IPv6 = %q, want 2001:db8::1", got)
	}
}

func TestGetSyncInterval(t *testing.T) {
	t.Setenv("SAPI_CLIENT_PROFILE", "browser")
	t.Setenv("SYNC_INTERVAL_SEC", "")
	if got := GetSyncInterval(); got != 60*time.Second {
		t.Errorf("expected default 60s, got %v", got)
	}

	t.Setenv("SYNC_INTERVAL_SEC", "30")
	if got := GetSyncInterval(); got != 30*time.Second {
		t.Errorf("expected 30s, got %v", got)
	}

	t.Setenv("SYNC_INTERVAL_SEC", "5") // below min 15s
	if got := GetSyncInterval(); got != 15*time.Second {
		t.Errorf("expected clamped min 15s, got %v", got)
	}

	t.Setenv("SYNC_INTERVAL_SEC", "invalid")
	if got := GetSyncInterval(); got != 60*time.Second {
		t.Errorf("expected default 60s on invalid, got %v", got)
	}
}

func TestTransferLimits(t *testing.T) {
	t.Setenv("WEBDAV_MAX_DOWNLOADS", "")
	t.Setenv("WEBDAV_MAX_UPLOADS", "")
	if d, u := TransferLimits(true); d != 4 || u != 2 {
		t.Errorf("por defecto: esperado 4/2, obtenido %d/%d", d, u)
	}
	t.Setenv("WEBDAV_MAX_DOWNLOADS", "6")
	t.Setenv("WEBDAV_MAX_UPLOADS", "0")
	if d, u := TransferLimits(true); d != 6 || u != 2 {
		t.Errorf("con variables: esperado 6/2 (0 se ignora), obtenido %d/%d", d, u)
	}
}

// Con TOKENS_ENCRYPTION_KEY los secretos se guardan cifrados (enc:v1:) y solo se
// descifran con la misma clave.
func TestTokensEncryptionKey_AESGCM(t *testing.T) {
	t.Setenv("TOKENS_ENCRYPTION_KEY", "mi-clave-maestra-super-secreta-2026")
	enc, err := EncryptSecret("plain-valkey-secret")
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	if !strings.HasPrefix(enc, "enc:v1:") || strings.Contains(enc, "plain-valkey-secret") {
		t.Fatalf("esperaba el secreto cifrado con prefijo enc:v1:, obtenido %q", enc)
	}
	if got := DecryptSecret(enc); got != "plain-valkey-secret" {
		t.Fatalf("DecryptSecret con la clave buena = %q", got)
	}
	t.Setenv("TOKENS_ENCRYPTION_KEY", "clave-incorrecta")
	if got := DecryptSecret(enc); got == "plain-valkey-secret" {
		t.Fatal("con una clave incorrecta no debe descifrarse")
	}
}
