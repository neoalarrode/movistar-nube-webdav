// Package rclonesvc arranca y vigila un rclone interno que sirve por WebDAV la nube
// entera desde la raíz con cada carpeta según su configuración de rclone guardada
// en el proxy (internal/vault): las que tienen una (crypt, chunker...) se ven ya
// descifradas, cada una con su clave; el resto, tal cual. Lo que suba un cliente
// se cifra, se trocea y pasa por las colas y límites del proxy.
package rclonesvc

import (
	"bufio"
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"movistar-webdav-proxy/internal/autochunk"
	"movistar-webdav-proxy/internal/cryptname"
	"movistar-webdav-proxy/internal/logger"
	"movistar-webdav-proxy/internal/rcloneconf"
	"movistar-webdav-proxy/internal/vault"
)

// Options configura el servicio.
type Options struct {
	Binary      string // ejecutable de rclone (RCLONE_BINARY, por defecto «rclone»)
	UpstreamURL string // WebDAV del propio proxy, p. ej. http://127.0.0.1:8080
	User, Pass  string // credenciales del WebDAV del proxy (y del descifrado)
	Addr        string // dirección del WebDAV descifrado (WEBDAV_DECRYPTED_ADDR, por defecto :8081)
	CacheMode   string // --vfs-cache-mode (WEBDAV_DECRYPTED_CACHE_MODE, por defecto off: subida directa)
	ConfigDir   string // dónde escribir rclone.conf (tmpfs; por defecto /tmp/rclone-proxy)

	// Montaje FUSE de los archivos descifrados (opcional), para que el sistema los
	// vea como una carpeta normal (servidor multimedia, Samba...). Requiere /dev/fuse y SYS_ADMIN.
	MountPath  string   // WEBDAV_MOUNT_PATH, p. ej. /mnt/nube
	MountArgs  []string // WEBDAV_MOUNT_ARGS (sustituye a las opciones por defecto)
	MountCache string   // WEBDAV_MOUNT_CACHE_DIR (por defecto /app/cache/rclone)

	OnRestart func() // al cambiar las configuraciones (p. ej. recargar los descifradores de nombres)
}

// SetRootLister fija cómo listar las carpetas de la raíz sin lanzar rclone (el
// propio listado del proxy). Si devuelve nil, se usa rclone lsf.
func (s *Service) SetRootLister(f func() []string) {
	s.mu.Lock()
	s.rootLister = f
	s.mu.Unlock()
}

// DefaultMountArgs son las opciones del montaje probado en producción.
func DefaultMountArgs(cacheDir string) []string {
	return []string{"--allow-other", "--vfs-cache-mode", "writes", "--vfs-cache-max-size", "30G", "--vfs-cache-max-age", "1h",
		"--cache-dir", cacheDir, "--vfs-read-chunk-size", "32M", "--vfs-read-chunk-size-limit", "256M",
		"--dir-cache-time", "1000h", "--attr-timeout", "1h", "--umask", "022",
		"--rc", "--rc-addr", "127.0.0.1:5573", "--rc-no-auth"}
}

// RCAddr es el control remoto del rclone interno (solo en local).
const RCAddr = "127.0.0.1:8092"

// Forget hace que el rclone interno olvide lo que tiene en caché de un archivo (p. ej.
// tras subirlo: guarda la hora de subida, no la fecha original que ya tiene la nube).
func Forget(file string) error {
	req, err := http.NewRequest(http.MethodPost, "http://"+RCAddr+"/vfs/forget?file="+url.QueryEscape(strings.TrimPrefix(file, "/")), nil)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("vfs/forget: HTTP %d", resp.StatusCode)
	}
	return nil
}

// Service vigila el proceso de rclone.
type Service struct {
	opts    Options
	mu      sync.Mutex
	cancel  context.CancelFunc
	running bool
	lastErr string

	mountRunning bool
	mountErr     string

	rootLister func() []string
}

// New crea el servicio con valores por defecto.
func New(o Options) *Service {
	if o.Binary == "" {
		o.Binary = "rclone"
	}
	if o.Addr == "" {
		o.Addr = ":8081"
	}
	if o.CacheMode == "" {
		o.CacheMode = "off"
	}
	if o.ConfigDir == "" {
		o.ConfigDir = filepath.Join(os.TempDir(), "rclone-proxy")
	}
	if o.MountCache == "" {
		o.MountCache = "/app/cache/rclone"
	}
	return &Service{opts: o}
}

// Status resume el estado para el panel.
func (s *Service) Status() map[string]interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]interface{}{"running": s.running, "addr": s.opts.Addr, "cache_mode": s.opts.CacheMode, "error": s.lastErr,
		"mount_path": s.opts.MountPath, "mount_running": s.mountRunning, "mount_error": s.mountErr}
}

