package webui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"movistar-webdav-proxy/internal/webdav"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Estado de la copia masiva a la nube que hace un script externo con rclone.
// El proxy no la controla: solo lee los ficheros que deja el script en
// COPY_STATUS_DIR (montado en solo lectura) y escribe órdenes en COPY_CONTROL_DIR:
//
//	plan.txt       bibliotecas en orden, una por línea
//	progress.log   "<fecha> START <biblioteca>" y "<fecha> END <biblioteca> rc=<código>"
//	<biblioteca>.log  log de rclone con --stats-one-line
//	STOP           motivo de una parada automática (si existe)
//	stats.json     volcado de "rclone rc core/stats" de la copia (archivos subiéndose)
//	mount-stats.json  ídem del rclone del montaje (archivos leyéndose)
//	control/bwlimit   límite de ancho de banda pedido desde el panel ("off", "20M"...)

type copyLibrary struct {
	Name   string `json:"name"`
	State  string `json:"state"` // pending | running | done | failed
	Start  string `json:"start,omitempty"`
	End    string `json:"end,omitempty"`
	Result string `json:"rc,omitempty"`
}

// copyTransfer es un archivo en curso según rclone (nombre legible, no cifrado).
type copyTransfer struct {
	Name       string  `json:"name"`
	Size       int64   `json:"size"`
	Bytes      int64   `json:"bytes"`
	Percentage int     `json:"percentage"`
	Speed      float64 `json:"speed"` // bytes/s
	ETA        *int64  `json:"eta"`   // segundos, null si se desconoce

	Idle        bool  `json:"idle,omitempty"`         // lectura en pausa (el reproductor tira de su búfer)
	IdleSeconds int64 `json:"idle_seconds,omitempty"` // desde la última ráfaga
}

type copyStatus struct {
	Enabled      bool           `json:"enabled"`
	Libraries    []copyLibrary  `json:"libraries"`
	Current      string         `json:"current,omitempty"`
	Transferred  string         `json:"transferred,omitempty"`
	Total        string         `json:"total,omitempty"`
	Percent      string         `json:"percent,omitempty"`
	Speed        string         `json:"speed,omitempty"`
	ETA          string         `json:"eta,omitempty"`
	StatsAt      string         `json:"stats_at,omitempty"`
	RecentErrors []string       `json:"recent_errors"`
	ErrorCount   int            `json:"error_count"`
	Stop         string         `json:"stop,omitempty"`
	BWLimit      string         `json:"bwlimit"`
	Uploading    []copyTransfer `json:"uploading"`
	Reading      []copyTransfer `json:"reading"`
	Queued       []copyTransfer `json:"queued"`       // subidas esperando hueco en el proxy
	UploadSpeed  float64        `json:"upload_speed"` // bytes/s según rclone en vivo
	ReadSpeed    float64        `json:"read_speed"`
	Folders      []folderStatus `json:"folders"`
}

// readTransfers lee un volcado de "rclone rc core/stats". Si es de hace más de
// staleAfter (rclone parado o el volcador caído), se ignora.
// rcloneTotals son los totales de la ejecución según "rclone rc core/stats".
type rcloneTotals struct {
	Bytes      int64
	TotalBytes int64
	Speed      float64
	ETA        *int64
}

func readTransfers(path string, staleAfter time.Duration) ([]copyTransfer, float64) {
	tr, tot := readRcloneStats(path, staleAfter)
	return tr, tot.Speed
}

