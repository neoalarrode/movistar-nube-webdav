package vault

import (
	"os"
	"testing"

	"movistar-webdav-proxy/internal/config"
	"movistar-webdav-proxy/internal/cryptname"
)

func TestSaveLoad_EncryptedAndObscuredInput(t *testing.T) {
	old := config.TokensDir
	config.TokensDir = t.TempDir()
	t.Cleanup(func() { config.TokensDir = old })
	t.Setenv("TOKENS_ENCRYPTION_KEY", "llave-de-prueba")

	obs, _ := cryptname.Obscure("mi clave")
	if err := Save(Crypt{Password: obs, Password2: "sal"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(file())
	if string(b) == "" || containsAny(string(b), "mi clave", "sal\"") {
		t.Fatalf("las contraseñas no deben quedar en claro en disco: %s", b)
	}
	mu.Lock()
	current = nil
	mu.Unlock()
	c, err := Load()
	if err != nil || c == nil || c.Password != "mi clave" || c.Password2 != "sal" || c.Root != "datos" || c.NameFormat != "*-chunk###" {
		t.Fatalf("Load = %+v, %v", c, err)
	}
}

func TestGenerate(t *testing.T) {
	a, _ := Generate()
	b, _ := Generate()
	if a.Password == "" || a.Password == b.Password || a.Password2 == b.Password2 {
		t.Fatal("las claves generadas deben ser distintas y no vacías")
	}
}

func containsAny(s string, subs ...string) bool {
	for _, x := range subs {
		for i := 0; i+len(x) <= len(s); i++ {
			if s[i:i+len(x)] == x {
				return true
			}
		}
	}
	return false
}

func TestFolders_SaveLoadAndMigrate(t *testing.T) {
	old := config.TokensDir
	config.TokensDir = t.TempDir()
	t.Cleanup(func() { config.TokensDir = old })
	t.Setenv("TOKENS_ENCRYPTION_KEY", "llave")

	// Configuración antigua (una clave para «datos») → se convierte al cargar.
	if err := Save(Crypt{Password: "clave", Password2: "sal", Root: "datos"}); err != nil {
		t.Fatal(err)
	}
	if err := LoadFolders(); err != nil {
		t.Fatal(err)
	}
	fs := Folders()
	if len(fs) != 1 || fs[0].Name != "datos" || NamesFor(fs[0]) == nil {
		t.Fatalf("migración: %+v", fs)
	}
	// Otra carpeta con su propia clave pegada.
	cfg := StandardConfig(Crypt{Password: "otra", Password2: "sal2"})
	if _, err := SaveFolder(Folder{Name: "meloinvento", Config: cfg}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(foldersFile())
	if containsAny(string(b), "[cifrada]", "chunk_size") {
		t.Fatal("la configuración debe ir cifrada en disco")
	}
	if err := LoadFolders(); err != nil || len(Folders()) != 2 {
		t.Fatalf("recarga: %v %+v", err, Folders())
	}
	a, b2 := NamesFor(Folders()[0]), NamesFor(Folders()[1])
	if a == nil || b2 == nil || a.EncryptSegment("x") == b2.EncryptSegment("x") {
		t.Fatal("cada carpeta debe descifrar con su propia clave")
	}
}