// Restart (re)arranca rclone con las configuraciones por carpeta actuales.
func (s *Service) Restart() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	folders := vault.Folders()
	if s.opts.OnRestart != nil {
		s.opts.OnRestart()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.mu.Unlock()
	go s.supervise(ctx, folders)
	if s.opts.MountPath != "" {
		go s.superviseMount(ctx, folders)
	}
}

// Stop detiene rclone.
func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

// writeConfig escribe un rclone.conf (0600). «nube» es el WebDAV en bruto del
// proxy; cada carpeta configurada de la raíz lleva su cadena de rclone (la pegada,
// enganchada a «nube:<carpeta>»); «raiz» las junta (combine) con el resto de
// carpetas de la raíz tal cual, para servir la nube entera desde la raíz.
func (s *Service) writeConfig(folders []vault.Folder, topDirs []string) (string, error) {
	if err := os.MkdirAll(s.opts.ConfigDir, 0o700); err != nil {
		return "", err
	}
	pass := ""
	if s.opts.Pass != "" {
		pass, _ = cryptname.Obscure(s.opts.Pass)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[nube]\ntype = webdav\nurl = %s\nvendor = owncloud\nuser = %s\npass = %s\n\n", s.opts.UpstreamURL, s.opts.User, pass)
	configured := map[string]bool{}
	var upstreams []string
	for i, f := range folders {
		ch, err := rcloneconf.Remap(f.Config, f.Top, "nube:"+f.Name, fmt.Sprintf("c%d-", i))
		if err != nil {
			logger.Log("[Descifrado] ⚠️ Configuración de «%s» no válida (%v): se sirve tal cual", f.Name, err)
			continue
		}
		// Tamaño de trozo rebajado si Movistar rechaza las subidas grandes (autochunk).
		ch.CapChunkSize(autochunk.Chunk(), rcloneconf.ParseSize)
		configured[f.Name] = true
		b.WriteString(ch.String())
		upstreams = append(upstreams, fmt.Sprintf("%q", fmt.Sprintf("%s=%s:", f.Name, ch.Top)))
	}
	for _, d := range topDirs {
		if d = strings.Trim(d, "/"); d != "" && !configured[d] {
			upstreams = append(upstreams, fmt.Sprintf("%q", fmt.Sprintf("%s=nube:%s", d, d)))
		}
	}
	if len(upstreams) == 0 {
		// Nube vacía y sin carpetas configuradas: la raíz tal cual.
		b.WriteString("[raiz]\ntype = alias\nremote = nube:\n")
	} else {
		fmt.Fprintf(&b, "[raiz]\ntype = combine\nupstreams = %s\n", strings.Join(upstreams, " "))
	}
	path := filepath.Join(s.opts.ConfigDir, "rclone.conf")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// topDirs lista las carpetas de la raíz de la nube (WebDAV en bruto del proxy).
func (s *Service) topDirs(ctx context.Context, folders []vault.Folder) []string {
	s.mu.Lock()
	lister := s.rootLister
	s.mu.Unlock()
	if lister != nil {
		if dirs := lister(); dirs != nil {
			return dirs
		}
	}
	conf, err := s.writeConfig(folders, nil)
	if err != nil {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	lsf := exec.CommandContext(cctx, s.opts.Binary, "lsf", "--dirs-only", "--config", conf, "nube:")
	lsf.Env = rcloneEnv(conf)
	out, err := lsf.Output()
	if err != nil {
		logger.Log("[Descifrado] ⚠️ No se pudieron listar las carpetas de la raíz: %v", err)
		return nil
	}
	var dirs []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.Trim(strings.TrimSpace(l), "/"); l != "" {
			dirs = append(dirs, l)
		}
	}
	return dirs
}

// args son los argumentos de «rclone serve webdav».
func (s *Service) args(conf string) []string {
	a := []string{"serve", "webdav", "raiz:", "--config", conf,
		"--addr", s.opts.Addr, "--vfs-cache-mode", s.opts.CacheMode,
		// Todo lo escribe este mismo rclone, así que su caché de carpetas sigue al día;
		// releer cada minuto obligaba al chunker a volver a leer sus metadatos.
		"--dir-cache-time", "1h", "--log-level", "NOTICE",
		// Búferes con mmap: devuelve antes la memoria al sistema (máquinas pequeñas).
		"--use-mmap",
		// Control remoto local (p. ej. para olvidar una carpeta, ver Forget).
		"--rc", "--rc-addr", RCAddr, "--rc-no-auth"}
	if s.opts.User != "" {
		a = append(a, "--user", s.opts.User, "--pass", s.opts.Pass)
	}
	return a
}

func (s *Service) supervise(ctx context.Context, folders []vault.Folder) {
	backoff := 2 * time.Second
	for ctx.Err() == nil {
		conf, err := s.writeConfig(folders, s.topDirs(ctx, folders))
		if err != nil {
			s.setState(false, err.Error())
			return
		}
		cmd := exec.CommandContext(ctx, s.opts.Binary, s.args(conf)...)
		cmd.Env = rcloneEnv(conf)
		out, _ := cmd.StdoutPipe()
		cmd.Stderr = cmd.Stdout
		if err := cmd.Start(); err != nil {
			s.setState(false, "no se pudo arrancar rclone: "+err.Error())
			logger.Log("[Descifrado] ❌ No se pudo arrancar rclone (%s): %v", s.opts.Binary, err)
			return
		}
		s.setState(true, "")
		logger.Log("[Descifrado] 🔓 WebDAV en %s: la nube desde la raíz; con configuración propia %v, el resto tal cual (caché %s)", s.opts.Addr, folderNames(folders), s.opts.CacheMode)
		go pipeLog(out)
		start := time.Now()
		err = cmd.Wait()
		if ctx.Err() != nil {
			s.setState(false, "")
			return
		}
		s.setState(false, fmt.Sprintf("rclone terminó: %v", err))
		logger.Log("[Descifrado] ⚠️ rclone terminó (%v); se reinicia en %s", err, backoff)
		if time.Since(start) > time.Minute {
			backoff = 2 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < time.Minute {
			backoff *= 2
		}
	}
}

func folderNames(fs []vault.Folder) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Name)
	}
	return out
}

