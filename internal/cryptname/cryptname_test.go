package cryptname

import "testing"

// Vectores de la batería de pruebas de rclone crypt (contraseña vacía).
func TestDecryptSegment_RcloneVectors(t *testing.T) {
	c, err := New("", "")
	if err != nil {
		t.Fatal(err)
	}
	for plain, enc := range map[string]string{
		"1":   "p0e52nreeaj0a5ea7s64m4j72s",
		"12":  "l42g6771hnv3an9cgc8cr2n1ng",
		"123": "qgm4avr35m5loi1th53ato71v0",
	} {
		if got := c.EncryptSegment(plain); got != enc {
			t.Errorf("cifrar %q = %q, esperado %q", plain, got, enc)
		}
		if got, err := c.DecryptSegment(enc); err != nil || got != plain {
			t.Errorf("descifrar %q = %q, %v; esperado %q", enc, got, err, plain)
		}
	}
}

func TestDecryptPath_WithChunkSuffix(t *testing.T) {
	c, _ := New("contraseña", "sal")
	enc := "/datos/" + c.EncryptSegment("Series") + "/" + c.EncryptSegment("T1") + "/" + c.EncryptSegment("E01.mkv") + "-chunk002_ab12cd"
	if got := c.DecryptPath(enc, "/datos"); got != "Series/T1/E01.mkv (chunk002)" {
		t.Errorf("DecryptPath = %q", got)
	}
	if got := c.DecryptPath("/otra/cosa", "/datos"); got != "" {
		t.Errorf("fuera de la raíz debe dar vacío: %q", got)
	}
}

// Vectores de la batería de pruebas de rclone obscure.
func TestReveal_RcloneVectors(t *testing.T) {
	for in, want := range map[string]string{
		"YWFhYWFhYWFhYWFhYWFhYQ":         "",
		"YWFhYWFhYWFhYWFhYWFhYXMaGgIlEQ": "potato",
		"YmJiYmJiYmJiYmJiYmJiYp3gcEWbAw": "potato",
	} {
		if got, err := Reveal(in); err != nil || got != want {
			t.Errorf("Reveal(%q) = %q, %v; esperado %q", in, got, err, want)
		}
	}
}

func TestObscureReveal_RoundTrip(t *testing.T) {
	o, err := Obscure("clave secreta ñ")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Reveal(o); err != nil || got != "clave secreta ñ" {
		t.Fatalf("ida y vuelta: %q, %v", got, err)
	}
}
