# 🌐 Modo web (opcional)

En el modo web el proxy no usa el inicio de sesión del cliente de escritorio
(OAuth `omh windows client`), sino **la misma sesión que la web oficial**
(`micloud.movistar.es`): un Chromium sin pantalla dentro del contenedor guarda la
sesión y las llamadas a SAPI se hacen desde esa pestaña, igual que la web.

Por ahora es **solo para Movistar Cloud** (`micloud.movistar.es`); no está probado
con O2 Cloud.

Úsalo si el emparejamiento por SMS del cliente de escritorio falla o CloudFront lo
bloquea (`403`), o si prefieres que el tráfico sea idéntico al de la web. El modo
normal no cambia y sigue siendo el recomendado si te funciona: su imagen ocupa
~20 MB; la del modo web, varios cientos de MB por Chromium.

## Puesta en marcha

```bash
docker compose up -d
```

1. Abre el panel (`http://IP_DE_TU_SERVIDOR:5000`). La tarjeta **Sesión web**
   sustituye a la de vinculación.
2. Pulsa **🔐 Abrir inicio de sesión**: verás la página de Movistar Cloud del
   Chromium del contenedor y podrás usarla con el ratón y el teclado.
3. Entra con **«Inicia sesión con e-mail y contraseña»** y marca **«No cerrar
   sesión»** (ver *Renovación automática*). También puedes entrar con el móvil y
   el SMS, pero entonces la sesión no se renueva sola.
4. La tarjeta pasa a **Activa** y el servidor WebDAV empieza a funcionar.

La sesión se guarda en el volumen de `/app/tokens` (perfil de Chromium en
`/app/tokens/webprofile`) y sobrevive a reinicios y actualizaciones.

## Cómo funciona

| Operación | Cómo la hace el proxy |
|---|---|
| Llamadas a SAPI (listados, carpetas, renombrar, borrar…) | Desde la pestaña de la web, con sus cookies, la `validationkey` y la cabecera `X-deviceid: web-…` |
| Subidas | Directas a `upload.micloud.movistar.es` con las cookies de la sesión, como el formulario de la web: `acceptasynchronous=true` solo para archivos de más de 200 MiB (`minAsyncUploadFileSizeBytes` de la web) |
| Validación tras subir | Si SAPI responde `MED-1017` (el archivo aún se está validando) al moverlo, se consulta `/media?action=get-validation-status`, como la web, en vez de reintentar a ciegas |
| Descargas | Por la URL firmada del archivo (admite `Range`, sin cookies). Si caduca (dura unas horas), se pide otra y se reintenta |
| Borrado | `/media/<tipo>?action=delete&softdelete=true` (archivos) y `/media/folder?action=softdelete` (carpetas), con el cuerpo JSON de la web. Si SAPI lo rechaza, se usa la llamada genérica de siempre |
| `COPY` de WebDAV | Copia en la nube (`/media/file?action=copy`), sin descargar ni volver a subir. Si no es posible, la copia normal |
| Concurrencia | 2 subidas (`defaultConcurrentUploads` de la web) y 4 descargas simultáneas |

## Renovación automática de la sesión

La sesión de la web (`JSESSIONID`) **caduca a las 24 h del inicio de sesión**,
aunque haya actividad. Si se entró con **«No cerrar sesión»**, Movistar guarda
además una cookie de larga duración (`PLC`, ~90 días) con la que la web vuelve a
entrar sola.

El proxy aprovecha eso: una hora antes de la caducidad (`WEB_SESSION_RENEW_BEFORE`)
borra la cookie de sesión y recarga la web, que entra sola con una sesión nueva de
24 h, sin contraseña ni código. Mientras lo hace, el resto de llamadas esperan:
la `PLC` cambia en cada uso y, si dos peticiones la usaran a la vez, Movistar la
anularía. Por la misma razón las subidas directas no la envían.

El panel muestra cuándo caduca la sesión y si la renovación está activa. Si una
renovación falla, se reintenta cada 10 minutos; la `PLC` nunca se borra por sí
sola (solo al abrir a propósito `/web-login/`), así que un fallo pasajero de red o
de Movistar no obliga a volver a entrar.

