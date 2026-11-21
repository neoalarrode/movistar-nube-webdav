package auth

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
	"golang.org/x/sys/cpu"
)

// clientHelloIDFromEnv resuelve la huella TLS (ClientHelloID) que debe imitar el
// proxy. El cliente oficial de Windows enlaza OpenSSL 1.1 (libssl-1_1.dll) y negocia
// HTTP/2 contra CloudFront; la huella JA3/JA4 que emite crypto/tls de Go es
// catalogada como cliente automatizado por AWS WAF Bot Control (403). Por eso se
// imita un navegador convencional en lugar de usar crypto/tls. Configurable con
// UTLS_CLIENT_HELLO para pruebas A/B (chrome_auto, firefox_auto, safari_auto, ios,
// android, edge, chrome_120, golang, randomized).
func clientHelloIDFromEnv() utls.ClientHelloID {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("UTLS_CLIENT_HELLO"))) {
	case "firefox", "firefox_auto":
		return utls.HelloFirefox_Auto
	case "safari", "safari_auto":
		return utls.HelloSafari_Auto
	case "ios":
		return utls.HelloIOS_Auto
	case "android":
		return utls.HelloAndroid_11_OkHttp
	case "edge":
		return utls.HelloEdge_Auto
	case "chrome", "chrome_auto":
		return utls.HelloChrome_Auto
	case "chrome_120":
		return utls.HelloChrome_120
	case "golang":
		return utls.HelloGolang
	case "randomized":
		return utls.HelloRandomized
	default:
		return utls.HelloChrome_Auto
	}
}

// newUClient crea la UConn para helloID, aplicando la especificación a medida si
// la huella es propia.
func newUClient(conn net.Conn, cfg *utls.Config, helloID utls.ClientHelloID) (*utls.UConn, error) {
	spec, custom := chachaFirstSpec(helloID) // sin AES por hardware: ChaCha20 delante (ver preferChaCha)
	if !custom {
		return utls.UClient(conn, cfg, helloID), nil
	}
	uConn := utls.UClient(conn, cfg, utls.HelloCustom)
	if err := uConn.ApplyPreset(&spec); err != nil {
		return nil, fmt.Errorf("utls preset %s: %w", helloID.Str(), err)
	}
	return uConn, nil
}

// FingerprintName devuelve la etiqueta de la huella TLS seleccionada (para logs).
func FingerprintName() string {
	id := clientHelloIDFromEnv()
	return id.Str()
}

// dialTLSContextWithUTLS realiza el dial TCP y envuelve la conexión en una UConn de
// uTLS para que el ClientHello imite al perfil elegido en lugar del de crypto/tls.
// Firma compatible con http2.Transport.DialTLSContext.
func dialTLSContextWithUTLSALPN(ctx context.Context, network, addr string, nextProtos []string) (net.Conn, error) {
	plainConn, err := newDialer().DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}

	host, _, splitErr := net.SplitHostPort(addr)
	if splitErr != nil {
		host = addr
	}

	helloID := clientHelloIDFromEnv()
	uConn, err := newUClient(plainConn, &utls.Config{
		ServerName: host,
		NextProtos: nextProtos,
	}, helloID)
	if err != nil {
		_ = plainConn.Close()
		return nil, err
	}

	if err := uConn.HandshakeContext(ctx); err != nil {
		_ = plainConn.Close()
		return nil, fmt.Errorf("utls handshake with %s: %w", host, err)
	}
	return uConn, nil
}

func dialTLSContextWithUTLS(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
	return dialTLSContextWithUTLSALPN(ctx, network, addr, []string{"h2", "http/1.1"})
}

func dialTLSContextWithUTLSHTTP1(ctx context.Context, network, addr string) (net.Conn, error) {
	return dialTLSContextWithUTLSALPN(ctx, network, addr, []string{"http/1.1"})
}

// dialTLSContextHTTP1Only es como dialTLSContextWithUTLSHTTP1 pero garantiza que el
// ClientHello anuncie solo "http/1.1". Con los perfiles de navegador de uTLS
// (Chrome, Firefox, Edge…) la extensión ALPN del preset ("h2", "http/1.1") manda
// sobre Config.NextProtos, y CloudFront elige h2 aunque el transporte sea
// HTTP/1.1 ("malformed HTTP response \x00\x00\x12\x04…"). Aquí se toma el
// preset, se cambia su ALPN y se aplica como ClientHello a medida.
// http1OnlySpec devuelve el ClientHello del perfil indicado con la extensión ALPN
// reducida a "http/1.1".
func http1OnlySpec(helloID utls.ClientHelloID) (utls.ClientHelloSpec, error) {
	spec, err := utls.UTLSIdToSpec(helloID)
	if err != nil {
		return spec, err
	}
	for _, ext := range spec.Extensions {
		if alpn, ok := ext.(*utls.ALPNExtension); ok {
			alpn.AlpnProtocols = []string{"http/1.1"}
		}
	}
	if preferChaCha() {
		spec.CipherSuites = chachaFirst(spec.CipherSuites)
	}
	return spec, nil
}

