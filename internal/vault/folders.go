package vault

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"movistar-webdav-proxy/internal/config"
	"movistar-webdav-proxy/internal/cryptname"
	"movistar-webdav-proxy/internal/rcloneconf"
)

// Folder es la configuración de rclone de una carpeta de la raíz de la nube (p. ej.
// «datos»): el texto tal cual de rclone.conf (crypt, chunker...) y el remoto de
// arriba. Las carpetas sin configuración se sirven tal cual.
type Folder struct {
	Name   string `json:"name"`
	Config string `json:"config"` // cifrado en disco con TOKENS_ENCRYPTION_KEY
	Top    string `json:"top,omitempty"`
}

var folders map[string]Folder

func foldersFile() string { return filepath.Join(config.TokensDir, "folders.json") }

// LoadFolders lee las configuraciones por carpeta. Si solo existe la configuración
// antigua (crypt.json, una clave para una carpeta), la convierte.
func LoadFolders() error {
	mu.Lock()
	defer mu.Unlock()
	folders = map[string]Folder{}
	b, err := os.ReadFile(foldersFile())
	if errors.Is(err, os.ErrNotExist) {
		mu.Unlock()
		old, lerr := Load()
		mu.Lock()
		if lerr != nil || old == nil {
			return lerr
		}
		for _, p := range old.Projects() {
			folders[p] = Folder{Name: p, Config: StandardConfig(*old)}
		}
		return saveFoldersLocked()
	}
	if err != nil {
		return err
	}
	var list []Folder
	if err := json.Unmarshal(b, &list); err != nil {
		return err
	}
	for _, f := range list {
		f.Config = config.DecryptSecret(f.Config)
		if f.Config == "" {
			return fmt.Errorf("no se pudo leer la configuración de «%s» (¿cambió TOKENS_ENCRYPTION_KEY?)", f.Name)
		}
		folders[f.Name] = f
	}
	return nil
}

func saveFoldersLocked() error {
	var list []Folder
	for _, f := range folders {
		enc, err := config.EncryptSecret(f.Config)
		if err != nil {
			return err
		}
		list = append(list, Folder{Name: f.Name, Config: enc, Top: f.Top})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	b, _ := json.MarshalIndent(list, "", "  ")
	if err := os.MkdirAll(config.TokensDir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(foldersFile()+".tmp", b, 0o600); err != nil {
		return err
	}
	return os.Rename(foldersFile()+".tmp", foldersFile())
}

// Folders devuelve las carpetas configuradas, ordenadas por nombre.
func Folders() []Folder {
	mu.RLock()
	defer mu.RUnlock()
	var out []Folder
	for _, f := range folders {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SaveFolder valida y guarda la configuración de una carpeta de la raíz.
func SaveFolder(f Folder) (*rcloneconf.Chain, error) {
	f.Name = strings.Trim(strings.TrimSpace(f.Name), "/")
	if f.Name == "" || strings.Contains(f.Name, "/") {
		return nil, errors.New("indica una carpeta de la raíz (sin «/»)")
	}
	ch, err := rcloneconf.Remap(f.Config, f.Top, "nube:"+f.Name, "x-")
	if err != nil {
		return nil, err
	}
	mu.Lock()
	defer mu.Unlock()
	if folders == nil {
		folders = map[string]Folder{}
	}
	folders[f.Name] = f
	return ch, saveFoldersLocked()
}

// DeleteFolder quita la configuración de una carpeta (pasa a servirse tal cual).
func DeleteFolder(name string) error {
	mu.Lock()
	defer mu.Unlock()
	delete(folders, name)
	return saveFoldersLocked()
}

// StandardConfig es la configuración recomendada (crypt sobre chunker) con las
// contraseñas de c, en formato rclone.conf.
func StandardConfig(c Crypt) string {
	ob := func(v string) string {
		o, _ := cryptname.Obscure(v)
		return o
	}
	d := Defaults()
	if c.ChunkSize == "" {
		c.ChunkSize = d.ChunkSize
	}
	if c.NameFormat == "" {
		c.NameFormat = d.NameFormat
	}
	if c.Transaction == "" {
		c.Transaction = d.Transaction
	}
	s := fmt.Sprintf("[trozos]\ntype = chunker\nremote = nube:\nchunk_size = %s\nname_format = %s\ntransactions = %s\nhash_type = none\nmeta_format = simplejson\n\n"+
		"[cifrada]\ntype = crypt\nremote = trozos:\nfilename_encryption = standard\ndirectory_name_encryption = true\npassword = %s\n",
		c.ChunkSize, c.NameFormat, c.Transaction, ob(c.Password))
	if c.Password2 != "" {
		s += "password2 = " + ob(c.Password2) + "\n"
	}
	return s
}

// NamesFor devuelve el descifrador de nombres de una carpeta (si su cadena tiene
// un crypt con cifrado de nombres estándar), o nil.
func NamesFor(f Folder) *cryptname.Cipher {
	ch, err := rcloneconf.Remap(f.Config, f.Top, "nube:"+f.Name, "x-")
	if err != nil {
		return nil
	}
	cs := ch.Crypt()
	if cs == nil || (cs.Get("filename_encryption") != "" && cs.Get("filename_encryption") != "standard") {
		return nil
	}
	// En rclone.conf las contraseñas van siempre ofuscadas y rclone las revela sin
	// más: igual aquí (adivinar si están ofuscadas podía dar otra clave).
	p, err := cryptname.Reveal(cs.Get("password"))
	if err != nil || p == "" {
		return nil
	}
	var p2 string
	if v := cs.Get("password2"); v != "" {
		if p2, err = cryptname.Reveal(v); err != nil {
			return nil
		}
	}
	n, err := cryptname.New(p, p2)
	if err != nil {
		return nil
	}
	return n
}
