package webui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadCopyStatus(t *testing.T) {
	dir := t.TempDir()
	ctl := filepath.Join(dir, "control")
	_ = os.Mkdir(ctl, 0o755)
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("plan.txt", "Peliculas\nAMC+\nVídeos\n")
	write("progress.log", "2026-11-12 22:43:38 INICIO Peliculas\n2026-11-13 08:10:00 FIN Peliculas rc=0\n2026-11-13 08:10:01 INICIO AMC+\n")
	write("AMC+.log", "2026/11/13 08:20:00 INFO  :    42.311 GiB / 168.200 GiB, 25%, 31.020 MiB/s, ETA 1h9m\n2026/11/13 08:21:00 ERROR : a.mkv: Failed to copy: 503\n")
	// El límite de subida ya no es del script de copia: es el común del proxy.
	uploadBWLimit.Store("20M")
	t.Cleanup(func() { uploadBWLimit.Store("off") })

	st := readCopyStatus(dir, ctl)
	if !st.Enabled || st.Current != "AMC+" {
		t.Fatalf("esperada copia de AMC+ en curso: %+v", st)
	}
	want := map[string]string{"Peliculas": "done", "AMC+": "running", "Vídeos": "pending"}
	for _, l := range st.Libraries {
		if want[l.Name] != l.State {
			t.Errorf("%s: estado %q, esperado %q", l.Name, l.State, want[l.Name])
		}
	}
	// La velocidad es la del propio proxy (aquí, sin subidas en curso).
	if st.Percent != "25" || st.Speed != "0.000 B/s" || st.ETA != "1h9m" || st.Total != "168.200 GiB" {
		t.Errorf("estadísticas mal leídas: %+v", st)
	}
	if st.ErrorCount != 1 || st.BWLimit != "20M" {
		t.Errorf("errores %d (esperado 1), límite %q (esperado 20M)", st.ErrorCount, st.BWLimit)
	}
}

func TestReadTransfers(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "stats.json")
	body := `{"speed":44040192,"transferring":[{"name":"b.mkv","size":100,"bytes":50,"percentage":50,"speed":10,"speedAvg":8,"eta":5},{"name":"a.mkv","size":10,"bytes":1,"percentage":10,"speed":3,"eta":null}]}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	tr, sp := readTransfers(p, time.Minute)
	if len(tr) != 2 || tr[0].Name != "a.mkv" || tr[1].Speed != 8 || tr[0].ETA != nil || sp != 44040192 {
		t.Fatalf("lectura incorrecta: %+v, velocidad %v", tr, sp)
	}
	old := time.Now().Add(-5 * time.Minute)
	_ = os.Chtimes(p, old, old)
	if tr, _ := readTransfers(p, time.Minute); tr != nil {
		t.Fatalf("un volcado antiguo debe ignorarse, obtenido %+v", tr)
	}
}

// Tras un corte, el log y el volcado de la ejecución anterior no deben mezclarse.
func TestReadCopyStatus_IgnoresPreviousRun(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("progress.log", "2026-11-12 22:43:38 INICIO Peliculas\n2026-11-12 23:23:23 FIN Peliculas rc=143\n2026-11-12 23:23:32 INICIO Peliculas\n")
	write("Peliculas.log", "2026/11/12 23:22:00 INFO  :   25.660 GiB / 1.377 TiB, 2%, 32.000 MiB/s, ETA 9h\n2026/11/12 23:22:30 ERROR : x.mkv: corte\n")
	write("stats.json", `{"speed":1,"transferring":[{"name":"viejo.mkv","size":1,"bytes":1,"percentage":1}]}`)
	before, _ := time.ParseInLocation("2006-01-02 15:04:05", "2026-11-12 23:23:00", time.Local)
	_ = os.Chtimes(filepath.Join(dir, "stats.json"), before, before)

	st := readCopyStatus(dir, "")
	if st.Transferred != "" || st.ErrorCount != 0 || len(st.Uploading) != 0 {
		t.Fatalf("se mezclaron datos de la ejecución anterior: %+v", st)
	}
	if st.Current != "Peliculas" {
		t.Fatalf("esperado Peliculas en curso, obtenido %q", st.Current)
	}
}

// Si el log de rclone no es legible, los totales salen de stats.json.
func TestReadCopyStatus_FallbackToStatsJSON(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "progress.log"), []byte("2026-11-13 10:54:41 INICIO AMC+\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "AMC+.log"), []byte("2026/11/13 12:09:41 INFO  :   1 GiB / 2 GiB, 50%, 1 MiB/s, ETA 1s\n"), 0o000)
	_ = os.WriteFile(filepath.Join(dir, "stats.json"), []byte(`{"bytes":160004636537,"totalBytes":180040000000,"speed":27890000,"eta":720,"transferring":[{"name":"x.mkv","size":10,"bytes":5,"percentage":50}]}`), 0o644)

	st := readCopyStatus(dir, "")
	if st.Percent != "88" || st.ETA != "12m0s" || st.Total == "" || st.Speed == "" {
		t.Fatalf("respaldo desde stats.json incorrecto: %+v", st)
	}
}
