package main

import (
	"context"
	"fmt"
	"time"

	"movistar-webdav-proxy/internal/logger"
	"movistar-webdav-proxy/internal/notifier"
	"movistar-webdav-proxy/internal/rclonesvc"
	"movistar-webdav-proxy/internal/sapi"
	"movistar-webdav-proxy/internal/vault"
	"movistar-webdav-proxy/internal/webclient"
	"movistar-webdav-proxy/internal/webdav"
)

// healthAlertAfter: un problema tiene que durar esto antes de avisar por Telegram;
// los cortes de unos segundos (la sesión se recupera sola) no avisan.
const healthAlertAfter = 2 * time.Minute

type healthCheck struct {
	key, title, desc, okTitle, okDesc string
	ok                                func() bool
	badSince                          time.Time
	alerted                           bool
}

// step comprueba una vez: avisa si lleva mal más de healthAlertAfter y, si ya avisó,
// avisa también cuando se recupera.
func (c *healthCheck) step(now time.Time) {
	if c.ok() {
		if c.alerted {
			_ = notifier.Alert("", "✅", c.okTitle, c.okDesc, "")
		}
		c.badSince, c.alerted = time.Time{}, false
		return
	}
	if c.badSince.IsZero() {
		c.badSince = now
	}
	if !c.alerted && now.Sub(c.badSince) >= healthAlertAfter {
		c.alerted = true
		_ = notifier.Alert(c.key, "⚠️", c.title, c.desc, "")
	}
}

// watchHealth vigila la sesión web y el descifrado (rclone interno) cada 30 s.
func watchHealth(ctx context.Context, sess *webclient.Session, svc *rclonesvc.Service) {
	var checks []*healthCheck
	if sess != nil {
		checks = append(checks, &healthCheck{
			key: "web_session_lost", title: "Sesión de Movistar Cloud perdida",
			desc:    "El proxy no puede leer ni subir a la nube. Si no se recupera sola en unos minutos, vuelve a entrar desde el panel en /web-relogin/ (no cierra la sesión actual).",
			okTitle: "Sesión de Movistar Cloud recuperada", okDesc: "El proxy vuelve a leer y subir con normalidad.",
			ok: sess.LoggedIn,
		})
	}
	if svc != nil {
		checks = append(checks, &healthCheck{
			key: "decrypt_down", title: "Descifrado parado",
			desc:    "El rclone interno que descifra las carpetas no está funcionando: las lecturas y subidas de las carpetas cifradas fallan.",
			okTitle: "Descifrado funcionando de nuevo", okDesc: "El rclone interno vuelve a servir las carpetas cifradas.",
			ok: func() bool { running, _ := svc.Status()["running"].(bool); return running || len(vault.Folders()) == 0 },
		})
	}
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			for _, c := range checks {
				c.step(now)
			}
			webdav.SweepClientCopies(now)
		}
	}
}

// onNetworkChange: el equipo ha cambiado de conexión (via es la etiqueta que manda el
// aviso, p. ej. "wifi"). Se cortan las conexiones con Movistar (las subidas en curso
// fallan y el cliente las reintenta enteras), se relanza el navegador de la sesión web
// y se avisa por Telegram.
func onNetworkChange(via string, sess *webclient.Session) {
	notifier.ResetConnections()
	n := sapi.ResetConnections(via)
	desc := "La conexión del equipo ha cambiado: las peticiones con Movistar se han cortado y se repiten por la red nueva."
	if via != "" {
		desc = fmt.Sprintf("La conexión del equipo ha cambiado (ahora: %s): las peticiones con Movistar se han cortado y se repiten por la red nueva.", via)
	}
	// El aviso no espera al relanzamiento del navegador (tarda de 10 a 20 s).
	go notifier.Alert("", "📶", "Cambio de red", desc, fmt.Sprintf("%d peticiones cortadas", n))
	if sess != nil {
		if err := sess.ResetNetwork(); err != nil {
			logger.Log("[WebSession] ❌ No se pudo relanzar el navegador tras el cambio de red: %v", err)
		}
	}
}
