package rcloneconf

import (
	"strings"
	"testing"
)

const pasted = `
[movistar]
type = webdav
url = http://127.0.0.1:5050
vendor = owncloud
user = x
pass = y

[movistar-chunk]
type = chunker
chunk_size = 1Gi
remote = movistar:datos
name_format = *-chunk###

[movistar-crypt]
type = crypt
password = AAA
password2 = BBB
remote = movistar-chunk:
filename_encryption = standard
directory_name_encryption = true
`

func TestRemap_ReattachesChainToProxyFolder(t *testing.T) {
	ch, err := Remap(pasted, "", "nube:datos", "c0-")
	if err != nil {
		t.Fatal(err)
	}
	if ch.Top != "c0-movistar-crypt" || strings.Join(ch.Types, ",") != "crypt,chunker" {
		t.Fatalf("cadena: %+v", ch)
	}
	out := ch.String()
	for _, want := range []string{"[c0-movistar-crypt]", "remote = c0-movistar-chunk:", "[c0-movistar-chunk]", "remote = nube:datos", "password = AAA"} {
		if !strings.Contains(out, want) {
			t.Errorf("falta %q en:\n%s", want, out)
		}
	}
	if strings.Contains(out, "[c0-movistar]") || strings.Contains(out, "url =") {
		t.Errorf("el WebDAV de la base no debe copiarse:\n%s", out)
	}
	if c := ch.Crypt(); c == nil || c.Get("password2") != "BBB" {
		t.Error("no encuentra la sección crypt")
	}
}

func TestRemap_ErrorsAreClear(t *testing.T) {
	if _, err := Remap("[a]\ntype = crypt\n", "", "nube:x", "p-"); err == nil {
		t.Error("sin remote debe fallar")
	}
	if _, err := Remap("basura", "", "nube:x", "p-"); err == nil {
		t.Error("texto no válido debe fallar")
	}
	if _, err := Remap(pasted, "noexiste", "nube:x", "p-"); err == nil {
		t.Error("remoto inexistente debe fallar")
	}
}

// Solo se admiten tipos que transforman lo que va a la nube.
func TestRemap_RejectsLocalTypes(t *testing.T) {
	conf := "[c]\ntype = cache\nremote = nube:\nchunk_path = /app/tokens\n"
	if _, err := Remap(conf, "c", "nube:datos", ""); err == nil || !strings.Contains(err.Error(), "cache") {
		t.Fatalf("debía rechazar el tipo cache: %v", err)
	}
}
