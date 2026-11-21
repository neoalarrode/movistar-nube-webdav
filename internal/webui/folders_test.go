package webui

import (
	"os"

	"movistar-webdav-proxy/internal/webdav"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildFolders(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("progress.log", "2026-11-13 08:00:00 INICIO Peliculas\n2026-11-13 10:54:41 FIN Peliculas rc=1\n2026-11-13 10:54:41 INICIO Viajes\n")
	// Incluye una línea sin ruta y un duplicado: no deben contar.
	write("origen-danado.txt", "Peliculas\tCigüeñas.mkv\t1\tx\nPeliculas\t\t0\ty\nPeliculas\tCigüeñas.mkv\t1\tz\n")
	write("plan.txt", "Peliculas\nViajes\nVídeos\nTrabajo\nFotos\n")
	// folders.json es el ls del montaje de la nube: Fotos (pendiente) aún no está.
	write("folders.json", `{"folders":[{"name":"Peliculas","in_cloud":true},{"name":"Viajes","in_cloud":true},{"name":"Vídeos","in_cloud":true},{"name":"Trabajo","in_cloud":true},{"name":"DVR","in_cloud":true}]}`)
	write("stats.json", `{"transferring":[{"name":"Serie/T1/e1.mkv","size":10,"bytes":1,"percentage":10}]}`)
	write("mount-stats.json", `{"transferring":[{"name":"Vídeos/Dark/e3.mkv","size":10,"bytes":3,"percentage":30},{"name":"Trabajo/nuevo.mkv","size":10,"bytes":2,"percentage":20}]}`)
	write("vfs-queue.json", `{"queue":[{"name":"Trabajo/nuevo.mkv","uploading":true},{"name":"DVR/grab.ts","uploading":false}]}`)

	// Las subidas salen del propio proxy (la de la copia y la del montaje).
	old := webdav.Transfers
	webdav.Transfers = webdav.NewTransferTracker(time.Minute)
	t.Cleanup(func() { webdav.Transfers = old })
	h1 := webdav.Transfers.Start("upload", "/datos/Viajes/Serie/T1/e1.mkv", 10, 0)
	h2 := webdav.Transfers.Start("upload", "/datos/Trabajo/nuevo.mkv", 10, 0)
	defer h1.Done()
	defer h2.Done()

	st := readCopyStatus(dir, "")
	got := map[string]folderStatus{}
	for _, f := range st.Folders {
		got[f.Name] = f
	}
	check := func(name, activity, copy string) {
		t.Helper()
		f, ok := got[name]
		if !ok {
			t.Fatalf("falta la carpeta %q", name)
		}
		if f.Activity != activity || f.Copy != copy {
			t.Errorf("%s: actividad %q y copia %q; esperado %q y %q", name, f.Activity, f.Copy, activity, copy)
		}
	}
	check("Viajes", "upload", "running")
	check("Vídeos", "download", "pending")
	check("Trabajo", "upload", "pending")
	check("DVR", "cache", "excluded")
	check("Peliculas", "idle", "done_warn")
	if f := got["Peliculas"]; f.Damaged != 1 || len(f.DamagedFiles) != 1 {
		t.Errorf("Peliculas: esperado 1 dañado, hay %d (%q)", f.Damaged, f.DamagedFiles)
	}
	check("Fotos", "idle", "pending")
	if f := got["Fotos"]; f.InCloud {
		t.Error("Fotos aún no está en la nube")
	}
	if _, ok := got["el reproductor Server Databases"]; ok {
		t.Error("una carpeta que solo está en el NAS no debe salir")
	}

	if len(st.Reading) != 1 || st.Reading[0].Name != "Vídeos/Dark/e3.mkv" {
		t.Errorf("la subida del montaje no debe salir como lectura: %+v", st.Reading)
	}
	if len(st.Uploading) != 2 {
		t.Errorf("esperadas 2 subidas (copia + montaje), hay %d", len(st.Uploading))
	}
}

// Una reproducción entre ráfagas sigue visible "en búfer" unos minutos y su
// carpeta sigue contando como bajando; pasado el plazo desaparece.
func TestReadTrackerKeepsBufferedReads(t *testing.T) {
	rt := newReadTracker(5 * time.Minute)
	t0 := time.Now()
	avatar := copyTransfer{Name: "Peliculas/Avatar.mkv", Percentage: 60, Speed: 4e6}
	if got := rt.merge([]copyTransfer{avatar}, t0); len(got) != 1 || got[0].Idle {
		t.Fatalf("lectura activa: %+v", got)
	}
	got := rt.merge(nil, t0.Add(2*time.Minute))
	if len(got) != 1 || !got[0].Idle || got[0].IdleSeconds != 120 || got[0].Speed != 0 {
		t.Fatalf("entre ráfagas debe seguir en búfer: %+v", got)
	}
	if got := rt.merge(nil, t0.Add(6*time.Minute)); len(got) != 0 {
		t.Fatalf("pasado el plazo debe desaparecer: %+v", got)
	}
}