### Volver a iniciar sesión sin cerrar la actual

`/web-relogin/` abre un inicio de sesión en un contexto aislado del navegador,
sin tocar la sesión en uso: las transferencias siguen mientras tanto. Al terminar
(con «No cerrar sesión»), el proxy copia las cookies nuevas a la pestaña principal,
recarga y comprueba con una llamada a SAPI que la sesión nueva funciona antes de
darla por buena. Sirve para renovar la `PLC` o cambiar de cuenta sin cortar nada.

> Si entras con el móvil y el SMS no hay `PLC`: la sesión durará 24 h y habrá que
> volver a entrar.

## Papelera

Todo lo que borra el proxy va a la papelera de Movistar Cloud, y la papelera
cuenta para el límite de carpetas de la cuenta. Con
`WEB_TRASH_EMPTY_EVERY=6h` el proxy la vacía periódicamente (`/media/trash?action=empty`,
el botón de la web). Está **desactivado por defecto**: vaciarla borra para siempre
todo lo que contenga. Actívalo si lo que se borra son sobre todo temporales (por
ejemplo, los trozos del `chunker` de rclone).

## Opciones de la cuenta

El panel muestra las opciones que la web lee al cargar
(`/system/information`): tamaño máximo por archivo, límite de elementos por
operación al borrar o mover carpetas (error `FOL-1036`) y caducidad automática de
la papelera. En una cuenta de Movistar Cloud (noviembre de 2026) eran: 4096 MB por
archivo, 1000 elementos por operación de carpeta y 86 400 min (60 días) en la
papelera.

## Variables de entorno del modo web

| Variable | Por defecto | Descripción |
|---|---|---|
| `WEB_SESSION` | `true` | Modo web, el único del proxy (`false` solo en pruebas). |
| `CHROME_PATH` | autodetectar (`/usr/bin/chromium` en la imagen) | Ejecutable de Chromium/Chrome. |
| `CHROME_NO_SANDBOX` | `false` (`true` en la imagen) | Lanza Chromium sin sandbox (necesario como root en contenedores). |
| `WEB_SESSION_RENEW_BEFORE` | `1h` | Margen antes de la caducidad de 24 h para renovar la sesión. |
| `WEB_TRASH_EMPTY_EVERY` | *(desactivado)* | Vaciado periódico de la papelera (p. ej. `6h`; mínimo `1m`). |
| `WEBDAV_MAX_DOWNLOADS` | `1` (`4` en modo web) | Descargas simultáneas a la nube. |
| `WEBDAV_MAX_UPLOADS` | `1` (`2` en modo web) | Subidas simultáneas a la nube. |
| `SAPI_MAX_CONCURRENT` | `1` (en modo web, descargas + subidas + 2) | Llamadas simultáneas a SAPI. |
| `WEBDAV_UPLOAD_SPOOL` | `false` en modo web (`true` en `desktop`) | Copia local de cada subida mientras se envía. En modo web no: la subida va en flujo directo y, si falla, el `PUT` responde `503` para que el cliente la repita. |
| `WEBDAV_QUEUE_RAM_MB` | solo los búferes de las subidas en curso | RAM total para subidas que esperan hueco. Se ajusta a la memoria libre del equipo. Con la cola llena, el proxy deja de leer del cliente (contrapresión). |
| `WEBDAV_QUEUE_DISK_MB` | `0` en modo web (sin límite en `desktop`) | Disco para subidas en cola cuando no caben en RAM. |
| `WEBDAV_UPLOAD_BWLIMIT` | *(sin límite)* | Límite de subida (p. ej. `20M`, también desde el panel). |
| `WEBDAV_SMALLFILE_CACHE_MB` | `32` | Memoria para el contenido de archivos pequeños ya leídos. |
| `DOWNLOAD_URL_MAX_AGE` | `1h` | Cuánto se reutiliza una URL de descarga firmada. |
| `TLS_PREFER_CHACHA` | según el procesador | `true`/`false` fuerza ChaCha20-Poly1305 delante de AES-GCM en el `ClientHello` (ver más abajo). |

