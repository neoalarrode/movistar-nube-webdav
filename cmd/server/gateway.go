package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"time"

	"movistar-webdav-proxy/internal/cryptname"
	"movistar-webdav-proxy/internal/logger"
	"movistar-webdav-proxy/internal/rclonesvc"
	"movistar-webdav-proxy/internal/sapi"
	"movistar-webdav-proxy/internal/vault"
	"movistar-webdav-proxy/internal/webdav"
)

// decryptedGateway es el WebDAV público. Sin carpetas configuradas lo atiende el
// WebDAV en bruto; con alguna, reenvía todo al rclone interno (cada carpeta según
// su configuración y el resto tal cual) salvo crear una carpeta en la raíz, que
// rclone no puede hacer con «combine»: esa la crea el proxy en la nube y recarga
// rclone para que aparezca.
type decryptedGateway struct {
	upstream *httputil.ReverseProxy
	raw      *webdav.Server
	svc      *rclonesvc.Service
	// onNetChange se llama cuando el equipo avisa de un cambio de red por
	// networkChangePath; via es la etiqueta de la conexión nueva (p. ej. «wifi»).
	onNetChange func(via string)
}

// networkChangePath recibe el aviso de cambio de red de un script del equipo (p. ej.
// un hook de networkd-dispatcher): dentro de Docker el proxy no ve las tarjetas.
const networkChangePath = "/.red-cambio"

func newDecryptedGateway(rcloneAddr string, raw *webdav.Server, svc *rclonesvc.Service) *decryptedGateway {
	target, _ := url.Parse("http://" + rcloneAddr)
	rp := httputil.NewSingleHostReverseProxy(target)
	rp.FlushInterval = -1 // descargas y subidas en flujo, sin acumular
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if r.Context().Err() != nil {
			// El cliente se fue (p. ej. un reproductor cancela una lectura al saltar): no es un fallo de rclone.
			return
		}
		logger.Log("[Descifrado] ⚠️ %s %s: rclone interno no responde: %v", r.Method, r.URL.Path, err)
		w.Header().Set("Retry-After", "5")
		http.Error(w, "Servicio descifrado no disponible, reintenta", http.StatusServiceUnavailable)
	}
	return &decryptedGateway{upstream: rp, raw: raw, svc: svc}
}

// clientCopyPath recibe el estado de las copias que hacen los clientes con rclone
// (p. ej. un script de copia con rclone) para que el panel enseñe el total pendiente.
const clientCopyPath = "/.nube-estado"

