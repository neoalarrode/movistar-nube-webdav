# Movistar Nube WebDAV ☁️

Proxy WebDAV para **Movistar Cloud** que trabaja a través de su **sesión web oficial**. Con él puedes usar la nube como una unidad de red, desde rclone, desde tus copias de seguridad o desde un servidor multimedia, con cifrado integrado y subidas robustas.

> [!NOTE]
> **Proyecto independiente y no oficial.** No está afiliado ni respaldado por Telefónica, S.A. ni Movistar. «Movistar» se usa solo para indicar con qué servicio es compatible. Consulta [DISCLAIMER.md](DISCLAIMER.md) y [TRADEMARKS.md](TRADEMARKS.md).

> [!IMPORTANT]
> **Origen.** Este proyecto nace del código de [MovistarWebDAVProxy](https://github.com/uniextra/MovistarWebDAVProxy) (licencia MIT), al que se aportó el modo web, y conserva su historial y la autoría de cada cambio. Hoy es **un proyecto aparte, con una filosofía y un funcionamiento completamente distintos**:
> - se centra en la sesión web de Movistar Cloud, en lugar de imitar a la app oficial de escritorio;
> - integra el cifrado y la gestión de los trozos en el propio proxy, para que los clientes no necesiten configuración;
> - está pensado también para equipos pequeños.
>
> No es una versión del original, ni este proyecto es responsable de él (ni al revés).

<p align="center">
  <img src="screenshot.png" alt="Panel web" width="850">
</p>

## 📑 Índice

1. [Por qué la sesión web](#-por-qué-la-sesión-web)
2. [Qué hace](#-qué-hace)
3. [Puesta en marcha](#-puesta-en-marcha)
4. [Conectar clientes](#-conectar-clientes)
5. [Documentación](#-documentación)
6. [Estado del proyecto](#-estado-del-proyecto)
7. [Licencia](#-licencia)

## 🌐 Por qué la sesión web

El proxy usa Movistar Cloud exactamente como lo usa su web, con un Chromium sin pantalla que guarda la sesión:
- las llamadas a la API salen desde la propia pestaña de la web;
- las subidas van por el mismo formulario que usa la web;
- las descargas usan las URL firmadas.

Al comportarse como la web oficial, no choca con el cortafuegos de Movistar (CloudFront WAF), que bloquea con `403`/`429` a los clientes que no se parecen a ninguno de los suyos.

## ✨ Qué hace

- **Sesión que no se cae:**
  - renovación automática con «No cerrar sesión»;
  - nuevo inicio de sesión desde el panel sin cerrar el actual (`/web-relogin/`);
  - la sesión no se pierde por fallos pasajeros.
- **Cifrado integrado por carpeta:** cada carpeta de la raíz puede llevar su configuración de rclone (`crypt`, `chunker`…), guardada cifrada y editable desde el panel.
  - El WebDAV sirve esas carpetas ya descifradas.
  - Los clientes solo necesitan un WebDAV normal, sin rclone ni claves en cada máquina.
- **Subidas robustas:**
  - flujo directo sin escribir en disco (opcional);
  - una cola común con prioridades y contrapresión;
  - corte de las subidas que Movistar deja de leer;
  - **tamaño de trozo automático**: si Movistar limita el tamaño por subida de la cuenta (`MED-1048`), el `chunk_size` se ajusta solo, sin tocar ningún cliente.
- **Ligero en equipos pequeños:**
  - TLS con ChaCha20 en procesadores sin AES por hardware;
  - Chromium en modo ahorrador;
  - cachés con tope;
  - listados en frío rápidos.
- **Red:**
  - las conexiones muertas se cierran en un minuto;
  - aviso de cambio de conexión del equipo (`POST /.red-cambio`): corta lo que va por la red vieja y lo repite por la nueva.
- **Telegram:** bot con comandos y botones (`/estado`, `/progreso`, `/subidas`, `/errores`, `/carpetas`) y avisos de:
  - sesión;
  - límite de tamaño;
  - límite de carpetas;
  - errores de escritura;
  - fin de cada copia.
- **Panel web:**
  - actividad en tiempo real;
  - configuración de cada carpeta;
  - credenciales del WebDAV;
  - límite de subida en caliente;
  - progreso de las copias que informan los clientes (`POST /.nube-estado`).

## 🐳 Puesta en marcha

Necesitas Docker. La imagen está publicada para amd64 y arm64:

```bash
curl -O https://raw.githubusercontent.com/neoalarrode/movistar-nube-webdav/main/docker-compose.yml
WEBDAV_USER=usuario WEBDAV_PASSWORD=contraseña docker compose up -d
```

1. Abre el panel en `http://IP_DEL_EQUIPO:5000` con el mismo usuario y contraseña.
2. Pulsa **«Abrir inicio de sesión»**, entra con tu e-mail y contraseña de Movistar Cloud y marca **«No cerrar sesión»**. Así el proxy renueva la sesión solo.
3. El WebDAV queda en `http://IP_DEL_EQUIPO:5050`.

Las variables principales y todas las opciones están en [docs/configuration.md](docs/configuration.md) y [docs/web-mode.md](docs/web-mode.md).

## 🔌 Conectar clientes

Cualquier cliente WebDAV sirve: el Explorador de Windows, el Finder, `davfs2`, rclone (`vendor = owncloud`), servidores multimedia o herramientas de copia de seguridad. Hay guías en [docs/backup-clients.md](docs/backup-clients.md) y [docs/network-drive.md](docs/network-drive.md).

> [!TIP]
> No expongas los puertos `5000` y `5050` a Internet sin cifrar. Para acceder desde fuera, usa una VPN (WireGuard, Tailscale…) o un proxy inverso con HTTPS.

## 📚 Documentación

- [Modo web: sesión, subidas, cifrado integrado, red, Telegram e informe de copias](docs/web-mode.md)
- [Configuración, variables de entorno, seguridad y proxies inversos](docs/configuration.md)
- [Clientes de copia de seguridad y rclone](docs/backup-clients.md)
- [Unidad de red en Windows, macOS y Linux](docs/network-drive.md)
- [Registro de cambios](CHANGELOG.md)

## 🚧 Estado del proyecto

El modo nativo heredado del proyecto original (inicio de sesión por SMS/OAuth e imitación de la app de escritorio) ya está retirado ([#1](https://github.com/neoalarrode/movistar-nube-webdav/issues/1)): el proxy funciona solo con la sesión web y tiene una única imagen. El trabajo se organiza con [issues](https://github.com/neoalarrode/movistar-nube-webdav/issues) y pull requests.

## 📄 Licencia

[MIT](LICENSE). Conserva el aviso de copyright del proyecto original junto al de este.
