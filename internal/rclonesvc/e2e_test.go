package rclonesvc

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	xwebdav "golang.org/x/net/webdav"

	"movistar-webdav-proxy/internal/cryptname"
	"movistar-webdav-proxy/internal/vault"
)

// Prueba de punta a punta con rclone de verdad (RCLONE_BINARY): la nube es un
// WebDAV sobre una carpeta local; «datos» va cifrada con su clave, «meloinvento»
// con otra y «Fotos» tal cual. Lo que se sube por el WebDAV descifrado debe quedar
// en la nube cifrado con los mismos nombres que calcula cryptname, y leerse igual.
func TestEndToEnd_RealRclone(t *testing.T) {
	bin := os.Getenv("RCLONE_BINARY")
	if bin == "" {
		t.Skip("RCLONE_BINARY no definido")
	}
	cloud := t.TempDir()
	for _, d := range []string{"datos", "meloinvento", "Fotos", "troceada"} {
		_ = os.Mkdir(filepath.Join(cloud, d), 0o755)
	}
	_ = os.WriteFile(filepath.Join(cloud, "Fotos", "playa.jpg"), []byte("foto"), 0o644)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	srv := &http.Server{Handler: &xwebdav.Handler{FileSystem: xwebdav.Dir(cloud), LockSystem: xwebdav.NewMemLS()}}
	go srv.Serve(ln)
	defer srv.Close()

	old := os.Getenv("TOKENS_DIR")
	_ = old
	l2, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l2.Addr().String()
	l2.Close()
	folders := []vault.Folder{
		{Name: "datos", Config: vault.StandardConfig(vault.Crypt{Password: "clave-datos", Password2: "sal-datos"})},
		{Name: "meloinvento", Config: vault.StandardConfig(vault.Crypt{Password: "otra-clave"})},
		{Name: "troceada", Config: vault.StandardConfig(vault.Crypt{Password: "clave-trozos", ChunkSize: "1Mi"})},
	}
	s := New(Options{Binary: bin, UpstreamURL: "http://" + ln.Addr().String(), Addr: addr, ConfigDir: t.TempDir()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.supervise(ctx, folders)
	s.WaitReady(ctx)
	time.Sleep(500 * time.Millisecond)

	do := func(method, p string, body []byte) (int, string) {
		req, _ := http.NewRequest(method, "http://"+addr+p, bytes.NewReader(body))
		if method == "PROPFIND" {
			req.Header.Set("Depth", "1")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	content := bytes.Repeat([]byte("película "), 1000)
	if code, _ := do("MKCOL", "/datos/Películas", nil); code >= 300 {
		t.Fatalf("MKCOL %d", code)
	}
	if code, b := do("PUT", "/datos/Películas/Mr. Robot.mkv", content); code >= 300 {
		t.Fatalf("PUT %d %s", code, b)
	}
	if code, _ := do("PUT", "/meloinvento/nota.txt", []byte("hola")); code >= 300 {
		t.Fatalf("PUT meloinvento %d", code)
	}

	// En la nube: nombres cifrados exactamente como los calcula cryptname.
	ce, _ := cryptname.New("clave-datos", "sal-datos")
	encDir := ce.EncryptSegment("Películas")
	encFile := ce.EncryptSegment("Mr. Robot.mkv")
	if _, err := os.Stat(filepath.Join(cloud, "datos", encDir, encFile)); err != nil {
		entries, _ := filepath.Glob(filepath.Join(cloud, "datos", "*", "*"))
		t.Fatalf("no está %s/%s en la nube; hay %v", encDir, encFile, entries)
	}
	raw, _ := os.ReadFile(filepath.Join(cloud, "datos", encDir, encFile))
	if bytes.Contains(raw, []byte("película")) {
		t.Fatal("el contenido debe ir cifrado en la nube")
	}
	cm, _ := cryptname.New("otra-clave", "")
	if _, err := os.Stat(filepath.Join(cloud, "meloinvento", cm.EncryptSegment("nota.txt"))); err != nil {
		t.Fatal("meloinvento debe ir cifrada con su propia clave")
	}

	// Archivo mayor que el trozo (2 MiB en «meloinvento»... aquí 1Mi en datos2): se trocea.
	big := bytes.Repeat([]byte("0123456789abcdef"), 200000) // 3,2 MB
	if code, b := do("PUT", "/troceada/grande.bin", big); code >= 300 {
		t.Fatalf("PUT grande %d %s", code, b)
	}
	ct, _ := cryptname.New("clave-trozos", "")
	chunks, _ := filepath.Glob(filepath.Join(cloud, "troceada", ct.EncryptSegment("grande.bin")+"-chunk*"))
	if len(chunks) != 4 {
		all, _ := filepath.Glob(filepath.Join(cloud, "troceada", "*"))
		t.Fatalf("esperados 4 trozos de 1 MiB, hay %v", all)
	}
	if code, b := do("GET", "/troceada/grande.bin", nil); code != 200 || b != string(big) {
		t.Fatalf("GET grande %d, %d bytes", code, len(b))
	}

	// Por el WebDAV descifrado: se lee igual y la raíz muestra las tres carpetas.
	if code, b := do("GET", "/datos/Películas/Mr. Robot.mkv", nil); code != 200 || b != string(content) {
		t.Fatalf("GET %d, %d bytes", code, len(b))
	}
	if code, b := do("GET", "/Fotos/playa.jpg", nil); code != 200 || b != "foto" {
		t.Fatalf("carpeta tal cual: %d %q", code, b)
	}
	_, list := do("PROPFIND", "/", nil)
	for _, d := range []string{"datos", "meloinvento", "Fotos"} {
		if !strings.Contains(list, "/"+d+"/") {
			t.Errorf("la raíz no muestra %s:\n%s", d, list)
		}
	}
}
