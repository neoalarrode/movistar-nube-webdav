// Package vault guarda en el proxy la configuración de cifrado de los archivos
// (rclone crypt sobre rclone chunker) para que el propio proxy pueda servirlos ya
// descifrados. Las contraseñas se guardan cifradas con TOKENS_ENCRYPTION_KEY.
package vault

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"movistar-webdav-proxy/internal/config"
	"movistar-webdav-proxy/internal/cryptname"
)

// Crypt es la configuración de cifrado y troceado, compatible con rclone.
type Crypt struct {
	Password    string `json:"password"`  // en claro en memoria; cifrada en disco
	Password2   string `json:"password2"` // sal (puede ir vacía)
	Root        string `json:"root"`      // carpeta(s) cifrada(s) de la raíz de la nube, separadas por comas (p. ej. "datos")
	ChunkSize   string `json:"chunk_size"`
	NameFormat  string `json:"name_format"`
	Transaction string `json:"transactions"`
}

// Defaults son los valores recomendados (los de la documentación del proxy).
func Defaults() Crypt {
	return Crypt{Root: "datos", ChunkSize: "1Gi", NameFormat: "*-chunk###", Transaction: "norename"}
}

var (
	mu      sync.RWMutex
	current *Crypt
)

func file() string { return filepath.Join(config.TokensDir, "crypt.json") }

// Load lee la configuración guardada (si la hay).
func Load() (*Crypt, error) {
	b, err := os.ReadFile(file())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c Crypt
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	c.Password = config.DecryptSecret(c.Password)
	c.Password2 = config.DecryptSecret(c.Password2)
	if c.Password == "" {
		return nil, errors.New("no se pudo leer la clave guardada (¿cambió TOKENS_ENCRYPTION_KEY?)")
	}
	mu.Lock()
	current = &c
	mu.Unlock()
	return &c, nil
}

// Current devuelve la configuración activa o nil.
func Current() *Crypt {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil {
		return nil
	}
	c := *current
	return &c
}

// Save valida y guarda la configuración (contraseñas en claro u ofuscadas por
// rclone, que se detectan y se revelan).
func Save(c Crypt) error {
	c.Password = revealIfObscured(strings.TrimSpace(c.Password))
	c.Password2 = revealIfObscured(strings.TrimSpace(c.Password2))
	if c.Password == "" {
		return errors.New("falta la contraseña de cifrado")
	}
	d := Defaults()
	if c.Root = strings.Trim(strings.TrimSpace(c.Root), "/"); c.Root == "" {
		c.Root = d.Root
	}
	if c.ChunkSize == "" {
		c.ChunkSize = d.ChunkSize
	}
	if c.NameFormat == "" {
		c.NameFormat = d.NameFormat
	}
	if c.Transaction == "" {
		c.Transaction = d.Transaction
	}
	disk := c
	var err error
	if disk.Password, err = config.EncryptSecret(c.Password); err != nil {
		return err
	}
	if disk.Password2, err = config.EncryptSecret(c.Password2); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(disk, "", "  ")
	if err := os.MkdirAll(config.TokensDir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(file()+".tmp", b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(file()+".tmp", file()); err != nil {
		return err
	}
	mu.Lock()
	current = &c
	mu.Unlock()
	return nil
}

// Generate crea una clave nueva al azar (para un almacenamiento nuevo).
func Generate() (Crypt, error) {
	c := Defaults()
	p1, p2 := make([]byte, 32), make([]byte, 32)
	if _, err := rand.Read(p1); err != nil {
		return c, err
	}
	if _, err := rand.Read(p2); err != nil {
		return c, err
	}
	c.Password = base64.RawURLEncoding.EncodeToString(p1)
	c.Password2 = base64.RawURLEncoding.EncodeToString(p2)
	return c, nil
}

// revealIfObscured acepta las contraseñas tal como aparecen en rclone.conf.
func revealIfObscured(v string) string {
	if v == "" {
		return ""
	}
	if r, err := cryptname.Reveal(v); err == nil && r != "" && isPrintable(r) {
		return r
	}
	return v
}

func isPrintable(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == 0xfffd {
			return false
		}
	}
	return true
}

// Names devuelve el descifrador de nombres de la configuración activa, o nil.
func Names() *cryptname.Cipher {
	c := Current()
	if c == nil {
		return nil
	}
	n, err := cryptname.New(c.Password, c.Password2)
	if err != nil {
		return nil
	}
	return n
}

// Projects son las carpetas de la raíz de la nube que van cifradas con esta clave.
func (c Crypt) Projects() []string {
	var out []string
	for _, p := range strings.Split(c.Root, ",") {
		if p = strings.Trim(strings.TrimSpace(p), "/"); p != "" {
			out = append(out, p)
		}
	}
	return out
}
