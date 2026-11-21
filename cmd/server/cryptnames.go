package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"movistar-webdav-proxy/internal/autochunk"
	"movistar-webdav-proxy/internal/cryptname"
	"movistar-webdav-proxy/internal/logger"
	"movistar-webdav-proxy/internal/notifier"
	"movistar-webdav-proxy/internal/rcloneconf"
	"movistar-webdav-proxy/internal/rclonesvc"
	"movistar-webdav-proxy/internal/sapi"
	"movistar-webdav-proxy/internal/vault"
	"movistar-webdav-proxy/internal/webdav"
)

// prepareVault carga las configuraciones de rclone por carpeta guardadas en el
// proxy. La primera vez importa la clave de RCLONE_CRYPT_PASSWORD[2]/RCLONE_CRYPT_ROOT
// (como en rclone.conf, ofuscadas o en claro) o la de crypt.json como configuración
// de esa carpeta.
func prepareVault() {
	if _, err := vault.Load(); err != nil {
		logger.Log("[Descifrado] ⚠️ %v", err)
	}
	if vault.Current() == nil {
		if p := strings.TrimSpace(os.Getenv("RCLONE_CRYPT_PASSWORD")); p != "" {
			c := vault.Defaults()
			c.Password, c.Password2 = p, strings.TrimSpace(os.Getenv("RCLONE_CRYPT_PASSWORD2"))
			if r := strings.TrimSpace(os.Getenv("RCLONE_CRYPT_ROOT")); r != "" {
				c.Root = r
			}
			if err := vault.Save(c); err != nil {
				logger.Log("[Descifrado] ⚠️ No se pudo guardar la clave de RCLONE_CRYPT_PASSWORD: %v", err)
			} else {
				logger.Log("[Descifrado] 🔑 Clave de cifrado importada del entorno y guardada en el proxy.")
			}
		}
	}
	if err := vault.LoadFolders(); err != nil {
		logger.Log("[Descifrado] ⚠️ %v", err)
	}
	for _, f := range vault.Folders() {
		logger.Log("[Descifrado] 📁 Carpeta «%s» con configuración propia de rclone", f.Name)
	}
}

// internalDecryptedAddr es donde escucha el rclone interno; delante va el WebDAV
// público del proxy (decryptedGateway).
const internalDecryptedAddr = "127.0.0.1:8091"

// folderNames guarda el descifrador de nombres de cada carpeta configurada.
var folderNames sync.Map // nombre de carpeta → *cryptname.Cipher (o nil)

// folderSizers guarda cómo calcular los tamaños en bruto de cada carpeta configurada.
var folderSizers sync.Map // nombre de carpeta → *uploadSizer

func refreshFolderNames() {
	folderNames.Range(func(k, _ interface{}) bool { folderNames.Delete(k); return true })
	folderSizers.Range(func(k, _ interface{}) bool { folderSizers.Delete(k); return true })
	for _, f := range vault.Folders() {
		if n := vault.NamesFor(f); n != nil {
			folderNames.Store(f.Name, n)
		}
		if ch, err := rcloneconf.Remap(f.Config, f.Top, "nube:"+f.Name, ""); err == nil {
			ch.CapChunkSize(autochunk.Chunk(), rcloneconf.ParseSize) // igual que el rclone interno
			if sz := newUploadSizer(ch); sz != nil {
				folderSizers.Store(f.Name, sz)
			}
		}
	}
}

