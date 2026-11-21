package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"movistar-webdav-proxy/internal/config"
	"movistar-webdav-proxy/internal/rclonesvc"
	"movistar-webdav-proxy/internal/sapi"
	"movistar-webdav-proxy/internal/vault"
	"movistar-webdav-proxy/internal/webdav"
)

// Sin credenciales la pasarela responde 401 sin llegar al rclone interno (y sin anotar
// tamaños ni fechas de la ruta); con ellas, pasa.
func TestGatewayRequiresAuthFirst(t *testing.T) {
	old := config.TokensDir
	config.TokensDir = t.TempDir()
	t.Cleanup(func() { config.TokensDir = old })
	if err := vault.LoadFolders(); err != nil {
		t.Fatal(err)
	}
	conf := "[x-trozos]\ntype = chunker\nremote = nube:Fotos\n\n[x-cifrada]\ntype = crypt\nremote = x-trozos:\npassword = abc\n"
	if _, err := vault.SaveFolder(vault.Folder{Name: "Fotos", Config: conf}); err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer up.Close()
	g := newDecryptedGateway(strings.TrimPrefix(up.URL, "http://"), nil, rclonesvc.New(rclonesvc.Options{User: "u", Pass: "p"}))

	req := httptest.NewRequest(http.MethodPut, "/Fotos/a.jpg", strings.NewReader("x"))
	req.Header.Set("X-OC-Mtime", "1600000000")
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || hits.Load() != 0 {
		t.Fatalf("sin credenciales: %d, llegó al interno %d veces", rec.Code, hits.Load())
	}
	req = httptest.NewRequest(http.MethodPut, "/Fotos/a.jpg", strings.NewReader("x"))
	req.SetBasicAuth("u", "mala")
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || hits.Load() != 0 {
		t.Fatalf("contraseña mala: %d", rec.Code)
	}
	req = httptest.NewRequest("PROPFIND", "/Fotos/", nil)
	req.SetBasicAuth("u", "p")
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if hits.Load() != 1 {
		t.Fatalf("con credenciales no llegó al interno (código %d)", rec.Code)
	}
}

// El estado de una copia de un cliente se guarda solo con credenciales y llega al panel.
func TestGatewayClientCopyReport(t *testing.T) {
	g := newDecryptedGateway("127.0.0.1:1", nil, rclonesvc.New(rclonesvc.Options{User: "u", Pass: "p"}))
	body := `{"id":"PC-1","dest":"Vídeos/Serie","bytes":100,"total_bytes":400,"speed":50,"eta":6}`
	req := httptest.NewRequest(http.MethodPost, clientCopyPath, strings.NewReader(body))
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("sin credenciales: %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodPost, clientCopyPath, strings.NewReader(body))
	req.SetBasicAuth("u", "p")
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("con credenciales: %d", rec.Code)
	}
	cs := webdav.ClientCopies(time.Minute)
	if len(cs) != 1 || cs[0].Dest != "Vídeos/Serie" || cs[0].TotalBytes != 400 {
		t.Fatalf("estado guardado: %+v", cs)
	}
	req = httptest.NewRequest(http.MethodPost, clientCopyPath, strings.NewReader(`{"id":"PC-1","done":true}`))
	req.SetBasicAuth("u", "p")
	g.ServeHTTP(httptest.NewRecorder(), req)
	if len(webdav.ClientCopies(time.Minute)) != 0 {
		t.Fatal("al terminar debe desaparecer")
	}
}

// El aviso de cambio de red exige credenciales y llama al manejador con la vía.
func TestGatewayNetworkChange(t *testing.T) {
	got := make(chan string, 1)
	g := newDecryptedGateway("127.0.0.1:1", nil, rclonesvc.New(rclonesvc.Options{User: "u", Pass: "p"}))
	g.onNetChange = func(via string) { got <- via }
	req := httptest.NewRequest(http.MethodPost, networkChangePath, strings.NewReader(`{"via":"wifi"}`))
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("sin credenciales: %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodPost, networkChangePath, strings.NewReader(`{"via":"wifi"}`))
	req.SetBasicAuth("u", "p")
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("con credenciales: %d", rec.Code)
	}
	select {
	case v := <-got:
		if v != "wifi" {
			t.Fatalf("vía: %q", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no se llamó al manejador")
	}
}

// Tras un cambio de red, una subida fallida se devuelve como 503 + Retry-After para que
// rclone la vuelva a mandar entera; sin cambio de red, el error tal cual.
func TestGatewayPutRetryableAfterNetworkChange(t *testing.T) {
	old := config.TokensDir
	config.TokensDir = t.TempDir()
	t.Cleanup(func() { config.TokensDir = old })
	if err := vault.LoadFolders(); err != nil {
		t.Fatal(err)
	}
	conf := "[x-trozos]\ntype = chunker\nremote = nube:Fotos\n\n[x-cifrada]\ntype = crypt\nremote = x-trozos:\npassword = abc\n"
	if _, err := vault.SaveFolder(vault.Folder{Name: "Fotos", Config: conf}); err != nil {
		t.Fatal(err)
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "conflicto", http.StatusConflict)
	}))
	defer up.Close()
	g := newDecryptedGateway(strings.TrimPrefix(up.URL, "http://"), nil, rclonesvc.New(rclonesvc.Options{User: "u", Pass: "p"}))
	put := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/Fotos/a.jpg", strings.NewReader("x"))
		req.SetBasicAuth("u", "p")
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		return rec
	}
	if !sapi.NetworkChangedWithin(netChangeRetryWindow) {
		if rec := put(); rec.Code != http.StatusConflict {
			t.Fatalf("sin cambio de red: %d", rec.Code)
		}
	}
	sapi.ResetConnections("prueba")
	rec := put()
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" || strings.Contains(rec.Body.String(), "conflicto") {
		t.Fatalf("tras cambio de red: %d %q %q", rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())
	}
}
