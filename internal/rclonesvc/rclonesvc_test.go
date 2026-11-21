package rclonesvc

import (
	"os"
	"strings"
	"testing"

	"movistar-webdav-proxy/internal/autochunk"
	"movistar-webdav-proxy/internal/config"
	"movistar-webdav-proxy/internal/cryptname"
	"movistar-webdav-proxy/internal/vault"
)

// Cada carpeta lleva su configuración pegada (con su clave) enganchada a
// «nube:<carpeta>»; el resto de la raíz va tal cual.
func TestWriteConfig_PerFolder(t *testing.T) {
	s := New(Options{UpstreamURL: "http://127.0.0.1:8080", User: "u", Pass: "p", ConfigDir: t.TempDir()})
	pasted := "[movistar]\ntype = webdav\nurl = http://x:5050\n\n[movistar-chunk]\ntype = chunker\nremote = movistar:datos\nchunk_size = 1Gi\nname_format = *-chunk###\nhash_type = none\ntransactions = norename\n\n[movistar-crypt]\ntype = crypt\nremote = movistar-chunk:\npassword = " + mustObscure(t, "clave") + "\n"
	folders := []vault.Folder{
		{Name: "datos", Config: pasted},
		{Name: "meloinvento", Config: vault.StandardConfig(vault.Crypt{Password: "otra"})},
	}
	path, err := s.writeConfig(folders, []string{"datos", "meloinvento", "Fotos del móvil"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	conf := string(b)
	for _, want := range []string{"[nube]", "url = http://127.0.0.1:8080",
		"[c0-movistar-chunk]", "remote = nube:datos", "name_format = *-chunk###", "transactions = norename",
		"[c0-movistar-crypt]", "remote = c0-movistar-chunk:",
		"[c1-trozos]", "remote = nube:meloinvento", "[c1-cifrada]", "remote = c1-trozos:",
		"[raiz]", "type = combine", `upstreams = "datos=c0-movistar-crypt:" "meloinvento=c1-cifrada:" "Fotos del móvil=nube:Fotos del móvil"`} {
		if !strings.Contains(conf, want) {
			t.Errorf("falta %q en:\n%s", want, conf)
		}
	}
	if strings.Contains(conf, "[c0-movistar]") || strings.Contains(conf, "x:5050") {
		t.Fatal("la sección WebDAV pegada no debe usarse")
	}
	if strings.Contains(conf, "= clave") || strings.Contains(conf, "= otra") {
		t.Fatal("las contraseñas deben ir ofuscadas como en rclone.conf")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("permisos %v, esperados 0600", fi.Mode().Perm())
	}
	// Sin carpetas en la nube ni configuraciones: la raíz tal cual.
	path, _ = s.writeConfig(nil, nil)
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "type = alias\nremote = nube:") {
		t.Fatalf("raíz vacía:\n%s", b)
	}
}

func mustObscure(t *testing.T, v string) string {
	o, err := cryptname.Obscure(v)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// El montaje usa por defecto las mismas opciones que el de producción.
func TestMountArgs_DefaultsMatchProduction(t *testing.T) {
	s := New(Options{MountPath: "/mnt/nube"})
	got := strings.Join(s.mountArgs("/tmp/r.conf"), " ")
	want := "mount raiz: /mnt/nube --config /tmp/r.conf --allow-other --vfs-cache-mode writes --vfs-cache-max-size 30G --vfs-cache-max-age 1h --cache-dir /app/cache/rclone --vfs-read-chunk-size 32M --vfs-read-chunk-size-limit 256M --dir-cache-time 1000h --attr-timeout 1h --umask 022 --rc --rc-addr 127.0.0.1:5573 --rc-no-auth"
	if got != want {
		t.Errorf("args:\n%s\nesperado:\n%s", got, want)
	}
	s2 := New(Options{MountPath: "/x", MountArgs: []string{"--vfs-cache-mode", "off"}})
	if g := strings.Join(s2.mountArgs("c"), " "); g != "mount raiz: /x --config c --vfs-cache-mode off" {
		t.Errorf("WEBDAV_MOUNT_ARGS no sustituye: %s", g)
	}
}

// Si Movistar rechaza las subidas grandes, el rclone interno trocea más pequeño.
func TestWriteConfig_AutoChunkCap(t *testing.T) {
	old := config.TokensDir
	config.TokensDir = t.TempDir()
	t.Cleanup(func() { config.TokensDir = old; autochunk.Reset() })
	autochunk.Load()
	autochunk.Rejected(1<<30, 1<<30)
	autochunk.Rejected(1<<30, 1<<30)
	s := New(Options{UpstreamURL: "http://127.0.0.1:8080", ConfigDir: t.TempDir()})
	path, err := s.writeConfig([]vault.Folder{{Name: "datos", Config: vault.StandardConfig(vault.Crypt{Password: "x"})}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "chunk_size = 95Mi") {
		t.Fatalf("esperado trozo de 95 MiB:\n%s", b)
	}
}

// El rclone interno no hereda variables RCLONE_* (rclone las toma como opciones).
func TestRcloneEnv_DropsRcloneVars(t *testing.T) {
	t.Setenv("RCLONE_CRYPT_PASSWORD2", "sal-equivocada")
	t.Setenv("OTRA", "1")
	env := strings.Join(rcloneEnv("/tmp/r.conf"), "\\n")
	if strings.Contains(env, "RCLONE_CRYPT_PASSWORD2") || !strings.Contains(env, "OTRA=1") || !strings.Contains(env, "RCLONE_CONFIG=/tmp/r.conf") {
		t.Fatalf("entorno: %s", env)
	}
}