// setupCrypt arranca el rclone interno que sirve en el puerto WebDAV público la
// nube con cada carpeta según su configuración, y el descifrado de nombres del panel.
func setupCrypt(webdavPort, rawPort string) *rclonesvc.Service {
	refreshFolderNames()
	webdav.SetDisplayName(func(p string) string {
		var out string
		folderNames.Range(func(k, v interface{}) bool {
			if d := v.(*cryptname.Cipher).DecryptPath(p, k.(string)); d != "" {
				out = k.(string) + "/" + d
				return false
			}
			return true
		})
		return out
	})

	pass := os.Getenv("WEBDAV_PASSWORD")
	if pass == "" && os.Getenv("WEBDAV_PASSWORD_HASH") != "" {
		logger.Log("[Descifrado] ⚠️ Con WEBDAV_PASSWORD_HASH el rclone interno no puede autenticarse contra el proxy: usa WEBDAV_PASSWORD.")
	}
	svc := rclonesvc.New(rclonesvc.Options{
		Binary:      os.Getenv("RCLONE_BINARY"),
		UpstreamURL: "http://127.0.0.1:" + rawPort,
		User:        os.Getenv("WEBDAV_USER"),
		Pass:        pass,
		Addr:        internalDecryptedAddr,
		CacheMode:   os.Getenv("WEBDAV_DECRYPTED_CACHE_MODE"),
		MountPath:   strings.TrimSpace(os.Getenv("WEBDAV_MOUNT_PATH")),
		MountArgs:   strings.Fields(os.Getenv("WEBDAV_MOUNT_ARGS")),
		MountCache:  strings.TrimSpace(os.Getenv("WEBDAV_MOUNT_CACHE_DIR")),
		OnRestart:   refreshFolderNames,
	})
	setupAutoChunk(svc)
	if len(vault.Folders()) > 0 {
		svc.Restart()
	}
	return svc
}

// setupAutoChunk baja solo el tamaño de trozo cuando Movistar rechaza subidas por
// tamaño (MED-1048) y recarga el rclone interno con el nuevo (agrupando los rechazos
// de 15 s: las subidas en curso se cortan y el cliente las reintenta).
func setupAutoChunk(svc *rclonesvc.Service) {
	autochunk.Load()
	if c := autochunk.Chunk(); c > 0 {
		logger.Log("[Descifrado] ✂️ Trozos de %d MiB (rebajado automáticamente: Movistar rechaza subidas más grandes)", c>>20)
	}
	var mu sync.Mutex
	var pending bool
	autochunk.OnChange(func() {
		c := autochunk.Chunk()
		if c > 0 {
			logger.Log("[Descifrado] ✂️ Movistar rechaza las subidas grandes (MED-1048): trozos de %d MiB a partir de ahora", c>>20)
			_ = notifier.Alert(fmt.Sprintf("autochunk_%d", c>>20), "✂️", "Movistar ha bajado el tamaño máximo de subida",
				fmt.Sprintf("Movistar rechaza archivos grandes (MED-1048). El proxy sube ahora en trozos de %d MiB; lo ya subido se sigue leyendo igual.", c>>20), "")
		} else {
			logger.Log("[Descifrado] ✂️ Tamaño de trozo restablecido al de la configuración")
		}
		mu.Lock()
		if pending {
			mu.Unlock()
			return
		}
		pending = true
		mu.Unlock()
		go func() {
			time.Sleep(15 * time.Second) // agrupa los rechazos de las subidas que van en paralelo
			mu.Lock()
			pending = false
			mu.Unlock()
			svc.Restart()
		}()
	})
	sapi.UploadRejected = func(size int64) {
		if size > 0 {
			autochunk.Rejected(size, autochunk.Chunk())
		}
	}
	sapi.UploadAccepted = autochunk.Accepted
}

// rootDirLister lista las carpetas de la raíz de la nube con el propio listado del
// proxy (base local), sin lanzar rclone. nil si no se puede.
func rootDirLister(srv *webdav.Server) func() []string {
	return func() []string {
		fs := srv.FS()
		if fs == nil {
			return nil
		}
		f, err := fs.OpenFile(context.Background(), "/", os.O_RDONLY, 0)
		if err != nil {
			return nil
		}
		defer f.Close()
		infos, err := f.Readdir(-1)
		if err != nil {
			return nil
		}
		var dirs []string
		for _, fi := range infos {
			if fi.IsDir() {
				dirs = append(dirs, fi.Name())
			}
		}
		return dirs // nil si aún no hay nada (base local sin cargar): se usa rclone lsf
	}
}
