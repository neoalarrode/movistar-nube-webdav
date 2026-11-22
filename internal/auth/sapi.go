package auth

import (
	"os"
	"strings"
)

// SAPIUserAgent es el User-Agent de navegador con el que se llama a SAPI (coherente
// con la huella TLS de Chrome de tls_fingerprint.go).
const SAPIUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36"

// GetSAPIBaseURL es la URL base de la API (SAPI_BASE_URL la sustituye, p. ej. en pruebas).
func GetSAPIBaseURL() string {
	if env := strings.TrimSpace(os.Getenv("SAPI_BASE_URL")); env != "" {
		return env
	}
	return "https://micloud.movistar.es/sapi"
}

// GetUploadBaseURL es la URL base del servidor de subidas.
func GetUploadBaseURL() string {
	return "https://upload.micloud.movistar.es"
}

// GetSAPIUserAgent permite cambiar la huella de navegador de SAPI sin
// recompilar cuando CloudFront vuelve a endurecer sus reglas.
func GetSAPIUserAgent() string {
	if value := strings.TrimSpace(os.Getenv("SAPI_USER_AGENT")); value != "" {
		return value
	}
	return SAPIUserAgent
}

// GetSAPIUserAgentInfo devuelve el User-Agent activo y true si se ha modificado
// mediante SAPI_USER_AGENT respecto a la constante por defecto SAPIUserAgent.
func GetSAPIUserAgentInfo() (string, bool) {
	def := SAPIUserAgent
	if value := strings.TrimSpace(os.Getenv("SAPI_USER_AGENT")); value != "" && value != def {
		return value, true
	}
	return def, false
}

// SanitizeDeviceID deja el identificador de dispositivo en el formato de la web:
// "web-…" sin '+', '/', '='.
func SanitizeDeviceID(devID string) string {
	devID = strings.TrimSpace(devID)
	if strings.HasPrefix(devID, "fol-") {
		clean := strings.TrimPrefix(devID, "fol-")
		clean = strings.NewReplacer("+", "", "/", "", "=", "").Replace(clean)
		return "web-" + clean
	}
	return devID
}
