package config

import (
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

// Opciones del proxy, cada una con su variable de entorno y su valor por defecto,
// documentadas en docs/configuration.md. Se leen al usarlas.

// CaseInsensitiveNames (WEBDAV_CASE_INSENSITIVE): buscar nombres sin distinguir
// mayúsculas. La nube las distingue («A.txt» y «a.txt» son dos archivos), y
// responder a «a.txt» con «A.txt» hacía que rclone diera por subido un archivo que no
// lo estaba o, en un cambio solo de mayúsculas, borrara el origen. Por defecto, sin
// distinguir (cómodo con el Explorador de Windows o el Finder); con rclone o restic
// conviene WEBDAV_CASE_INSENSITIVE=false.
func CaseInsensitiveNames() bool {
	if v, ok := envBoolSet("WEBDAV_CASE_INSENSITIVE"); ok {
		return v
	}
	return true
}

// EscapeNames (SAPI_ESCAPE_NAMES, por defecto true): guardar en la nube, cambiando un
// punto por «%2E», los nombres de archivo que la nube no admite (los que empiezan o
// terminan en punto y los que terminan en .tmp). Ver internal/sapi/nombres.go.
func EscapeNames() bool { return envBool("SAPI_ESCAPE_NAMES", true) }

// Seguimiento de la validación tras subir: primera consulta a los
// ValidationFirstWait, luego cada vez más espaciadas hasta ValidationMaxWait; se deja
// tras ValidationMaxFailures consultas fallidas seguidas.
func ValidationFirstWait() time.Duration {
	return envSeconds("SAPI_VALIDATION_FIRST_SEC", 10*time.Second)
}
func ValidationMaxWait() time.Duration { return envSeconds("SAPI_VALIDATION_MAX_SEC", 60*time.Second) }

// EmptyManifestRetryWait (WEBDAV_EMPTY_MANIFEST_RETRY_SEC): si no se pudo subir el
// manifiesto de archivos vacíos, se reintenta tras esta espera.
func EmptyManifestRetryWait() time.Duration {
	return envSeconds("WEBDAV_EMPTY_MANIFEST_RETRY_SEC", 120*time.Second)
}

// Copia en la nube (COPY) sin respuesta: se busca la copia cada CopyPollInterval
// durante CopyWaitBase + tamaño/CopyWaitBytesPerSec, hasta CopyWaitMax.
func CopyPollInterval() time.Duration { return envSeconds("WEBDAV_COPY_POLL_SEC", 10*time.Second) }
func CopyWaitBase() time.Duration     { return envSeconds("WEBDAV_COPY_WAIT_BASE_SEC", 120*time.Second) }
func CopyWaitMax() time.Duration      { return envSeconds("WEBDAV_COPY_WAIT_MAX_SEC", 600*time.Second) }
func CopyWaitBytesPerSec() int64 {
	return int64(envInt("WEBDAV_COPY_WAIT_MBPS", 10)) << 20
}

// envBool lee un sí/no; vacío o no reconocido = def.
func envBool(name string, def bool) bool {
	if v, ok := envBoolSet(name); ok {
		return v
	}
	return def
}

// envBoolSet lee un sí/no; ok = false si la variable está vacía o no se reconoce.
func envBoolSet(name string) (value, ok bool) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	}
	return false, false
}

// envInt lee un entero positivo; vacío o no válido = def.
func envInt(name string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name))); err == nil && n > 0 {
		return n
	}
	return def
}

// envSeconds lee segundos (admite decimales, «0.5»); vacío o no válido = def.
func envSeconds(name string, def time.Duration) time.Duration {
	f, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(name)), 64)
	if err != nil || f <= 0 || math.IsInf(f, 0) || math.IsNaN(f) {
		return def
	}
	return time.Duration(f * float64(time.Second))
}
