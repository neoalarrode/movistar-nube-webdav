// Package cryptname descifra los nombres de archivos y carpetas cifrados por
// rclone crypt (filename_encryption = standard, base32), para mostrarlos legibles en
// el panel. Solo descifra nombres: el contenido nunca pasa por aquí.
package cryptname

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"

	"golang.org/x/crypto/scrypt"
)

// defaultSalt es la sal de rclone crypt cuando password2 está vacía.
var defaultSalt = []byte{0xA8, 0x0D, 0xF4, 0x3A, 0x8F, 0xBD, 0x03, 0x08, 0xA7, 0xCA, 0xB8, 0x3E, 0x58, 0x1F, 0x86, 0xB1}

// obscureKey es la clave fija con la que rclone «ofusca» las contraseñas en rclone.conf.
var obscureKey = []byte{
	0x9c, 0x93, 0x5b, 0x48, 0x73, 0x0a, 0x55, 0x4d,
	0x6b, 0xfd, 0x7c, 0x63, 0xc8, 0x86, 0xa9, 0x2b,
	0xd3, 0x90, 0x19, 0x8e, 0xb8, 0x12, 0x8a, 0xfb,
	0xf4, 0xde, 0x16, 0x2b, 0x8b, 0x95, 0xf6, 0x38,
}

var b32 = base32.HexEncoding.WithPadding(base32.NoPadding)