func (g *decryptedGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == clientCopyPath {
		g.clientCopy(w, r)
		return
	}
	if r.URL.Path == networkChangePath {
		g.networkChange(w, r)
		return
	}
	if len(vault.Folders()) == 0 {
		g.raw.ServeHTTP(w, r)
		return
	}
	// Las credenciales se comprueban antes de nada: lo de abajo anota en el WebDAV en
	// bruto tamaños y fechas esperados por ruta, y una petición sin credenciales no
	// debe poder tocar los de una subida legítima en curso.
	if user, pass, ok := r.BasicAuth(); !ok || !g.svc.CheckAuth(user, pass) {
		w.Header().Set("WWW-Authenticate", `Basic realm="WebDAV"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method == "MKCOL" {
		clean := strings.Trim(path.Clean("/"+r.URL.Path), "/")
		if clean != "" && !strings.Contains(clean, "/") {
			g.mkdirRoot(w, r, clean)
			return
		}
	}
	if r.Method == "PROPFIND" {
		// rclone no sabe la cuota de la cuenta y anuncia 1 PiB libre sin lo usado.
		rw, flush := g.raw.WithAccountQuota(w)
		g.upstream.ServeHTTP(rw, r)
		flush()
		return
	}
	if r.Method == http.MethodGet {
		// El panel muestra la lectura como un solo archivo en claro, no trozo a trozo.
		if raw, ok := rawPathFor(r.URL.Path); ok {
			defer webdav.CoverUploads(raw)()
		}
		tw, done := webdav.TrackPublicDownload(w, path.Clean("/"+r.URL.Path))
		defer done()
		g.upstream.ServeHTTP(tw, r)
		return
	}
	if r.Method == http.MethodPut {
		// El panel muestra la subida desde que llega, con su nombre en claro y su
		// tamaño, en vez de esperar a que el rclone interno empiece a mandar el trozo
		// cifrado (que se oculta para no contarla dos veces).
		body, done := webdav.TrackPublicUpload(path.Clean("/"+r.URL.Path), r.ContentLength, r.Body)
		r.Body = body
		defer done()
		if raw, ok := rawPathFor(r.URL.Path); ok {
			defer webdav.CoverUploads(raw)()
			// rclone no reenvía la cabecera: se marca la ruta en bruto que va a escribir.
			if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Upload-Priority")), "bulk") {
				defer webdav.MarkBulkPath(raw)()
			}
			// rclone sube con la hora de subida: se conserva la fecha original del cliente.
			if mt := strings.TrimSpace(r.Header.Get("X-OC-Mtime")); mt != "" {
				defer webdav.ExpectUploadMtime(raw, mt)()
			}
			// rclone sube en flujo sin Content-Length: se le dice al WebDAV en bruto el
			// tamaño de cada objeto para que suba directo a la nube.
			if size := r.ContentLength; size >= 0 {
				if sz := sizerFor(r.URL.Path); sz != nil {
					dir, base := path.Split("/" + raw)
					defer webdav.ExpectUploadSize(raw, func(p string) int64 {
						pd, name := path.Split(p)
						if pd != dir {
							return -1
						}
						return sz.sizeOf(size, base, name)
					})()
				}
			}
		}
	}
	// Tras una subida no se pide al rclone interno que olvide el archivo: rclone 1.76
	// ya guarda la fecha de X-OC-Mtime, y olvidar un archivo le hace releer la carpeta
	// entera (y el chunker, los metadatos de cada archivo) en cada subida.
	if r.Method == http.MethodPut {
		// Una subida fallida se apunta en la copia del cliente a la que pertenece. Si
		// falla tras un cambio de red (se cortaron las conexiones con Movistar), se
		// responde 503 con Retry-After: rclone lo trata como temporal y vuelve a mandar
		// el archivo entero en vez de darlo por fallido.
		sw := &statusWriter{ResponseWriter: w, retryAfterNetChange: true}
		g.upstream.ServeHTTP(sw, r)
		if sw.status >= 400 {
			webdav.RecordUploadFailure(r.URL.Path, fmt.Sprintf("%d %s", sw.status, http.StatusText(sw.status)))
		}
		return
	}
	g.upstream.ServeHTTP(w, r)
}

// netChangeRetryWindow: tras un cambio de red, los errores de subida se devuelven como
// 503 (reintentable) durante este tiempo.
const netChangeRetryWindow = 3 * time.Minute

// statusWriter guarda el código de respuesta. Con retryAfterNetChange, un error justo
// después de un cambio de red se sustituye por 503 + Retry-After (y se descarta el
// cuerpo del error original).
type statusWriter struct {
	http.ResponseWriter
	status              int
	retryAfterNetChange bool
	swallow             bool
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status != 0 {
		return
	}
	s.status = code
	if s.retryAfterNetChange && code >= 400 && code != http.StatusUnauthorized && sapi.NetworkChangedWithin(netChangeRetryWindow) {
		s.swallow = true
		s.ResponseWriter.Header().Set("Retry-After", "5")
		http.Error(s.ResponseWriter, "Cambio de red: reintenta la subida", http.StatusServiceUnavailable)
		return
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.WriteHeader(http.StatusOK)
	}
	if s.swallow {
		return len(b), nil
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// networkChange atiende el aviso de cambio de red (POST con {"via": "<conexión>"},
// p. ej. "cable", "wifi" o "lte"; solo es una etiqueta para el aviso).
func (g *decryptedGateway) networkChange(w http.ResponseWriter, r *http.Request) {
	if user, pass, ok := r.BasicAuth(); !ok || !g.svc.CheckAuth(user, pass) {
		w.Header().Set("WWW-Authenticate", `Basic realm="WebDAV"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Via string `json:"via"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body)
	body.Via = strings.TrimSpace(body.Via)
	if len(body.Via) > 32 {
		body.Via = body.Via[:32]
	}
	if g.onNetChange != nil {
		go g.onNetChange(body.Via)
	}
	w.WriteHeader(http.StatusNoContent)
}

// clientCopy guarda el estado de una copia de un cliente (POST con JSON, con las
// credenciales del WebDAV).
func (g *decryptedGateway) clientCopy(w http.ResponseWriter, r *http.Request) {
	if user, pass, ok := r.BasicAuth(); !ok || !g.svc.CheckAuth(user, pass) {
		w.Header().Set("WWW-Authenticate", `Basic realm="WebDAV"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	var c webdav.ClientCopy
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&c); err != nil || c.ID == "" || len(c.ID) > 100 || len(c.Dest) > 1000 {
		http.Error(w, "estado no válido", http.StatusBadRequest)
		return
	}
	webdav.ReportClientCopy(c)
	w.WriteHeader(http.StatusNoContent)
}

// rawPathFor traduce una ruta del WebDAV público a la ruta en bruto en la nube:
// igual en las carpetas sin configuración y con los nombres cifrados en las que
// cifran nombres con el formato estándar de rclone crypt.
func rawPathFor(p string) (string, bool) {
	clean := strings.Trim(path.Clean("/"+p), "/")
	folder, rest, _ := strings.Cut(clean, "/")
	configured := false
	for _, f := range vault.Folders() {
		if f.Name == folder {
			configured = true
			break
		}
	}
	if !configured {
		return clean, true
	}
	v, ok := folderNames.Load(folder)
	if !ok {
		return "", false
	}
	c := v.(*cryptname.Cipher)
	segs := []string{folder}
	for _, s := range strings.Split(rest, "/") {
		if s != "" {
			segs = append(segs, c.EncryptSegment(s))
		}
	}
	return strings.Join(segs, "/"), true
}

func (g *decryptedGateway) mkdirRoot(w http.ResponseWriter, r *http.Request, name string) {
	fs := g.raw.FS()
	if fs == nil {
		http.Error(w, "no disponible", http.StatusServiceUnavailable)
		return
	}
	if err := fs.Mkdir(context.Background(), "/"+name, 0o755); err != nil {
		logger.Log("[Descifrado] ❌ No se pudo crear la carpeta raíz '%s': %v", name, err)
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	logger.Log("[Descifrado] 📁 Nueva carpeta en la raíz: '%s'", name)
	g.svc.Restart()
	g.svc.WaitReady(context.Background())
	w.WriteHeader(http.StatusCreated)
}

// sizerFor devuelve cómo calcular los tamaños en bruto en la carpeta de p (sin
// configuración: tal cual).
func sizerFor(p string) *uploadSizer {
	folder, _, _ := strings.Cut(strings.Trim(path.Clean("/"+p), "/"), "/")
	for _, f := range vault.Folders() {
		if f.Name == folder {
			if v, ok := folderSizers.Load(folder); ok {
				return v.(*uploadSizer)
			}
			return nil
		}
	}
	return &uploadSizer{}
}
