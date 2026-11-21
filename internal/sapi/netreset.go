package sapi

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"

	"movistar-webdav-proxy/internal/logger"
)

// Corte de las conexiones con Movistar al cambiar la red del equipo (p. ej.
// cable ↔ Wi-Fi). Las conexiones abiertas por la red que se ha ido se quedan muertas sin
// cerrarse y las peticiones esperan minutos; se cancelan todas para que se abran
// nuevas por la red buena. Una subida cancelada devuelve error y el cliente (rclone)
// vuelve a mandar el archivo entero.

var inflight struct {
	sync.Mutex
	cancels    map[*int]context.CancelFunc
	transports []interface{ CloseIdleConnections() }
	changedAt  time.Time
}

// ResetConnections cancela las peticiones en curso con Movistar y cierra las
// conexiones abiertas. Devuelve cuántas peticiones se cancelaron.
func ResetConnections(reason string) int {
	inflight.Lock()
	n := len(inflight.cancels)
	for k, cancel := range inflight.cancels {
		cancel()
		delete(inflight.cancels, k)
	}
	inflight.changedAt = time.Now()
	ts := append([]interface{ CloseIdleConnections() }(nil), inflight.transports...)
	inflight.Unlock()
	for _, t := range ts {
		t.CloseIdleConnections()
	}
	logger.Log("[SAPI] 🔌 Cambio de red (%s): %d peticiones con Movistar cortadas y conexiones cerradas; se reintentan por la red nueva.", reason, n)
	return n
}

// NetworkChangedWithin dice si hubo un cambio de red hace menos de d.
func NetworkChangedWithin(d time.Duration) bool {
	inflight.Lock()
	defer inflight.Unlock()
	return !inflight.changedAt.IsZero() && time.Since(inflight.changedAt) < d
}

// resettableTransport apunta cada petición en curso para poder cancelarla con
// ResetConnections (hasta que se cierra el cuerpo de su respuesta).
type resettableTransport struct {
	base http.RoundTripper
}

func newResettableTransport(base http.RoundTripper) *resettableTransport {
	t := &resettableTransport{base: base}
	if c, ok := base.(interface{ CloseIdleConnections() }); ok {
		inflight.Lock()
		inflight.transports = append(inflight.transports, c)
		inflight.Unlock()
	}
	return t
}

func (t *resettableTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(req.Context())
	key := new(int)
	inflight.Lock()
	if inflight.cancels == nil {
		inflight.cancels = map[*int]context.CancelFunc{}
	}
	inflight.cancels[key] = cancel
	inflight.Unlock()
	done := func() {
		inflight.Lock()
		delete(inflight.cancels, key)
		inflight.Unlock()
		cancel()
	}
	resp, err := t.base.RoundTrip(req.WithContext(ctx))
	if err != nil {
		done()
		return resp, err
	}
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, done: done}
	return resp, nil
}

// HTTP1Transport expone el transporte HTTP/1.1 de debajo (lo usan los tests).
func (t *resettableTransport) HTTP1Transport() *http.Transport {
	switch b := t.base.(type) {
	case *http.Transport:
		return b
	case interface{ HTTP1Transport() *http.Transport }:
		return b.HTTP1Transport()
	}
	return nil
}

type cancelOnClose struct {
	io.ReadCloser
	once sync.Once
	done func()
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.once.Do(c.done)
	return err
}