func readRcloneStats(path string, staleAfter time.Duration) ([]copyTransfer, rcloneTotals) {
	var tot rcloneTotals
	fi, err := os.Stat(path)
	if err != nil || time.Since(fi.ModTime()) > staleAfter {
		return nil, tot
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, tot
	}
	var stats struct {
		Bytes        int64   `json:"bytes"`
		TotalBytes   int64   `json:"totalBytes"`
		ETA          *int64  `json:"eta"`
		Speed        float64 `json:"speed"`
		Transferring []struct {
			Name       string  `json:"name"`
			Size       int64   `json:"size"`
			Bytes      int64   `json:"bytes"`
			Percentage int     `json:"percentage"`
			Speed      float64 `json:"speed"`
			SpeedAvg   float64 `json:"speedAvg"`
			ETA        *int64  `json:"eta"`
		} `json:"transferring"`
	}
	if json.Unmarshal(b, &stats) != nil {
		return nil, tot
	}
	tot = rcloneTotals{Bytes: stats.Bytes, TotalBytes: stats.TotalBytes, Speed: stats.Speed, ETA: stats.ETA}
	out := make([]copyTransfer, 0, len(stats.Transferring))
	for _, t := range stats.Transferring {
		sp := t.SpeedAvg
		if sp == 0 {
			sp = t.Speed
		}
		out = append(out, copyTransfer{Name: t.Name, Size: t.Size, Bytes: t.Bytes, Percentage: t.Percentage, Speed: sp, ETA: t.ETA})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, tot
}

// humanBytes formatea en unidades binarias como rclone ("148.873 GiB").
func humanBytes(n float64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	i := 0
	for n >= 1024 && i < len(units)-1 {
		n /= 1024
		i++
	}
	return fmt.Sprintf("%.3f %s", n, units[i])
}

func humanDuration(sec int64) string {
	d := time.Duration(sec) * time.Second
	h := int64(d.Hours())
	m := int64(d.Minutes()) % 60
	ss := int64(d.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%dm%ds", h, m, ss)
	case m > 0:
		return fmt.Sprintf("%dm%ds", m, ss)
	}
	return fmt.Sprintf("%ds", ss)
}

var (
	// "2026-11-12T22:43:38+01:00 START Peliculas" o "2026-11-12 22:43:38 INICIO Peliculas";
	// fin: "... END|FIN Peliculas rc=0" (el rc puede ir entre paréntesis).
	progressLine = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}(?:[ T]\S+)?)\s+(START|END|INICIO|FIN)\s+(.+?)(?:\s+\(?rc=(-?\d+)\)?)?\s*$`)
	// Línea de --stats-one-line: "2026/11/12 22:41:00 INFO  :  1.2 GiB / 1.3 TiB, 0%, 25.1 MiB/s, ETA 15h53m"
	statsLine = regexp.MustCompile(`^(\S+ \S+) INFO\s+:\s+([\d.]+\s*\S*B) / ([\d.]+\s*\S*B), (\d+|-)%, ([\d.]+\s*\S*B/s), ETA (\S+)`)
	bwlimitRe = regexp.MustCompile(`^(off|\d+(\.\d+)?[KMG]?)$`)
)

func readLines(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// tailLines devuelve las líneas de los últimos max bytes del fichero.
func tailLines(path string, max int64) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	cut := false
	if st, err := f.Stat(); err == nil && st.Size() > max {
		_, _ = f.Seek(st.Size()-max, io.SeekStart)
		cut = true
	}
	b, _ := io.ReadAll(f)
	lines := strings.Split(string(b), "\n")
	if cut && len(lines) > 0 {
		lines = lines[1:] // la primera está cortada
	}
	return lines
}

func readCopyStatus(dir, controlDir string) copyStatus {
	return readCopyStatusWith(dir, controlDir, nil)
}

// readCopyStatusWith es readCopyStatus con el seguimiento de lecturas recientes.
func readCopyStatusWith(dir, controlDir string, reads *readTracker) copyStatus {
	st := copyStatus{RecentErrors: []string{}, BWLimit: "off", Uploading: []copyTransfer{}, Reading: []copyTransfer{}, Queued: []copyTransfer{}}
	// La actividad (subidas, lecturas y cola) sale del propio proxy y se muestra
	// siempre, haya o no copia masiva. Si hay registros de la copia (COPY_STATUS_DIR),
	// se añaden su progreso y los nombres reales que da rclone.
	st.Enabled = true
	if dir == "" {
		fillFromProxy(&st)
		applyClientCopies(&st)
		return st
	}
	if _, err := os.Stat(dir); err != nil {
		fillFromProxy(&st)
		applyClientCopies(&st)
		return st
	}

	var runStart time.Time // inicio de la ejecución en curso: lo anterior es de otra ejecución
	libs := map[string]*copyLibrary{}
	var order []string
	add := func(name string) *copyLibrary {
		if l, ok := libs[name]; ok {
			return l
		}
		libs[name] = &copyLibrary{Name: name, State: "pending"}
		order = append(order, name)
		return libs[name]
	}
	for _, name := range readLines(filepath.Join(dir, "plan.txt")) {
		add(name)
	}
	for _, line := range readLines(filepath.Join(dir, "progress.log")) {
		m := progressLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		l := add(m[3])
		if m[2] == "START" || m[2] == "INICIO" {
			l.State, l.Start, l.End, l.Result = "running", m[1], "", ""
			st.Current = l.Name
			runStart = parseLocalTime(m[1])
		} else {
			l.End, l.Result = m[1], m[4]
			if m[4] == "0" {
				l.State = "done"
			} else {
				l.State = "failed"
			}
			if st.Current == l.Name {
				st.Current = ""
			}
		}
	}
	for _, name := range order {
		st.Libraries = append(st.Libraries, *libs[name])
	}

	// Estadísticas de la biblioteca en curso (o de la última que corrió).
	logLib := st.Current
	if logLib == "" {
		for i := len(st.Libraries) - 1; i >= 0; i-- {
			if st.Libraries[i].State != "pending" {
				logLib = st.Libraries[i].Name
				break
			}
		}
	}
	if logLib != "" {
		for _, line := range tailLines(filepath.Join(dir, logLib+".log"), 256<<10) {
			// El log de cada biblioteca se reutiliza entre ejecuciones (tras un corte):
			// solo cuenta lo escrito desde el último inicio.
			if !runStart.IsZero() {
				if ts := parseLocalTime(firstTwoFields(line)); !ts.IsZero() && ts.Before(runStart) {
					continue
				}
			}
			if m := statsLine.FindStringSubmatch(line); m != nil {
				st.StatsAt, st.Transferred, st.Total, st.Percent, st.Speed, st.ETA = m[1], m[2], m[3], m[4], m[5], m[6]
			}
			if strings.Contains(line, " ERROR ") {
				st.ErrorCount++
				st.RecentErrors = append(st.RecentErrors, line)
			}
		}
		if n := len(st.RecentErrors); n > 5 {
			st.RecentErrors = st.RecentErrors[n-5:]
		}
	}

	if b, err := os.ReadFile(filepath.Join(dir, "STOP")); err == nil {
		st.Stop = strings.TrimSpace(string(b))
		if st.Stop == "" {
			st.Stop = "parada automática (sin motivo registrado)"
		}
	}
	statsPath := filepath.Join(dir, "stats.json")
	if fi, err := os.Stat(statsPath); err == nil && !runStart.IsZero() && fi.ModTime().Before(runStart) {
		statsPath = "" // volcado de la ejecución anterior
	}
	if up, tot := readRcloneStats(statsPath, 90*time.Second); up != nil {
		st.Uploading, st.UploadSpeed = up, tot.Speed
		// Los totales salen del volcado en vivo (cada pocos segundos) en vez de la
		// línea de estadísticas del log, que rclone solo escribe una vez por minuto;
		// el log queda de respaldo si no hay volcado.
		if st.Current != "" && tot.TotalBytes > 0 {
			st.Transferred = humanBytes(float64(tot.Bytes))
			st.Total = humanBytes(float64(tot.TotalBytes))
			st.Percent = strconv.Itoa(int(tot.Bytes * 100 / tot.TotalBytes))
			st.Speed = humanBytes(tot.Speed) + "/s"
			if tot.ETA != nil {
				st.ETA = humanDuration(*tot.ETA)
			}
			if fi, err := os.Stat(statsPath); err == nil {
				st.StatsAt = fi.ModTime().Format("2006/01/02 15:04:05")
			}
		}
	}
	if rd, sp := readTransfers(filepath.Join(dir, "mount-stats.json"), 90*time.Second); rd != nil {
		st.Reading, st.ReadSpeed = rd, sp
	}

	// Las subidas y su velocidad salen siempre del propio proxy: ve todas las vías
	// (copia, montaje, SMB...) desde que llegan, con su nombre en claro y la carpeta
	// como primer componente. Del registro de la copia solo quedan el total pendiente,
	// el porcentaje y el tiempo restante.
	st.Uploading, st.UploadSpeed = proxyUploads()
	if st.UploadSpeed > 0 || st.Current != "" {
		st.Speed = humanBytes(st.UploadSpeed) + "/s"
	}
	copyUploads := 0
	// Las subidas del montaje (cola de su caché) ya las cuenta el proxy: fuera de las lecturas.
	separateMountUploads(dir, &st)
	// Solo lo que se está leyendo: las lecturas paradas (el reproductor tira de su
	// búfer) no se muestran.
	st.Folders = buildFolders(dir, st, copyUploads)
	fillFromProxy(&st)
	applyClientCopies(&st)
	return st
}

// applyClientCopies pone en la cabecera el total de las copias que mandan los
// clientes (p. ej. un script de copia con rclone), si no hay copia del script.
func applyClientCopies(st *copyStatus) {
	copies := webdav.ClientCopies(30 * time.Second)
	if st.Current != "" || len(copies) == 0 {
		return
	}
	var bytes, total int64
	var speed float64
	var eta int64
	var dests []string
	newest := copies[0].Updated
	for _, c := range copies {
		bytes += c.Bytes
		total += c.TotalBytes
		speed += c.Speed
		if c.ETA != nil && *c.ETA > eta {
			eta = *c.ETA
		}
		st.ErrorCount += c.Errors
		if c.Dest != "" {
			dests = append(dests, c.Dest)
		}
		if c.Updated.After(newest) {
			newest = c.Updated
		}
	}
	st.Current = strings.Join(dests, ", ")
	if st.Current == "" {
		st.Current = "copia desde un cliente"
	}
	if len(copies) > 1 {
		st.Current += fmt.Sprintf(" (%d envíos)", len(copies))
	}
	st.Transferred = humanBytes(float64(bytes))
	st.Total = humanBytes(float64(total))
	if total > 0 {
		st.Percent = strconv.Itoa(int(bytes * 100 / total))
	}
	if speed > 0 {
		st.Speed = humanBytes(speed) + "/s"
	}
	if eta > 0 {
		st.ETA = humanDuration(eta)
	} else if speed > 0 && total > bytes {
		st.ETA = humanDuration(int64(float64(total-bytes) / speed))
	}
	st.StatsAt = newest.Format("2006/01/02 15:04:05")
}

func (s *Server) handleCopyStatus(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, http.StatusOK, readCopyStatusWith(s.copyDir, s.copyControlDir, s.reads))
}

// handleCopyBWLimit guarda el límite pedido; el script de copia lo aplica en caliente.
func (s *Server) handleCopyBWLimit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Rate string `json:"rate"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	rate := strings.TrimSpace(req.Rate)
	bps, err := webdav.ParseBandwidth(rate)
	if err != nil || !bwlimitRe.MatchString(rate) {
		jsonResponse(w, http.StatusBadRequest, map[string]interface{}{"status": "error", "message": "Límite no válido (usa off, 20M, 500K...)"})
		return
	}
	// El límite se aplica en el proxy a TODAS las subidas, entren por donde entren.
	webdav.SetUploadBandwidth(bps)
	saveUploadBandwidth(rate)
	// Compatibilidad: si hay script de copia, también se le pasa.
	if s.copyControlDir != "" {
		_ = os.WriteFile(filepath.Join(s.copyControlDir, "bwlimit"), []byte(rate+"\n"), 0o644)
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{"status": "ok", "bwlimit": rate})
}