## Subidas en modo web

- **Flujo directo sin copia local:** el contenido se reenvía a Movistar según
  llega, sin escribir nada en disco. La subida a la nube no se abre hasta tener
  32 MB en memoria, para que el servidor de subidas no vea una conexión sin datos.
- **Cola común con prioridades:** todas las vías (WebDAV en bruto, WebDAV
  descifrado, montaje) comparten los huecos de `WEBDAV_MAX_UPLOADS`. Las subidas
  masivas (cabecera `X-Upload-Priority: bulk`) ceden el hueco a las normales.
- **Envío `chunked`:** las subidas van siempre sin `Content-Length`. Con él,
  Movistar lee el principio de un archivo que pasa de su límite y se queda
  callado; sin él, lo rechaza al instante con `MED-1048`.
- **Subida parada:** si Movistar deja de leer una subida durante 60 s, se corta y
  el cliente la repite. Si es la única parada y no ha habido un cambio de red, cuenta
  como rechazo por tamaño.
- **Tamaño de trozo automático:** Movistar limita a veces el tamaño por subida de
  una cuenta tras mucho volumen (p. ej. 100 MiB) y lo indica con `MED-1048`. Con
  dos rechazos, el proxy baja el `chunk_size` de las carpetas con `chunker` por
  debajo del límite (o a la mitad), recarga el rclone interno y avisa por
  Telegram. Lo ya subido se sigue leyendo igual. Se puede restablecer desde el
  panel.
- **Nombres de más de 255 caracteres:** se rechazan con `400` (Movistar los
  recorta sin avisar).
- **Fecha de modificación:** se conserva la del original (`X-OC-Mtime`).

## Cifrado integrado (rclone `crypt` + `chunker` por carpeta)

Cada carpeta de la raíz de la nube puede tener su propia configuración de rclone
(`crypt`, `chunker`…), guardada cifrada en el proxy y editable desde el panel. Solo
se aceptan tipos de remoto seguros. El puerto WebDAV público sirve entonces la nube
**ya descifrada**: un rclone interno (`serve webdav` con `combine`) monta cada
carpeta con su configuración y deja el resto tal cual. El WebDAV en bruto (trozos
cifrados) queda solo en `127.0.0.1:WEBDAV_RAW_PORT`.

- El panel muestra los nombres en claro, la actividad por archivo original (no por
  trozo) y el usuario y la contraseña del WebDAV.
- Los registros muestran las rutas descifradas.
- Las carpetas de la raíz se pueden crear desde el panel antes de configurarlas.
- Opcionalmente, el proxy también monta la nube descifrada con FUSE
  (`WEBDAV_MOUNT_PATH`).
- La primera vez se puede importar la clave de `RCLONE_CRYPT_PASSWORD` /
  `RCLONE_CRYPT_PASSWORD2` / `RCLONE_CRYPT_ROOT` (en claro u ofuscadas como en
  `rclone.conf`).

| Variable | Por defecto | Descripción |
|---|---|---|
| `WEBDAV_RAW_PORT` | `8090` | Puerto interno (solo `127.0.0.1`) del WebDAV en bruto. |
| `RCLONE_BINARY` | `rclone` | Ejecutable del rclone interno. |
| `WEBDAV_DECRYPTED_CACHE_MODE` | `off` | `--vfs-cache-mode` del rclone interno (`off`: subida directa). |
| `WEBDAV_MOUNT_PATH` / `WEBDAV_MOUNT_ARGS` / `WEBDAV_MOUNT_CACHE_DIR` | *(sin montaje)* | Montaje FUSE de la nube descifrada. |

## Cambios de red

Si el equipo cambia de conexión (cable ↔ Wi-Fi, otra salida a Internet…), las
conexiones abiertas con Movistar quedan muertas y antes tardaban minutos en
caer. Ahora:

- Todas las conexiones salientes usan `TCP_USER_TIMEOUT` de 60 s (Linux).
- `POST /.red-cambio` (con las credenciales del WebDAV y cuerpo
  `{"via":"…"}`) avisa al proxy del cambio. El proxy entonces:
  - corta al momento las peticiones en curso con Movistar y cierra sus
    conexiones;
  - relanza el navegador de la sesión;
  - avisa por Telegram.

  Se puede llamar desde un *hook* del gestor de red (p. ej. `networkd-dispatcher`).
