package main

import (
	"fmt"
	"html"
	"os"
	"sort"
	"strings"
	"time"

	"movistar-webdav-proxy/internal/cache"
	"movistar-webdav-proxy/internal/logger"
	"movistar-webdav-proxy/internal/notifier"
	"movistar-webdav-proxy/internal/rclonesvc"
	"movistar-webdav-proxy/internal/sapi"
	"movistar-webdav-proxy/internal/webclient"
	"movistar-webdav-proxy/internal/webdav"
)

// folderLimitApprox es el tope de carpetas de la cuenta que vimos en la práctica
// (FOL-1035 con ~9.984); Movistar no lo publica.
const folderLimitApprox = 10000

var botCommands = []notifier.Command{
	{Name: "estado", Description: "Estado general: sesión, descifrado, transferencias y el equipo"},
	{Name: "progreso", Description: "Progreso de las copias que informan los clientes"},
	{Name: "subidas", Description: "Subidas en curso, una a una"},
	{Name: "errores", Description: "Últimos errores y avisos"},
	{Name: "carpetas", Description: "Carpetas usadas frente al límite de la cuenta"},
	{Name: "ayuda", Description: "Lista de comandos"},
}

// botKeyboard son los botones bajo cada respuesta del bot.
var botKeyboard = [][]notifier.Button{
	{{Text: "📊 Estado", Data: "estado"}, {Text: "📦 Progreso", Data: "progreso"}},
	{{Text: "⬆️ Subidas", Data: "subidas"}, {Text: "🧾 Errores", Data: "errores"}},
	{{Text: "📁 Carpetas", Data: "carpetas"}, {Text: "❓ Ayuda", Data: "ayuda"}},
}

// botAliases: nombres en inglés y los de Telegram por defecto.
var botAliases = map[string]string{"start": "ayuda", "help": "ayuda", "status": "estado",
	"progress": "progreso", "uploads": "subidas", "errors": "errores", "folders": "carpetas"}

type botDeps struct {
	started time.Time
	sess    *webclient.Session
	svc     *rclonesvc.Service
	meta    *cache.MetadataStore
	client  *sapi.Client
}

func (d *botDeps) handle(cmd, args string) string {
	if out := d.reply(cmd, args); out != "" {
		return out + "\n<i>Actualizado " + time.Now().Format("15:04:05") + "</i>"
	}
	return ""
}

func (d *botDeps) reply(cmd, _ string) string {
	if a, ok := botAliases[cmd]; ok {
		cmd = a
	}
	switch cmd {
	case "estado":
		return d.estado()
	case "progreso":
		return d.progreso()
	case "subidas":
		return d.subidas()
	case "errores":
		return d.errores()
	case "carpetas":
		return d.carpetas()
	case "ayuda":
		var b strings.Builder
		b.WriteString("🤖 <b>Movistar Nube WebDAV</b>\n\nPulsa un botón o escribe un comando:\n\n")
		for _, c := range botCommands {
			fmt.Fprintf(&b, "/%s · %s\n", c.Name, html.EscapeString(c.Description))
		}
		return b.String()
	}
	return "No conozco ese comando. Escribe /ayuda para ver la lista."
}

func (d *botDeps) estado() string {
	var b strings.Builder
	fmt.Fprintf(&b, "📊 <b>Estado del proxy</b>\n\nEn marcha desde hace %s\n", humanAgo(time.Since(d.started)))
	if d.sess != nil {
		st := d.sess.Status()
		if on, _ := st["logged_in"].(bool); on {
			rem := "no"
			if r, _ := st["remember"].(bool); r {
				rem = "sí"
			}
			exp := ""
			if e, ok := st["expires"].(string); ok {
				if t, err := time.Parse(time.RFC3339, e); err == nil {
					exp = ", caduca " + t.Local().Format("02/01 15:04")
				}
			}
			fmt.Fprintf(&b, "🟢 Sesión de Movistar activa (No cerrar sesión: %s%s)\n", rem, exp)
		} else {
			b.WriteString("🔴 Sesión de Movistar <b>perdida</b>: entra desde el panel en /web-relogin/\n")
		}
	}
	if d.svc != nil {
		if running, _ := d.svc.Status()["running"].(bool); running {
			b.WriteString("🟢 Descifrado funcionando\n")
		} else {
			b.WriteString("🔴 Descifrado <b>parado</b>\n")
		}
	}
	var nUp, nRd int
	var spUp, spRd float64
	for _, t := range webdav.Transfers.Snapshot() {
		if t.Idle {
			continue
		}
		if t.Kind == "upload" {
			nUp++
			spUp += t.Speed
		} else {
			nRd++
			spRd += t.Speed
		}
	}
	fmt.Fprintf(&b, "⬆️ %d subidas · %s/s   ⬇️ %d lecturas · %s/s\n", nUp, humanSize(spUp), nRd, humanSize(spRd))
	if pi := piHealth(); pi != "" {
		fmt.Fprintf(&b, "🍓 %s\n", pi)
	}
	if d.client != nil {
		if sp, err := d.client.GetStorageSpace(); err == nil && sp != nil {
			fmt.Fprintf(&b, "☁️ %s usados en la nube\n", humanSize(float64(sp.UsedBytes)))
		}
	}
	return b.String()
}