// parseLocalTime interpreta las fechas del script ("2026-11-12 22:43:38" o RFC 3339)
// y de rclone ("2026/11/12 22:43:38") en hora local. Devuelve cero si no encaja.
func parseLocalTime(v string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006/01/02 15:04:05"} {
		if t, err := time.ParseInLocation(layout, strings.TrimSpace(v), time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
}

func firstTwoFields(line string) string {
	f := strings.Fields(line)
	if len(f) < 2 {
		return ""
	}
	return f[0] + " " + f[1]
}

// fillFromProxy completa la actividad con lo que ve el propio proxy: la cola
// siempre y, si rclone no ha dado datos (sin copia masiva o sin sus estadísticas),
// las subidas y lecturas en curso.
func fillFromProxy(st *copyStatus) {
	st.BWLimit = currentUploadBandwidth()
	var up, rd, q []copyTransfer
	var upSpeed, rdSpeed float64
	for _, t := range webdav.Transfers.Snapshot() {
		name := panelName(t)
		ct := copyTransfer{Name: name, Size: t.Size, Bytes: t.Bytes, Percentage: t.Percentage,
			Speed: t.Speed, ETA: t.ETA, Idle: t.Idle, IdleSeconds: t.IdleSeconds}
		switch t.Kind {
		case "upload":
			up = append(up, ct)
			upSpeed += t.Speed
		case "download":
			rd = append(rd, ct)
			if !t.Idle {
				rdSpeed += t.Speed
			}
		case "queued":
			q = append(q, ct)
		}
	}
	if q != nil {
		st.Queued = q
	}
	if len(st.Uploading) == 0 && len(up) > 0 {
		st.Uploading, st.UploadSpeed = up, upSpeed
	}
	if len(st.Reading) == 0 && len(rd) > 0 {
		st.Reading, st.ReadSpeed = rd, rdSpeed
	}
}

// copyCloudFolder es la carpeta de la nube donde la copia deja las bibliotecas
// (COPY_CLOUD_FOLDER, por defecto «datos»): en el panel las rutas empiezan por la
// biblioteca.
func copyCloudFolder() string {
	if v := strings.Trim(strings.TrimSpace(os.Getenv("COPY_CLOUD_FOLDER")), "/"); v != "" {
		return v
	}
	return "datos"
}

// panelName es el nombre legible de una transferencia, relativo a la carpeta de la copia.
func panelName(t webdav.TransferInfo) string {
	name := t.Display
	if name == "" {
		name = t.Path
	}
	name = strings.TrimPrefix(name, "/")
	return strings.TrimPrefix(name, copyCloudFolder()+"/")
}

// proxyUploads son las subidas en curso que ve el proxy y su velocidad total.
func proxyUploads() ([]copyTransfer, float64) {
	up := []copyTransfer{}
	var speed float64
	for _, t := range webdav.Transfers.Snapshot() {
		if t.Kind != "upload" {
			continue
		}
		up = append(up, copyTransfer{Name: panelName(t), Size: t.Size, Bytes: t.Bytes, Percentage: t.Percentage, Speed: t.Speed, ETA: t.ETA})
		speed += t.Speed
	}
	return up, speed
}
