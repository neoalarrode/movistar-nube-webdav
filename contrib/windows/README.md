# Nube en Windows: unidad de red y «Enviar a»

`instalar-nube.ps1` deja en un PC con Windows 10/11:

- **La unidad «Nube» (N:)**, montada con rclone contra el WebDAV de movistar-nube-webdav, con una caché de escritura local.
  - Un vigilante oculto la monta al iniciar sesión.
  - **La vuelve a montar sola** si rclone se cierra o deja de responder, por ejemplo tras perder la conexión.
  - Si el servidor no responde, espera a que vuelva antes de montar.
  - Cada remontaje queda anotado en `%LOCALAPPDATA%\rclone-nube\vigilante.log`.
- **«Enviar a → Nube (subida directa)»** en el menú del botón derecho, para subir archivos y carpetas enteras sin pasar por la caché:
  - leyendo el original a la velocidad de la subida;
  - con una cola, de modo que varios envíos esperan su turno;
  - con reintentos del archivo entero si se corta la red;
  - con el progreso en el panel del servidor y un resumen por Telegram al terminar (informe de copias, `POST /.nube-estado`).
- **Archivos que ya existen:** antes de copiar se comparan origen y destino. Si hay coincidencias, se pregunta qué hacer:
  - **omitir todos**;
  - **reemplazar todos**;
  - **decidir uno a uno**, en una lista con el tamaño y la fecha de cada lado.

## Instalación

En PowerShell **como administrador** (hace falta para instalar WinFsp):

```powershell
Invoke-WebRequest https://raw.githubusercontent.com/neoalarrode/movistar-nube-webdav/main/contrib/windows/instalar-nube.ps1 -OutFile $env:TEMP\instalar-nube.ps1
Set-ExecutionPolicy -Scope Process Bypass; & $env:TEMP\instalar-nube.ps1
```

Pide:
1. **La URL del WebDAV.** Puede ser la raíz (`http://SERVIDOR:5050`) o una carpeta (`http://SERVIDOR:5050/datos`). Se puede pasar con `-Url`; con `-Letra` se cambia la letra de la unidad.
2. **El usuario y la contraseña del WebDAV** (`WEBDAV_USER` / `WEBDAV_PASSWORD` del servidor, no los del panel).

Instala rclone y WinFsp con `winget`. Se puede volver a ejecutar para actualizar: conserva la URL y las credenciales, salvo que se pida cambiarlas.

> Aunque el instalador corre como administrador, la unidad se monta como tu usuario normal. Una unidad montada desde una ventana de administrador no aparece en el Explorador.

## Problemas

- **La unidad no aparece:** reinicia la sesión de Windows, o mira `vigilante.log` y `mount.log` en `%LOCALAPPDATA%\rclone-nube`.
- **Desinstalar:**
  1. Borra `Nube.cmd` de la carpeta de inicio (`shell:startup`).
  2. Borra el acceso «Nube (subida directa)» de `shell:sendto`.
  3. Borra el remoto con `rclone config delete nube`.

## Pruebas

`pruebas/probar.sh` comprueba, sin Windows (con PowerShell 7 y rclone):
- la sintaxis del instalador y de los scripts que genera;
- «Enviar a» sin duplicados y con duplicados en sus tres modos, contra una carpeta local.

Se ejecuta en la CI en cada cambio. Las partes que solo existen en Windows (winget, WinFsp, el montaje y las ventanas) hay que probarlas en un PC.