func (s *Service) setState(running bool, errMsg string) {
	s.mu.Lock()
	s.running, s.lastErr = running, errMsg
	s.mu.Unlock()
}

func pipeLog(r io.Reader) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			logger.Log("[Descifrado] %s", line)
		}
	}
}

// mountArgs son los argumentos de «rclone mount».
func (s *Service) mountArgs(conf string) []string {
	a := []string{"mount", "raiz:", s.opts.MountPath, "--config", conf}
	if len(s.opts.MountArgs) > 0 {
		return append(a, s.opts.MountArgs...)
	}
	return append(a, DefaultMountArgs(s.opts.MountCache)...)
}

// superviseMount mantiene montado WEBDAV_MOUNT_PATH con el rclone interno.
func (s *Service) superviseMount(ctx context.Context, folders []vault.Folder) {
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		conf, err := s.writeConfig(folders, s.topDirs(ctx, folders))
		if err != nil {
			s.setMount(false, err.Error())
			return
		}
		_ = os.MkdirAll(s.opts.MountPath, 0o755)
		_ = os.MkdirAll(s.opts.MountCache, 0o700)
		// Un montaje anterior colgado (p. ej. tras un reinicio brusco) impide montar.
		_ = exec.Command("fusermount3", "-uz", s.opts.MountPath).Run()
		cmd := exec.CommandContext(ctx, s.opts.Binary, s.mountArgs(conf)...)
		cmd.Env = rcloneEnv(conf)
		out, _ := cmd.StdoutPipe()
		cmd.Stderr = cmd.Stdout
		if err := cmd.Start(); err != nil {
			s.setMount(false, "no se pudo arrancar rclone mount: "+err.Error())
			return
		}
		s.setMount(true, "")
		logger.Log("[Descifrado] 📂 Archivos descifrados montados en %s", s.opts.MountPath)
		go pipeLog(out)
		start := time.Now()
		err = cmd.Wait()
		_ = exec.Command("fusermount3", "-uz", s.opts.MountPath).Run()
		if ctx.Err() != nil {
			s.setMount(false, "")
			return
		}
		s.setMount(false, fmt.Sprintf("rclone mount terminó: %v", err))
		logger.Log("[Descifrado] ⚠️ rclone mount terminó (%v); se vuelve a montar en %s", err, backoff)
		if time.Since(start) > time.Minute {
			backoff = 5 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < time.Minute {
			backoff *= 2
		}
	}
}

func (s *Service) setMount(running bool, errMsg string) {
	s.mu.Lock()
	s.mountRunning, s.mountErr = running, errMsg
	s.mu.Unlock()
}

// CheckAuth comprueba las credenciales del WebDAV (las mismas del proxy).
func (s *Service) CheckAuth(user, pass string) bool {
	if s.opts.User == "" {
		return true
	}
	// En tiempo constante, para no dar pistas de la contraseña por lo que tarda.
	u := subtle.ConstantTimeCompare([]byte(user), []byte(s.opts.User))
	p := subtle.ConstantTimeCompare([]byte(pass), []byte(s.opts.Pass))
	return u&p == 1
}

// WaitReady espera (hasta 60 s) a que el WebDAV interno acepte conexiones.
func (s *Service) WaitReady(ctx context.Context) {
	addr := s.opts.Addr
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// rcloneEnv es el entorno del rclone interno: el del contenedor sin ninguna
// variable RCLONE_*. rclone toma RCLONE_<TIPO>_<OPCIÓN> como valor por defecto de
// esa opción (p. ej. RCLONE_CRYPT_PASSWORD2 pondría esa sal a toda carpeta cuya
// configuración no lleve password2), y el proxy usa variables con esos nombres.
func rcloneEnv(conf string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "RCLONE_") {
			env = append(env, kv)
		}
	}
	return append(env, "RCLONE_CONFIG="+conf)
}