- Durante los 3 minutos siguientes, un `PUT` fallido responde `503` +
  `Retry-After` para que el cliente reenvíe el archivo en vez de darlo por
  perdido. Esa parada tampoco cuenta como rechazo por tamaño.

## Informe de copias desde el cliente

Un cliente que copia con rclone (p. ej. un script de «Enviar a») puede mandar
cada pocos segundos su `core/stats` a `POST /.nube-estado`. El cuerpo es JSON con:
`id`, `dest`, `bytes`, `total_bytes`, `speed`, `eta`, `errors` y `done`.

- El panel muestra entonces el progreso total del envío, no solo el archivo en
  curso.
- Al terminar (o si deja de informar 10 minutos), Telegram recibe un resumen: un
  bloque por carpeta enviada, con tamaño, duración, velocidad media y los errores
  desglosados, y el total.

## Bot de Telegram

Además de las alertas, el bot responde en el chat configurado (y solo ahí) a
`/estado`, `/progreso`, `/subidas`, `/errores`, `/carpetas` y `/ayuda`. Las
respuestas llevan botones que actualizan el mismo mensaje. El token y el chat se
pueden configurar desde el panel también en modo web, con un botón para detectar
el chat.

Alertas nuevas:
- sesión perdida o descifrado caído más de 2 minutos (y su recuperación);
- límite de carpetas de la cuenta (`FOL-1035`);
- ráfagas de errores de escritura;
- cambio del tamaño de trozo;
- cambios de red.

## Equipos sin AES por hardware

En procesadores sin instrucciones AES (p. ej. Raspberry Pi 4), AES-GCM va por
software y es unas 5 veces más lento que ChaCha20, así que el TLS con Movistar se
comía la CPU. El proxy hace lo mismo que Chrome (BoringSSL) en esos equipos: pone
ChaCha20-Poly1305 delante en el `ClientHello`, sin cambiar el resto de la huella.
Se aplica a las llamadas a SAPI y a las subidas. `TLS_PREFER_CHACHA` lo fuerza.

## Menos memoria

- Chromium arranca en modo ahorrador y con la caché en `/tmp` limitada.
- El rclone interno usa `--use-mmap`.
- No hay cachés en disco por defecto.
- El contenido de los archivos diminutos (p. ej. las fichas del `chunker`) se
  guarda en la base local, así que un listado en frío no los vuelve a pedir a la
  nube.

## Uso con rclone `chunker` + `crypt`

Configuración probada con varios TB de datos:

```ini
[movistar]
type = webdav
url = http://IP_DE_TU_SERVIDOR:5050
# owncloud envía X-OC-Mtime: se conserva la fecha de modificación
vendor = owncloud
user = admin
pass = ...

[movistar-chunk]
type = chunker
remote = movistar:datos
chunk_size = 1Gi
# Sin punto: ver la nota de abajo
name_format = *-chunk###
hash_type = none
transactions = norename

[movistar-crypt]
type = crypt
remote = movistar-chunk:
```

**Nota sobre la longitud de los nombres:** Movistar Cloud admite nombres de hasta
255 caracteres y recorta los más largos sin avisar. `crypt` (codificación
`base32`, la estándar) rellena cada nombre a bloques de 16 bytes y lo alarga un
60 %, así que un nombre original de 144 bytes o más (unos 140 caracteres sin
acentos) ya no cabe. El proxy rechaza esos nombres con `400` para
que rclone lo informe como error; si te pasa, acorta el nombre o usa
`filename_encoding = base64` (nombres más cortos) en un remoto `crypt` nuevo.

**Nota sobre `name_format`:** con el formato por defecto (`*.rclone_chunk.###`)
el chunker renombra cada trozo cambiando su *extensión*, y SAPI no permite cambiar
la extensión de un archivo (`MED-1050`): el proxy tiene que volver a subirlo
entero. Con `*-chunk###` el renombrado es instantáneo.