// Obscure ofusca una contraseña como «rclone obscure» (para escribir rclone.conf).
func Obscure(plain string) (string, error) {
	block, err := aes.NewCipher(obscureKey)
	if err != nil {
		return "", err
	}
	buf := make([]byte, aes.BlockSize+len(plain))
	if _, err := rand.Read(buf[:aes.BlockSize]); err != nil {
		return "", err
	}
	cipher.NewCTR(block, buf[:aes.BlockSize]).XORKeyStream(buf[aes.BlockSize:], []byte(plain))
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Reveal deshace la ofuscación de rclone (rclone reveal).
func Reveal(obscured string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(obscured)
	if err != nil {
		return "", err
	}
	if len(b) < aes.BlockSize {
		return "", errors.New("contraseña ofuscada demasiado corta")
	}
	block, err := aes.NewCipher(obscureKey)
	if err != nil {
		return "", err
	}
	iv, buf := b[:aes.BlockSize], b[aes.BlockSize:]
	out := make([]byte, len(buf))
	cipher.NewCTR(block, iv).XORKeyStream(out, buf)
	return string(out), nil
}

// Cipher descifra nombres con las claves de un remoto crypt.
type Cipher struct {
	block cipher.Block
	tweak []byte
}

// New crea el descifrador a partir de password y password2 en claro (como rclone).
func New(password, password2 string) (*Cipher, error) {
	salt := defaultSalt
	if password2 != "" {
		salt = []byte(password2)
	}
	key := make([]byte, 80)
	if password != "" {
		var err error
		if key, err = scrypt.Key([]byte(password), salt, 16384, 8, 1, 80); err != nil {
			return nil, err
		}
	}
	block, err := aes.NewCipher(key[32:64])
	if err != nil {
		return nil, err
	}
	return &Cipher{block: block, tweak: key[64:80]}, nil
}

// DecryptSegment descifra un componente de ruta.
func (c *Cipher) DecryptSegment(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	raw, err := b32.DecodeString(strings.ToUpper(s))
	if err != nil {
		return "", err
	}
	if len(raw) == 0 || len(raw)%aes.BlockSize != 0 {
		return "", errors.New("longitud no válida")
	}
	plain := emeTransform(c.block, c.tweak, raw, false)
	pad := int(plain[len(plain)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(plain) {
		return "", errors.New("relleno no válido")
	}
	for _, b := range plain[len(plain)-pad:] {
		if int(b) != pad {
			return "", errors.New("relleno no válido")
		}
	}
	return string(plain[:len(plain)-pad]), nil
}

// EncryptSegment cifra un componente (para pruebas).
func (c *Cipher) EncryptSegment(s string) string {
	if s == "" {
		return ""
	}
	pad := aes.BlockSize - len(s)%aes.BlockSize
	in := append([]byte(s), []byte(strings.Repeat(string(rune(pad)), pad))...)
	return strings.ToLower(b32.EncodeToString(emeTransform(c.block, c.tweak, in, true)))
}

// chunkSuffix: sufijos de rclone chunker («-chunk001», «-chunk001_ab12cd») y de
// sus temporales, que van después del nombre cifrado.
var chunkSuffix = regexp.MustCompile(`(-chunk\d{3,})(_[0-9a-z]{4,})?$`)

// DecryptPath descifra cada componente de una ruta WebDAV a partir de root (la
// carpeta del remoto crypt, p. ej. "/datos"). Lo que no se puede descifrar se deja
// tal cual. Devuelve "" si la ruta no está bajo root.
func (c *Cipher) DecryptPath(p, root string) string {
	root = "/" + strings.Trim(root, "/")
	if root == "/" {
		root = ""
	}
	if root != "" && !strings.HasPrefix(p, root+"/") {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(p, root+"/"), "/")
	for i, part := range parts {
		suffix := ""
		if i == len(parts)-1 {
			if m := chunkSuffix.FindStringSubmatchIndex(part); m != nil {
				suffix, part = part[m[0]:], part[:m[0]]
			}
		}
		if dec, err := c.DecryptSegment(part); err == nil {
			parts[i] = dec
		} else {
			parts[i] = part
		}
		if suffix != "" {
			parts[i] += " (" + strings.TrimPrefix(chunkSuffix.FindStringSubmatch(suffix)[1], "-") + ")"
		}
	}
	return strings.Join(parts, "/")
}

// emeTransform es el modo EME (ECB-Mix-ECB) que usa rclone crypt para los nombres.
func emeTransform(bc cipher.Block, tweak, in []byte, encrypt bool) []byte {
	aesT := func(dst, src []byte) {
		if encrypt {
			bc.Encrypt(dst, src)
		} else {
			bc.Decrypt(dst, src)
		}
	}
	m := len(in) / 16
	out := make([]byte, len(in))
	lTable := make([][]byte, m)
	li := make([]byte, 16)
	bc.Encrypt(li, make([]byte, 16))
	for i := 0; i < m; i++ {
		multByTwo(li, li)
		lTable[i] = append([]byte(nil), li...)
	}
	ppj := make([]byte, 16)
	for j := 0; j < m; j++ {
		xorBlocks(ppj, in[j*16:(j+1)*16], lTable[j])
		aesT(out[j*16:(j+1)*16], ppj)
	}
	mp := make([]byte, 16)
	xorBlocks(mp, out[0:16], tweak)
	for j := 1; j < m; j++ {
		xorBlocks(mp, mp, out[j*16:(j+1)*16])
	}
	mc := make([]byte, 16)
	aesT(mc, mp)
	mm := make([]byte, 16)
	xorBlocks(mm, mp, mc)
	for j := 1; j < m; j++ {
		multByTwo(mm, mm)
		xorBlocks(out[j*16:(j+1)*16], out[j*16:(j+1)*16], mm)
	}
	ccc1 := make([]byte, 16)
	xorBlocks(ccc1, mc, tweak)
	for j := 1; j < m; j++ {
		xorBlocks(ccc1, ccc1, out[j*16:(j+1)*16])
	}
	copy(out[0:16], ccc1)
	for j := 0; j < m; j++ {
		blk := out[j*16 : (j+1)*16]
		aesT(blk, blk)
		xorBlocks(blk, blk, lTable[j])
	}
	return out
}

func multByTwo(out, in []byte) {
	tmp := make([]byte, 16)
	tmp[0] = 2 * in[0]
	if in[15] >= 128 {
		tmp[0] ^= 135
	}
	for j := 1; j < 16; j++ {
		tmp[j] = 2 * in[j]
		if in[j-1] >= 128 {
			tmp[j]++
		}
	}
	copy(out, tmp)
}

func xorBlocks(out, a, b []byte) {
	for i := range out {
		out[i] = a[i] ^ b[i]
	}
}
