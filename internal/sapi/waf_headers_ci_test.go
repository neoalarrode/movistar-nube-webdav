package sapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"movistar-webdav-proxy/internal/auth"
	"movistar-webdav-proxy/internal/config"
)

func TestCI_CloudFrontWAF_RejectsLegacyFolDeviceIDAndBadUserAgent(t *testing.T) {
	t.Setenv("SAPI_CLIENT_PROFILE", "browser") // comportamiento del perfil "browser"
	t.Setenv("SAPI_USER_AGENT", "")
	t.Setenv("DESKTOP_USER_AGENT", "")

	var sawDeviceID, sawUserAgent, sawReferer, sawXHR, sawAcceptLang string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawDeviceID = r.Header.Get("X-deviceid")
		sawUserAgent = r.Header.Get("User-Agent")
		sawReferer = r.Header.Get("Referer")
		sawXHR = r.Header.Get("X-Requested-With")
		sawAcceptLang = r.Header.Get("Accept-Language")

		dropAsCloudFront401 := func() {
			w.Header().Set("Server", "CloudFront")
			w.Header().Set("X-Cache", "Error from cloudfront")
			w.WriteHeader(http.StatusUnauthorized) // 0-byte body
		}

		if sawDeviceID == "" || strings.HasPrefix(sawDeviceID, "fol-") || strings.ContainsAny(sawDeviceID, "+/=") {
			dropAsCloudFront401()
			return
		}
		if sawUserAgent == "DESKTOP" || strings.Contains(sawUserAgent, "Edg/157.0.0.0") || !strings.Contains(sawUserAgent, "Chrome/133.0.0.0") {
			dropAsCloudFront401()
			return
		}
		if sawReferer == "" || sawXHR != "XMLHttpRequest" || sawAcceptLang == "" {
			dropAsCloudFront401()
			return
		}

		w.Header().Set("Content-Type", "text/javascript;charset=UTF-8")
		switch {
		case strings.Contains(r.URL.Path, "/media"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{
					"quota":       int64(9223372036854775807),
					"used":        int64(361411543375),
					"free":        int64(9223371675443232432),
					"softdeleted": int64(134492577931),
					"nolimit":     true,
				},
				"responsetime": 1794844952692,
			})
		case strings.Contains(r.URL.Path, "/profile/changes"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"requesttime":  int64(1794844952000),
				"responsetime": int64(1794844952692),
				"data":         map[string]interface{}{},
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{}})
		}
	}))
	defer server.Close()

	client, err := NewClient(config.AccountConfig{
		Provider:        "movistar",
		Phone:           "612345678",
		AccessToken:     "valid-token",
		RefreshToken:    "valid-refresh",
		DeviceID:        "fol-xShd2lUKwcsWLi7YPhSfnA==",
		DeviceName:      "PC Windows",
		LastRefreshDate: time.Now().UnixMilli(),
		ExpiresIn:       "3600",
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	client.SetBaseURLs(server.URL+"/sapi", server.URL+"/upload")

	space, err := client.GetStorageSpace()
	if err != nil {
		t.Fatalf("GetStorageSpace failed: %v (UA=%q, X-deviceid=%q)", err, sawUserAgent, sawDeviceID)
	}
	if space == nil || space.UsedBytes != 361411543375 || !space.NoLimit {
		t.Fatalf("unexpected storage space: %+v", space)
	}

	if _, err := client.GetProfileChanges("0"); err != nil {
		t.Fatalf("GetProfileChanges failed: %v", err)
	}

	if sawDeviceID != "web-xShd2lUKwcsWLi7YPhSfnA" {
		t.Errorf("X-deviceid = %q, want %q", sawDeviceID, "web-xShd2lUKwcsWLi7YPhSfnA")
	}
	if !strings.Contains(sawUserAgent, "Chrome/133.0.0.0") {
		t.Errorf("User-Agent = %q, want Chrome/133.0.0.0", sawUserAgent)
	}

	// Verify SAPILoginOAuth also sanitizes legacy fol- DeviceIDs
	if got := auth.SanitizeDeviceID("fol-xShd2lUKwcsWLi7YPhSfnA=="); got != "web-xShd2lUKwcsWLi7YPhSfnA" {
		t.Errorf("SanitizeDeviceID = %q, want web-xShd2lUKwcsWLi7YPhSfnA", got)
	}
}

func TestCI_SummarizeHTTPErrorBody_TruncatesHTML(t *testing.T) {
	htmlBody := []byte(`<!--?xml version="1.0" encoding="UTF-8"?--><!DOCTYPE html><html><head><title>Movistar Cloud</title></head><body>` + strings.Repeat("<div>SPA</div>", 5000) + `</body></html>`)
	got := summarizeHTTPErrorBody(htmlBody)
	if !strings.Contains(got, "HTML error page") || !strings.Contains(got, `title="Movistar Cloud"`) {
		t.Fatalf("unexpected HTML summary: %q", got)
	}
	if len(got) >= 200 {
		t.Fatalf("HTML summary too long (%d bytes): %q", len(got), got)
	}

	longText := []byte(strings.Repeat("a", 500))
	gotLong := summarizeHTTPErrorBody(longText)
	if len(gotLong) > 250 || !strings.HasSuffix(gotLong, "…") {
		t.Fatalf("unexpected long text truncation: len=%d val=%q", len(gotLong), gotLong)
	}

	shortJSON := []byte(`{"error":{"code":"MED-1017"}}`)
	if gotShort := summarizeHTTPErrorBody(shortJSON); gotShort != string(shortJSON) {
		t.Fatalf("short JSON modified: %q", gotShort)
	}
}
