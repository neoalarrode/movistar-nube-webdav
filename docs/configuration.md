# Configuración Avanzada, Seguridad y Red 🔧🔒

Esta guía reúne toda la referencia técnica del proxy: variables de entorno completas, alertas de Telegram, arquitectura de autenticación y seguridad, configuración de proxies inversos (Nginx/NPM/Traefik), red Docker, filtro de basura del SO y limitaciones técnicas de la API de Movistar Cloud.

---

## 📋 1. Tabla Completa de Variables de Entorno

### 🔐 Autenticación y Acceso

| Variable | Por defecto | Descripción |
| :--- | :--- | :--- |
| `WEBDAV_USER` | *(vacío)* | Usuario para clientes WebDAV (y para el panel web si `WEBUI_USER` no se define). |
| `WEBDAV_PASSWORD` | *(vacío)* | Contraseña en texto plano para clientes WebDAV (necesaria si algún cliente usa NTLMv2). |
| `WEBDAV_PASSWORD_HASH` | *(vacío)* | Alternativa a `WEBDAV_PASSWORD`: hash SHA-256 en hexadecimal (ej. `sha256:e3b0c44...`). |
| `WEBUI_USER` | `WEBDAV_USER` | Usuario específico para el panel de control web (`:5000`). |
| `WEBUI_PASSWORD` | `WEBDAV_PASSWORD` | Contraseña específica para el panel de control web. |
| `WEBUI_PASSWORD_HASH` | *(vacío)* | Alternativa a `WEBUI_PASSWORD`: hash SHA-256 en hexadecimal para el panel web. |
| `REQUIRE_AUTH` | `false` | Si es `true`, el servidor aborta el arranque si no se han configurado credenciales. |
| `AUTH_DISABLED` | `false` | Si es `true`, desactiva explícitamente la autenticación tanto en WebDAV como en el panel web. |
| `WEBDAV_NTLM` | `auto` | Controla el soporte NTLMv2: en `auto` se ofrece a clientes Windows cuando hay `WEBDAV_PASSWORD`. Admite `true`/`always`/`1` o `false`/`0`/`no`/`disabled`. |

### 🚪 Puertos, Rutas y Almacenamiento

| Variable | Por defecto | Descripción |
| :--- | :--- | :--- |
| `PORT` | `5000` | Puerto interno del servidor del Panel Web. |
| `WEBDAV_PORT` | `8080` | Puerto interno del servidor WebDAV. |
| `WEBDAV_HOST_PORT` | `5050` | Puerto público de WebDAV mostrado en el panel; en Docker Compose toma el valor de `WEBDAV_PORT`. |
| `TOKENS_DIR` | `/app/tokens` | Directorio (`0700`) con la sesión del navegador (`webprofile`), la configuración guardada desde el panel (Telegram, carpetas) y la caché SQLite. |
| `TOKENS_ENCRYPTION_KEY` | *(vacío)* | Clave opcional para cifrar en reposo (**AES-256-GCM**) los secretos guardados en `/app/tokens` (token de Telegram y configuración de las carpetas). |
| `WEBDAV_TMPDIR` | `/tmp` *(o `TMPDIR`)* | Directorio para archivos temporales durante subidas mayores al búfer RAM. Si montas un volumen en disco (ej. `/app/tmp`), define `WEBDAV_TMPDIR=/app/tmp` para usarlo. |
| `TZ` | `Europe/Madrid` | Zona horaria del contenedor (usada en marcas de tiempo de logs y alertas). |

### 🔔 Notificaciones de Telegram (PR #53)

| Variable | Por defecto | Descripción |
| :--- | :--- | :--- |
| `TELEGRAM_BOT_TOKEN` | *(vacío)* | Token del bot de Telegram para alertas operativas y de sesión. |
| `TELEGRAM_CHAT_ID` | *(vacío)* | Identificador del chat o canal de Telegram que recibirá las alertas. |

