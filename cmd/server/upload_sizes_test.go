package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"movistar-webdav-proxy/internal/cryptname"
	"movistar-webdav-proxy/internal/rcloneconf"
)

func TestParseRcloneSize(t *testing.T) {
	for in, want := range map[string]int64{"1Gi": 1 << 30, "1G": 1 << 30, "512M": 512 << 20, "100k": 100 << 10, "2": 2 << 10, "1.5Mi": 3 << 19, "512B": 512, "100Mi": 100 << 20, "1GiB": 1 << 30} {
		if got, err := rcloneconf.ParseSize(in); err != nil || got != want {
			t.Errorf("%s = %d (%v), esperado %d", in, got, err, want)
		}
	}
}

// Con rclone de verdad (RCLONE_BINARY): los tamaños calculados coinciden con los
// objetos que escribe crypt sobre chunker, en archivos de uno y varios trozos.
func TestUploadSizer_MatchesRealRclone(t *testing.T) {
	bin := os.Getenv("RCLONE_BINARY")
	if bin == "" {
		t.Skip("RCLONE_BINARY no definido")
	}
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "nube")
	_ = os.MkdirAll(src, 0o755)
	_ = os.MkdirAll(dst, 0o755)
	sizes := map[string]int64{"vacio": 0, "uno": 1, "bloque": 65536, "bloque1": 65537, "casi": 1<<20 - 100, "justo": 1<<20 - 64, "tres": 3*(1<<20) + 12345}
	for n, s := range sizes {
		if err := os.WriteFile(filepath.Join(src, n), make([]byte, s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pass, _ := cryptname.Obscure("clave")
	conf := "[nube]\ntype = local\n\n[trozos]\ntype = chunker\nremote = nube:" + dst + "\nchunk_size = 1Mi\nname_format = *-chunk###\nhash_type = none\ntransactions = norename\nmeta_format = simplejson\n\n[cifrada]\ntype = crypt\nremote = trozos:\npassword = " + pass + "\n"
	cf := filepath.Join(dir, "r.conf")
	_ = os.WriteFile(cf, []byte(conf), 0o600)
	if out, err := exec.Command(bin, "copy", src, "cifrada:", "--config", cf).CombinedOutput(); err != nil {
		t.Fatalf("rclone: %v %s", err, out)
	}
	chain := conf[strings.Index(conf, "[trozos]"):] // sin la sección de la nube (local en la prueba)
	ch, err := rcloneconf.Remap(chain, "cifrada", "nube:x", "")
	if err != nil {
		t.Fatal(err)
	}
	sz := newUploadSizer(ch)
	c, _ := cryptname.New("clave", "")
	entries, _ := os.ReadDir(dst)
	checked := 0
	for n, s := range sizes {
		base := c.EncryptSegment(n)
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), base) {
				continue
			}
			fi, _ := e.Info()
			got := sz.sizeOf(s, base, e.Name())
			if e.Name() == base && cryptEncryptedSize(s) > 1<<20 {
				if got != -1 {
					t.Errorf("%s: el objeto de metadatos no se debe calcular (%d)", n, got)
				}
				checked++
				continue
			}
			if got != fi.Size() {
				t.Errorf("%s (%d B) → %s: calculado %d, real %d", n, s, e.Name(), got, fi.Size())
			}
			// Sin el sufijo de la transacción también.
			if i := strings.LastIndex(e.Name(), "_"); strings.Contains(e.Name(), "-chunk") && i > 0 && sz.sizeOf(s, base, e.Name()[:i]) != fi.Size() {
				t.Errorf("%s: nombre sin sufijo no reconocido", e.Name())
			}
			checked++
		}
	}
	if checked < len(sizes) {
		t.Fatalf("solo se comprobaron %d objetos: %v", checked, entries)
	}
	// La configuración de producción (1Gi) también se interpreta.
	prod := strings.Replace(chain, "1Mi", "1Gi", 1)
	ch2, _ := rcloneconf.Remap(prod, "cifrada", "nube:datos", "")
	if p := newUploadSizer(ch2); p == nil || p.chunkSize != 1<<30 || !p.crypt {
		t.Fatalf("producción: %+v", p)
	}
}