// preferChaCha indica si usar ChaCha20-Poly1305 en vez de AES-GCM (ver chachaFirst). Chrome (BoringSSL)
// lo hace en procesadores sin AES por hardware, como el de la Raspberry Pi 4: ahí AES
// va por software y es ~5 veces más lento que ChaCha20 (medido en la Pi 4: 69 frente a
// 322 MB/s por núcleo), y con AES el cifrado de la conexión con Movistar se comía la
// CPU. TLS_PREFER_CHACHA=true/false lo fuerza; por defecto, según el procesador.
func preferChaCha() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TLS_PREFER_CHACHA"))) {
	case "true", "1", "yes", "si", "sí":
		return true
	case "false", "0", "no":
		return false
	}
	return !hasAESHardware()
}

func hasAESHardware() bool {
	return cpu.ARM64.HasAES || cpu.X86.HasAES || cpu.S390X.HasAES
}

// chachaFirst devuelve los cifrados con los ChaCha20 delante (tras el GREASE inicial,
// si lo hay) y sin los AES-GCM de TLS 1.3: aunque ChaCha20 vaya primero, CloudFront
// elige AES si se le ofrece (preferencia del servidor), y Movistar negocia TLS 1.3. Con
// solo ChaCha20 en TLS 1.3 responde igual (comprobado el 20/11/2026: portada 200, API
// sin sesión 401, descarga 200, sin 403 de bot). Los de TLS 1.2 se quedan en su orden.
func chachaFirst(suites []uint16) []uint16 {
	isChaCha := func(c uint16) bool {
		return c == utls.TLS_CHACHA20_POLY1305_SHA256 ||
			c == utls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305 ||
			c == utls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305
	}
	out := make([]uint16, 0, len(suites))
	i := 0
	for ; i < len(suites) && suites[i]&0x0f0f == 0x0a0a; i++ { // GREASE
		out = append(out, suites[i])
	}
	for _, c := range suites[i:] {
		if isChaCha(c) {
			out = append(out, c)
		}
	}
	for _, c := range suites[i:] {
		if !isChaCha(c) && c != utls.TLS_AES_128_GCM_SHA256 && c != utls.TLS_AES_256_GCM_SHA384 {
			out = append(out, c)
		}
	}
	return out
}

// chachaFirstSpec devuelve el ClientHello del perfil con ChaCha20 delante, si toca
// (preferChaCha) y el perfil tiene especificación fija.
func chachaFirstSpec(helloID utls.ClientHelloID) (utls.ClientHelloSpec, bool) {
	if !preferChaCha() {
		return utls.ClientHelloSpec{}, false
	}
	spec, err := utls.UTLSIdToSpec(helloID)
	if err != nil {
		return spec, false
	}
	spec.CipherSuites = chachaFirst(spec.CipherSuites)
	return spec, true
}

func dialTLSContextHTTP1Only(ctx context.Context, network, addr string) (net.Conn, error) {
	helloID := clientHelloIDFromEnv()
	spec, err := http1OnlySpec(helloID)
	if err != nil {
		// Perfiles sin especificación fija (p. ej. HelloGolang): NextProtos basta.
		return dialTLSContextWithUTLSALPN(ctx, network, addr, []string{"http/1.1"})
	}

	plainConn, err := newDialer().DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	host, _, splitErr := net.SplitHostPort(addr)
	if splitErr != nil {
		host = addr
	}
	uConn := utls.UClient(plainConn, &utls.Config{ServerName: host, NextProtos: []string{"http/1.1"}}, utls.HelloCustom)
	if err := uConn.ApplyPreset(&spec); err != nil {
		_ = plainConn.Close()
		return nil, fmt.Errorf("utls preset %s: %w", helloID.Str(), err)
	}
	if err := uConn.HandshakeContext(ctx); err != nil {
		_ = plainConn.Close()
		return nil, fmt.Errorf("utls handshake with %s: %w", host, err)
	}
	if proto := uConn.ConnectionState().NegotiatedProtocol; proto != "" && proto != "http/1.1" {
		_ = uConn.Close()
		return nil, fmt.Errorf("utls handshake with %s: el servidor eligió %q en lugar de http/1.1", host, proto)
	}
	return uConn, nil
}

func dialTLSContextWithUTLSNoALPN(ctx context.Context, network, addr string) (net.Conn, error) {
	return dialTLSContextWithUTLSALPN(ctx, network, addr, nil)
}