> [!TIP]
> **Funcionamiento desde el segundo cero:** Desde la versión **v2.5.6** (PR #53), el notificador de Telegram es global y carga `TELEGRAM_BOT_TOKEN` y `TELEGRAM_CHAT_ID` automáticamente al iniciar el proceso, sin necesidad de abrir el panel web previamente. Si además guardas credenciales desde el panel web en `/app/tokens`, estas actualizan las activas al cargar la cuenta; si posteriormente desvinculas la cuenta o limpias esos campos en la web, el notificador vuelve a usar automáticamente las variables de entorno definidas en Docker.

### ⚡ Rendimiento, Concurrencia y Red

| Variable | Por defecto | Descripción |
| :--- | :--- | :--- |
| `MAX_UPLOAD_MB` | `4096` | Tamaño máximo permitido por archivo en subidas `PUT` (en MB). |
| `WEBDAV_BUFFER_MB` | `32` | Tamaño máximo del búfer en RAM (MB) antes de hacer spool a disco (óptimo para Duplicati y Restic). |
| `WEBDAV_MAX_DOWNLOADS` | `4` | Descargas simultáneas hacia la nube. |
| `WEBDAV_MAX_UPLOADS` | `2` | Subidas simultáneas hacia la nube (la web sube de 2 en 2). |
| `UPLOAD_RESPONSE_TIMEOUT_SEC` | `300` | Tiempo máximo de espera (segundos) tras enviar el archivo hasta que el servidor de subida responde. |
| `SYNC_INTERVAL_SEC` | `60` | Intervalo de sincronización incremental de metadatos (mínimo `15` segundos). |
| `WEBDAV_CASE_INSENSITIVE` | `true` | Buscar nombres sin distinguir mayúsculas: si no hay un archivo con el nombre exacto, se usa el que solo difiere en mayúsculas (cómodo con el Explorador de Windows o el Finder). La nube sí las distingue, así que con rclone, restic o Hyper Backup conviene `false`: si no, rclone puede dar por subido un archivo que no lo estaba o, en un cambio solo de mayúsculas, borrar el origen. |
| `WEBDAV_UPLOAD_SPOOL` | `false` | Copia local de cada subida mientras se envía a la nube. Sin ella no se escribe nada en disco: la subida va en flujo directo y, si falla, el `PUT` responde `503` para que el cliente la repita. Con `true`, un fallo se reintenta sin el cliente (Hyper Backup lo agradece). |
| `WEBDAV_QUEUE_RAM_MB` | solo los búferes en curso | RAM total para subidas que esperan hueco (se ajusta a la memoria libre). Con la cola llena, el proxy deja de leer del cliente hasta que haya hueco. |
| `WEBDAV_QUEUE_DISK_MB` | `0` | Disco para las subidas en cola que no caben en RAM. Con clientes que mandan varias subidas a la vez por la misma conexión (restic → rclone) conviene darle espacio: un cuerpo que no puede subir en flujo va entero al disco y el cliente no se bloquea. |
| `WEBDAV_UPLOAD_BWLIMIT` | *(sin límite)* | Límite de subida a la nube (`20M`, `500K`, `off`), también ajustable desde el panel. |
| `WEBDAV_SMALLFILE_CACHE_MB` | `32` | Tope de memoria para el contenido de archivos pequeños ya leídos. |
| `DOWNLOAD_URL_MAX_AGE` | `1h` | Tiempo que se reutiliza una URL de descarga firmada. |
| `TLS_PREFER_CHACHA` | según el procesador | Pone ChaCha20-Poly1305 delante de AES-GCM en el `ClientHello`, como hace Chrome en equipos sin AES por hardware (p. ej. Raspberry Pi 4, donde AES por software es ~5 veces más lento y el TLS se comía la CPU). `true`/`false` lo fuerza. |
| `SHUTDOWN_TIMEOUT_SEC` | `30` | Tiempo máximo de espera (segundos) para un apagado ordenado de subidas en curso. |
| `SAPI_MAX_CONCURRENT` | descargas + subidas + 2 | Llamadas concurrentes máximas a SAPI. |
| `SAPI_PACING_MS` | `100` | Pausa mínima en milisegundos entre peticiones secuenciales a SAPI. |
| `TRUSTED_PROXIES` | *(vacío)* | Lista separada por comas de IPs/CIDRs de proxies inversos de confianza para evaluar `X-Forwarded-For` / `X-Real-IP`. |
| `BRUTE_FORCE_EXCLUDED_IPS` | `127.0.0.1,::1,172.16.0.0/12` | IPs/subredes exentas de bloqueo HTTP `429` por intentos fallidos (cubre las redes bridge de Docker `172.17.0.0/16`, `172.18.0.0/16`, etc.; mantiene Basic Auth activo). |
| `LOG_LEVEL` | *(vacío)* | Si se establece en `debug`, habilita trazas detalladas `[DEBUG]` en el registro del panel. |

### 🕵️ Huella TLS, User-Agent y sesión web

| Variable | Por defecto | Descripción |
| :--- | :--- | :--- |
| `SAPI_USER_AGENT` | `Mozilla/5.0 ... Chrome/133.0.0.0 ...` | Fuerza el `User-Agent` enviado a SAPI (coherente con la huella TLS de Chrome). |
| `UTLS_CLIENT_HELLO` | `chrome_auto` | Huella TLS (`ClientHello`) hacia SAPI. Otros valores para pruebas: `golang`, `firefox_auto`, `safari_auto`, `ios`, `android`, `edge`, `chrome_120`, `randomized`. |
| `WEB_SESSION` | `true` | [Modo Web](web-mode.md), el único del proxy (`false` solo en pruebas). Sus variables específicas (`WEB_SESSION_RENEW_BEFORE`, `WEB_TRASH_EMPTY_EVERY`, `CHROME_PATH`, `CHROME_NO_SANDBOX`) se detallan en esa guía. |

---

## 🔔 2. Sistema de Alertas por Telegram

Con `TELEGRAM_BOT_TOKEN` y `TELEGRAM_CHAT_ID` configurados en tu `docker-compose.yml` (o desde el panel web), el proxy envía notificaciones automáticas ante los siguientes eventos:

* 🚀 **Arranque del servicio:** Confirma que el contenedor ha iniciado y muestra los puertos de escucha de WebDAV y Panel Web.
* ⚠️ **Sesión web sin iniciar:** si arranca sin sesión, avisa para que entres desde el panel.
* ❌ **Fallos críticos de inicialización:** notifica antes de detenerse si falla la inicialización del cliente SAPI o el arranque de Chromium.
* ⚠️ **Sesión caducada:** avisa cuando la sesión de Movistar Cloud caduca y hay que volver a entrar (con control antispam de **1 hora** por tipo de alerta).

---

## 🔒 3. Seguridad y Autenticación Detallada

* **Panel Web con Login Moderno (`:5000`):**
  * Formulario de inicio de sesión compatible con autocompletado de gestores de contraseñas (*Bitwarden*, *1Password*, *KeePass*, *Google Chrome*, *Apple Keychain*).
  * Sesiones basadas en cookies `HttpOnly` / `SameSite`, cabeceras de seguridad (`Content-Security-Policy`, `Cross-Origin-Opener-Policy`, `Cross-Origin-Resource-Policy`, `Permissions-Policy`), validación de origen frente a CSRF y cierre de sesión explícito (`/logout`).
* **Clientes WebDAV (`:5050` -> `:8080`, RFC 4918):**
  * Compatibilidad estándar con **HTTP Basic Auth** y **NTLMv2** (para *Duplicati*, *Synology HyperBackup*, *Rclone*, *Windows WebClient*, *macOS Finder*).
  * **Validación en Tiempo Constante:** Tanto el panel web como WebDAV validan usuario y contraseña usando `subtle.ConstantTimeCompare` sobre hashes SHA-256 para prevenir ataques de temporización.
  * **Normalización de usuarios Windows:** Los prefijos de dominio habituales de Windows WebClient (`DOMAIN\usuario` o `\usuario`) y formatos UPN (`usuario@dominio`) se normalizan automáticamente a `usuario`, ignorando espacios accidentales.
  * **Preservación intacta de contraseñas:** La contraseña se evalúa exactamente tal como se envía, permitiendo contraseñas complejas con dos puntos (`:`) o espacios internos.
  * **Desafío RFC 4918 (`401 Unauthorized`):** Toda petición WebDAV no autenticada o con credenciales erróneas recibe un `401 Unauthorized` junto a `WWW-Authenticate: Basic realm="WebDAV"`.
  * **Descubrimiento Windows WebClient:** La petición inicial `OPTIONS /` sin credenciales devuelve `200 OK` con `MS-Author-Via: DAV` y cabeceras `DAV: 1, 2`, permitiendo al explorador de Windows identificar el servicio antes de solicitar credenciales en `PROPFIND /`.
* **Contenedor sin privilegios (`non-root`):**
  * La imagen oficial ejecuta el servidor bajo el usuario sin privilegios `app` (`UID 10001`). En actualizaciones sobre volúmenes existentes, el script `docker-entrypoint.sh` ajusta los permisos de `/app/tokens` (y de `WEBDAV_TMPDIR` si está definido) antes de soltar privilegios con `su-exec`.
* **Cifrado de secretos en reposo:**
  * Si defines `TOKENS_ENCRYPTION_KEY`, los secretos que guarda el proxy en `/app/tokens` (el token de Telegram y la configuración de las carpetas) se cifran con **AES-256-GCM**.

---

## 🌐 4. Topologías de Red Docker y Proxies Inversos

* **No exponer directamente a Internet:** Si necesitas acceder desde fuera de tu red local, coloca siempre un proxy inverso con **TLS/HTTPS** delante (Caddy, Nginx, Traefik) o utiliza un túnel VPN (WireGuard / Tailscale).
* **Docker Bridge (por defecto):** Mediante `BRUTE_FORCE_EXCLUDED_IPS` (`127.0.0.1,::1,172.16.0.0/12`), el loopback y las subredes puente estándar de Docker (`172.17.0.0/16`, `172.18.0.0/16`, etc.) quedan exentas del bloqueo por fuerza bruta (`HTTP 429`). Esto evita que varios clientes compartiendo el gateway de Docker queden bloqueados accidentalmente. **La autenticación Basic Auth se exige y valida en todo momento; la exclusión nunca elude la contraseña.**
* **Proxy Inverso (Nginx, Nginx Proxy Manager, Traefik, Caddy, Synology DSM Reverse Proxy):**
  * Al configurar `TRUSTED_PROXIES` (ej. `127.0.0.1,172.18.0.0/16`), el proxy extrae la IP real del cliente de `X-Forwarded-For` o `X-Real-IP`. Si la petición proviene de un origen no confiable, las cabeceras de reenvío se descartan estrictamente para evitar spoofing.
  * ⚠️ **Importante para subidas grandes (`413 Request Entity Too Large`):** Si sitúas el contenedor detrás de **Nginx** o **Nginx Proxy Manager (NPM)**, desactiva el límite de tamaño de cuerpo y el buffering en la pestaña *Advanced* (*Custom Nginx Configuration*) para no cortar subidas WebDAV de varios GB:
    ```nginx
    client_max_body_size 0;
    proxy_request_buffering off;
    proxy_read_timeout 3600s;
    proxy_send_timeout 3600s;
    ```
  * **Nota para clientes Linux GVFS (Thunar / Nautilus `davs://`):** `gvfsd-dav` / `libsoup` presenta una limitación histórica de entero con signo de 32 bits (**2 GiB**) por archivo; para subir archivos de entre 2 GB y 4 GB desde Linux utiliza **Rclone** o `curl`.
* **Docker Host (`network_mode: host`):** En Linux nativo permite observar la IP real directa del socket. En Docker Desktop (Windows/macOS), debido a la máquina virtual interna, la topología recomendada es el modo bridge con mapeo de puertos o proxy inverso.

---

## 🧹 5. Filtro Inteligente de Basura del SO (*DevNull Absorber*)

Al conectar la unidad en red, los sistemas operativos intentan crear archivos de indexación, miniaturas y papeleras que ensucian la nube. El proxy integra un absorbedor en memoria que responde con éxito al cliente pero evita subir estos elementos a Movistar Cloud:

* **Windows:** `desktop.ini`, `autorun.inf`, `autorun.ini`, `thumbs.db`, `ehthumbs.db`, `ehthumbs_vista.db`, carpetas `$RECYCLE.BIN` y `System Volume Information`.
* **macOS:** `.DS_Store`, archivos AppleDouble `._*`, `.Spotlight-V100`, `.Trashes`, `.fseventsd`, `.TemporaryItems`, `.DocumentRevisions-V100`, `.apdisk`, `.VolumeIcon.icns`.
* **Linux:** Carpetas `.Trash-*` y archivos `.directory` de KDE.

---

## ⚠️ 6. Limitaciones Técnicas de Movistar Cloud (SAPI)

Aunque Movistar Cloud ofrece **almacenamiento total ilimitado**, la infraestructura de Telefónica (SAPI) impone ciertas restricciones técnicas:

1. **📦 Límite de tamaño por archivo individual (4 GB / 4096 MB):**
   * La API SAPI rechaza cualquier archivo individual que supere los **4 GB (4096 MB)**.
   * **Duplicati** y **Synology HyperBackup** no se ven afectados porque fragmentan las copias en volúmenes o bloques pequeños.
   * Si necesitas subir archivos individuales mayores de 4 GB mediante **Rclone**, configura el overlay `chunker` siguiendo la [Guía de Clientes de Backup](backup-clients.md#-configuración-avanzada-archivos-de-más-de-4-gb-chunker-y-cifrado-crypt).
2. **🚦 Concurrencia y Protección frente a CloudFront WAF (`403`):**
   * El proxy controla la concurrencia mediante semáforos configurables (`WEBDAV_MAX_DOWNLOADS=4`, `WEBDAV_MAX_UPLOADS=2`, `SAPI_MAX_CONCURRENT=1` en modo estándar) y un *circuit breaker* por servidor (`waf_breaker.go`) que pausa temporalmente las peticiones si CloudFront responde `403`, devolviendo `503 + Retry-After` a los clientes en lugar de lanzar ráfagas de reintentos.
3. **📏 Longitud máxima de nombres (255 caracteres):**
   * Movistar Cloud recorta silenciosamente los nombres que superan los **255 caracteres**. Para evitar pérdidas o ficheros ilegibles (especialmente con `rclone crypt`), el proxy rechaza con `HTTP 400` cualquier operación `PUT`, `MKCOL`, `MOVE` o `COPY` cuyo nombre de destino exceda los 255 caracteres.
4. **🔤 Caracteres reservados en nombres de archivo:**
   * Evita caracteres incompatibles con URLs o APIs REST (`\`, `:`, `*`, `?`, `"`, `<`, `>`, `|`) en nombres de archivos y directorios.