func (d *botDeps) progreso() string {
	copies := webdav.ClientCopies(30 * time.Second)
	if len(copies) == 0 {
		return "📦 No hay ninguna copia de cliente en curso.\n\nPara ver lo que se está subiendo ahora: /subidas"
	}
	var b strings.Builder
	b.WriteString("📦 <b>Copias en curso</b>\n")
	for _, c := range copies {
		pct := 0
		if c.TotalBytes > 0 {
			pct = int(c.Bytes * 100 / c.TotalBytes)
		}
		dest := c.Dest
		if dest == "" {
			dest = "(sin nombre)"
		}
		fmt.Fprintf(&b, "\n<b>%s</b>\n%s %d%%\n%s de %s · %s/s", html.EscapeString(dest), bar(pct), pct,
			humanSize(float64(c.Bytes)), humanSize(float64(c.TotalBytes)), humanSize(c.Speed))
		if c.ETA != nil && *c.ETA > 0 {
			fmt.Fprintf(&b, " · quedan %s", humanAgo(time.Duration(*c.ETA)*time.Second))
		}
		if c.Errors > 0 {
			fmt.Fprintf(&b, "\n⚠️ %d errores", c.Errors)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (d *botDeps) subidas() string {
	var ups []webdav.TransferInfo
	for _, t := range webdav.Transfers.Snapshot() {
		if t.Kind == "upload" && !t.Idle {
			ups = append(ups, t)
		}
	}
	if len(ups) == 0 {
		return "⬆️ No hay subidas en curso."
	}
	sort.Slice(ups, func(i, j int) bool { return ups[i].Percentage > ups[j].Percentage })
	var b strings.Builder
	fmt.Fprintf(&b, "⬆️ <b>%d subidas en curso</b>\n", len(ups))
	for i, t := range ups {
		if i == 10 {
			fmt.Fprintf(&b, "\n… y %d más", len(ups)-10)
			break
		}
		name := t.Display
		if name == "" {
			name = t.Path
		}
		fmt.Fprintf(&b, "\n%s\n%s %d%% · %s/s\n", html.EscapeString(shortName(name)), bar(t.Percentage), t.Percentage, humanSize(t.Speed))
	}
	return b.String()
}

func (d *botDeps) errores() string {
	var lines []string
	for _, l := range logger.GetLogs() {
		if strings.Contains(l, "❌") || (strings.Contains(l, "⚠️") && !strings.Contains(l, "Nombres duplicados")) {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return "✅ Sin errores ni avisos recientes."
	}
	if len(lines) > 10 {
		lines = lines[len(lines)-10:]
	}
	var b strings.Builder
	b.WriteString("🧾 <b>Últimos errores y avisos</b>\n")
	for _, l := range lines {
		if len(l) > 220 {
			l = l[:220] + "…"
		}
		fmt.Fprintf(&b, "\n%s\n", html.EscapeString(l))
	}
	return b.String()
}

func (d *botDeps) carpetas() string {
	if d.meta == nil {
		return "No disponible."
	}
	n, err := d.meta.CountFolders()
	if err != nil {
		return "No se pudo contar: " + html.EscapeString(err.Error())
	}
	pct := n * 100 / folderLimitApprox
	icon := "🟢"
	if pct >= 80 {
		icon = "🔴"
	} else if pct >= 60 {
		icon = "🟠"
	}
	return fmt.Sprintf("%s <b>%d carpetas</b> de ~%d (%d%%)\n%s\n\nEl límite no lo publica Movistar: es el que saltó en la práctica (FOL-1035).",
		icon, n, folderLimitApprox, pct, bar(pct))
}

// piHealth: carga, temperatura y memoria libre de la máquina (Linux).
func piHealth() string {
	var parts []string
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(b)); len(f) >= 3 {
			parts = append(parts, "carga "+strings.Join(f[:3], " "))
		}
	}
	if b, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp"); err == nil {
		var milli int
		if _, err := fmt.Sscan(strings.TrimSpace(string(b)), &milli); err == nil && milli > 0 {
			parts = append(parts, fmt.Sprintf("%.0f °C", float64(milli)/1000))
		}
	}
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			var kb int64
			if _, err := fmt.Sscanf(l, "MemAvailable: %d kB", &kb); err == nil {
				parts = append(parts, "RAM libre "+humanSize(float64(kb*1024)))
				break
			}
		}
	}
	return strings.Join(parts, " · ")
}

func bar(pct int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	full := pct / 10
	return strings.Repeat("▓", full) + strings.Repeat("░", 10-full)
}

func humanSize(n float64) string {
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	i := 0
	for n >= 1024 && i < len(units)-1 {
		n /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f %s", n, units[i])
	}
	return fmt.Sprintf("%.1f %s", n, units[i])
}

func humanAgo(d time.Duration) string {
	d = d.Round(time.Minute)
	if d < time.Minute {
		return "menos de un minuto"
	}
	days := int(d.Hours()) / 24
	h := int(d.Hours()) % 24
	m := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%d d %d h", days, h)
	case h > 0:
		return fmt.Sprintf("%d h %d min", h, m)
	}
	return fmt.Sprintf("%d min", m)
}

// shortName deja las dos últimas partes de la ruta (serie/temporada o carpeta/archivo).
func shortName(p string) string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return strings.Join(parts, "/")
}