// UploadResponseTimeout es lo que se espera la respuesta del servidor de subidas una vez
// enviado el archivo. Con archivos de cientos de MB, O2 tarda a veces más de 60 s en
// responder tras recibirlos (15/11/2026: 189 MB, dos intentos cortados por "timeout
// awaiting response headers"; otro de 153 MB respondió a los 87 s). Por defecto 5 min,
// configurable con UPLOAD_RESPONSE_TIMEOUT_SEC.
func UploadResponseTimeout() time.Duration {
	if v := strings.TrimSpace(os.Getenv("UPLOAD_RESPONSE_TIMEOUT_SEC")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 5 * time.Minute
}

// fingerprintedTransport envía los hosts SAPI (lectura, borrado, metadatos) por
// HTTP/2 con uTLS, y deja las subidas (upload.*) por HTTP/1.1, que ya funcionaba.
// Mezclar un handshake h2 con un stack HTTP/1.x provoca exactamente el error
// "malformed HTTP response \x00\x00\x12\x04..." (frame SETTINGS de HTTP/2).
type fingerprintedTransport struct {
	h1 *http.Transport
	h2 *http2.Transport
	// sapiH1, si no es nil, sustituye a h2 para los hosts SAPI: el perfil
	// "desktop" habla HTTP/1.1, como el cliente oficial (Qt 5). CloudFront corta
	// las peticiones de un dispositivo "fol-" que llegan por HTTP/2.
	sapiH1 *http.Transport
}

func (t *fingerprintedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme == "https" && !strings.HasPrefix(req.URL.Hostname(), "upload.") {
		if t.sapiH1 != nil {
			return t.sapiH1.RoundTrip(req)
		}
		return t.h2.RoundTrip(req)
	}
	return t.h1.RoundTrip(req)
}

// CloseIdleConnections cierra las conexiones abiertas de todos sus transportes.
func (t *fingerprintedTransport) CloseIdleConnections() {
	t.h1.CloseIdleConnections()
	t.h2.CloseIdleConnections()
	if t.sapiH1 != nil {
		t.sapiH1.CloseIdleConnections()
	}
}

// UsesHTTP1ForSAPI indica si los hosts SAPI van por HTTP/1.1 (perfil "desktop").
func (t *fingerprintedTransport) UsesHTTP1ForSAPI() bool {
	return t.sapiH1 != nil
}

// HTTP1Transport expone el transporte HTTP/1.1 subyacente (subidas y URLs planas)
// para verificaciones de configuración en tests.
func (t *fingerprintedTransport) HTTP1Transport() *http.Transport {
	return t.h1
}

// NewFingerprintedTransport construye un RoundTripper cuyo ClientHello lo emite
// uTLS (imitando un navegador convencional). Los hosts SAPI van por HTTP/2 real;
// upload.* y las URLs planas (tests httptest) siguen por HTTP/1.1.
func NewFingerprintedTransport() http.RoundTripper {
	h2 := &http2.Transport{
		DialTLSContext:  dialTLSContextWithUTLS,
		IdleConnTimeout: 90 * time.Second,
	}

	h1 := &http.Transport{
		DialContext: newDialer().DialContext,
		// El host de subida devuelve HTTP 408 ("Server timeout waiting for the
		// HTTP request from the client") si se reutiliza una conexión keep-alive
		// que el servidor ya consideraba caducada. Forzamos conexión nueva por
		// subida; el coste es despreciable frente a los ficheros de HyperBackup.
		DisableKeepAlives:     true,
		ForceAttemptHTTP2:     false,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: UploadResponseTimeout(),
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		MaxConnsPerHost:       100,
	}

	if preferChaCha() {
		// Sin AES por hardware, crypto/tls negocia AES-GCM con upload.* (CloudFront
		// elige AES si se le ofrece) y lo cifra por software: en la Pi 4 era lo que más
		// CPU gastaba al subir (perfil del 20/11/2026: ~45 % del proxy en AES/GHASH a
		// ~28 MB/s). Con uTLS solo se ofrece ChaCha20 en TLS 1.3, como en las descargas.
		h1.DialTLSContext = dialTLSContextHTTP1Only
	}

	ft := &fingerprintedTransport{h1: h1, h2: h2}
	return ft
}

// newSAPIHTTP1Transport es el transporte de los hosts SAPI en el perfil "desktop":
// HTTP/1.1 con keep-alive (como el cliente oficial) y ALPN "http/1.1".
func newSAPIHTTP1Transport() *http.Transport {
	return &http.Transport{
		DialContext:           newDialer().DialContext,
		DialTLSContext:        dialTLSContextHTTP1Only,
		ForceAttemptHTTP2:     false,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		MaxConnsPerHost:       100,
	}
}

// NewHTTP1FingerprintedTransport mantiene la huella uTLS pero fuerza ALPN
// HTTP/1.1. Se usa para comparar el comportamiento del cliente oficial cuando
// CloudFront trata el login de forma distinta a los endpoints SAPI normales.
func NewHTTP1FingerprintedTransport() http.RoundTripper {
	return &http.Transport{
		DialContext:           newDialer().DialContext,
		DialTLSContext:        dialTLSContextWithUTLSHTTP1,
		ForceAttemptHTTP2:     false,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		MaxConnsPerHost:       100,
	}
}

// NewNoALPNHTTP1Transport fuerza HTTP/1.1 sin anunciar ALPN. Algunos perfiles
// de CloudFront tratan de forma distinta el ClientHello cuando ALPN está vacío.
func NewNoALPNHTTP1Transport() http.RoundTripper {
	return &http.Transport{
		DialContext:           newDialer().DialContext,
		DialTLSContext:        dialTLSContextWithUTLSNoALPN,
		ForceAttemptHTTP2:     false,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		MaxConnsPerHost:       100,
	}
}
