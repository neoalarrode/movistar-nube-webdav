// Package rcloneconf interpreta configuraciones de rclone pegadas tal cual
// (formato rclone.conf) para usarlas por carpeta dentro del proxy: la cadena de
// remotos (crypt, chunker, compress...) se reengancha al WebDAV en bruto del proxy
// en la carpeta correspondiente de la nube.
package rcloneconf

import (
	"bufio"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Section es una sección [nombre] de rclone.conf, con sus claves en orden.
type Section struct {
	Name string
	Keys [][2]string
}

// Get devuelve el valor de una clave.
func (s Section) Get(k string) string {
	for _, kv := range s.Keys {
		if kv[0] == k {
			return kv[1]
		}
	}
	return ""
}

func (s *Section) set(k, v string) {
	for i := range s.Keys {
		if s.Keys[i][0] == k {
			s.Keys[i][1] = v
			return
		}
	}
	s.Keys = append(s.Keys, [2]string{k, v})
}

// Parse lee el texto de un rclone.conf.
func Parse(text string) ([]Section, error) {
	var out []Section
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			out = append(out, Section{Name: strings.TrimSpace(line[1 : len(line)-1])})
			continue
		}
		i := strings.Index(line, "=")
		if i < 0 || len(out) == 0 {
			return nil, fmt.Errorf("línea no válida: %q", line)
		}
		out[len(out)-1].Keys = append(out[len(out)-1].Keys, [2]string{strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:])})
	}
	if len(out) == 0 {
		return nil, errors.New("no hay ninguna sección [remoto]")
	}
	return out, nil
}

// splitRemote separa «nombre:ruta».
func splitRemote(v string) (name, path string, ok bool) {
	i := strings.Index(v, ":")
	if i <= 0 {
		return "", "", false
	}
	return v[:i], v[i+1:], true
}

// allowedTypes son los tipos de remoto que se admiten en la cadena de una carpeta:
// transforman lo que va a la nube. Otros (cache, local, union...) leen o escriben
// rutas del propio contenedor (claves, sesión) y no tienen sentido aquí.
var allowedTypes = map[string]bool{"crypt": true, "chunker": true, "compress": true, "hasher": true}

const allowedTypeList = "crypt, chunker, compress y hasher"

// Chain es una cadena de remotos lista para usar sobre una carpeta de la nube.
type Chain struct {
	Top      string    // remoto de arriba (ya con prefijo)
	Sections []Section // secciones renombradas, sin el WebDAV de la base
	Types    []string  // tipos de arriba abajo (p. ej. crypt, chunker)
}

// Remap toma el texto pegado, elige el remoto de arriba (top, o el que ningún otro
// usa si viene vacío), sigue su cadena «remote = …» hasta la base (un WebDAV u otro
// remoto que no esté en el texto) y la sustituye por base (p. ej. «nube:datos»).
// Las secciones se renombran con prefix para no chocar con otras carpetas.
func Remap(text, top, base, prefix string) (*Chain, error) {
	secs, err := Parse(text)
	if err != nil {
		return nil, err
	}
	byName := map[string]*Section{}
	referenced := map[string]bool{}
	for i := range secs {
		byName[secs[i].Name] = &secs[i]
		if n, _, ok := splitRemote(secs[i].Get("remote")); ok {
			referenced[n] = true
		}
	}
	if top == "" {
		var cands []string
		for _, s := range secs {
			if !referenced[s.Name] && s.Get("type") != "webdav" && s.Get("remote") != "" {
				cands = append(cands, s.Name)
			}
		}
		sort.Strings(cands)
		if len(cands) != 1 {
			return nil, fmt.Errorf("indica qué remoto es el de arriba (candidatos: %v)", cands)
		}
		top = cands[0]
	}
	cur, ok := byName[top]
	if !ok {
		return nil, fmt.Errorf("no existe el remoto [%s]", top)
	}
	ch := &Chain{Top: prefix + top}
	seen := map[string]bool{}
	for {
		if seen[cur.Name] {
			return nil, fmt.Errorf("la cadena de remotos tiene un bucle en [%s]", cur.Name)
		}
		seen[cur.Name] = true
		if t := cur.Get("type"); !allowedTypes[t] {
			return nil, fmt.Errorf("[%s] es de tipo %q: solo se admiten %s sobre una carpeta de la nube", cur.Name, t, allowedTypeList)
		}
		s := Section{Name: prefix + cur.Name, Keys: append([][2]string(nil), cur.Keys...)}
		ch.Types = append(ch.Types, cur.Get("type"))
		next, _, ok := splitRemote(cur.Get("remote"))
		if !ok {
			return nil, fmt.Errorf("[%s] no tiene «remote = …»: no se puede enganchar a la nube", cur.Name)
		}
		nextSec, inText := byName[next]
		if !inText || nextSec.Get("type") == "webdav" {
			s.set("remote", base)
			ch.Sections = append(ch.Sections, s)
			return ch, nil
		}
		_, path, _ := splitRemote(cur.Get("remote"))
		s.set("remote", prefix+next+":"+path)
		ch.Sections = append(ch.Sections, s)
		cur = nextSec
	}
}

// String escribe las secciones en formato rclone.conf.
func (c *Chain) String() string {
	var b strings.Builder
	for _, s := range c.Sections {
		fmt.Fprintf(&b, "[%s]\n", s.Name)
		for _, kv := range s.Keys {
			fmt.Fprintf(&b, "%s = %s\n", kv[0], kv[1])
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Crypt devuelve la primera sección crypt de la cadena (para descifrar nombres), o nil.
func (c *Chain) Crypt() *Section {
	for i := range c.Sections {
		if c.Sections[i].Get("type") == "crypt" {
			return &c.Sections[i]
		}
	}
	return nil
}

// CapChunkSize limita a n bytes el chunk_size de los chunker de la cadena (si lo
// tienen mayor). Devuelve si cambió algo.
func (c *Chain) CapChunkSize(n int64, parse func(string) (int64, error)) bool {
	if n <= 0 {
		return false
	}
	changed := false
	for i := range c.Sections {
		s := &c.Sections[i]
		if s.Get("type") != "chunker" {
			continue
		}
		cur := s.Get("chunk_size")
		if cur == "" {
			cur = "2Gi"
		}
		if v, err := parse(cur); err == nil && v <= n {
			continue
		}
		// En MiB si es exacto (rclone lee un número sin sufijo como KiB).
		if n%(1<<20) == 0 {
			s.set("chunk_size", fmt.Sprintf("%dMi", n>>20))
		} else {
			s.set("chunk_size", fmt.Sprintf("%dB", n))
		}
		changed = true
	}
	return changed
}

// ParseSize interpreta un tamaño de rclone («1Gi», «1G», «512M», «100k», «512B»…):
// como en rclone, K/M/G/T son potencias de 1024, «B» son bytes y sin sufijo, KiB.
func ParseSize(v string) (int64, error) {
	m := sizeRe.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return 0, fmt.Errorf("tamaño no válido: %q", v)
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil || f <= 0 {
		return 0, fmt.Errorf("tamaño no válido: %q", v)
	}
	mult := map[string]float64{"": 1 << 10, "K": 1 << 10, "M": 1 << 20, "G": 1 << 30, "T": 1 << 40, "P": 1 << 50}[strings.ToUpper(m[2])]
	if m[2] == "" && m[4] != "" {
		mult = 1 // «512B»: bytes
	}
	return int64(f * mult), nil
}

var sizeRe = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)\s*([KkMmGgTtPp]?)(i?)([Bb]?)$`)
