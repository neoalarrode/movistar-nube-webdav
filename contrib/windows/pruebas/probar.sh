#!/bin/bash
# Pruebas del instalador de Windows sin Windows (PowerShell 7 y rclone):
# sintaxis del instalador y de los scripts que genera, y «Enviar a» con y sin
# duplicados en sus tres modos. Sale con error si algo no da lo esperado.
set -euo pipefail
AQUI=$(cd "$(dirname "$0")" && pwd)
INST="$AQUI/../instalar-nube.ps1"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
export RCLONE_CONFIG_NUBEPRUEBA_TYPE=local
fallos=0

pwsh -NoProfile -File "$AQUI/sintaxis.ps1" -Instalador "$INST" || fallos=$((fallos+1))

prepara() {
  rm -rf "$T/origen" "$T/nube"; mkdir -p "$T/origen/Serie/T1" "$T/nube/destino/Serie/T1"
  echo nuevo > "$T/origen/Serie/T1/cap01.mkv"; echo cambiado-2 > "$T/origen/Serie/T1/cap02.mkv"; echo igual > "$T/origen/Serie/T1/cap03.mkv"
  echo suelto-nuevo > "$T/origen/suelto.txt"
  if [ "$1" = con-duplicados ]; then
    echo viejo-2 > "$T/nube/destino/Serie/T1/cap02.mkv"; echo igual > "$T/nube/destino/Serie/T1/cap03.mkv"
    touch -r "$T/origen/Serie/T1/cap03.mkv" "$T/nube/destino/Serie/T1/cap03.mkv"
  fi
}
caso() { # modo, escenario, esperado de cap02
  local modo=$1 esc=$2 cap02=$3
  prepara "$esc"
  pwsh -NoProfile -File "$AQUI/arnes.ps1" -Modo "$modo" -Instalador "$INST" -Destino "$T/nube/destino" -Salida "$T/subir.ps1"
  (cd "$T" && pwsh -NoProfile -File "$T/subir.ps1" "$T/origen/Serie" "$T/origen/suelto.txt" >"$T/salida.txt" 2>&1) || true
  local got="$(cat "$T/nube/destino/Serie/T1/cap01.mkv" 2>/dev/null)|$(cat "$T/nube/destino/Serie/T1/cap02.mkv" 2>/dev/null)|$(cat "$T/nube/destino/suelto.txt" 2>/dev/null)"
  local want="nuevo|$cap02|suelto-nuevo"
  if [ "$got" = "$want" ] && ! grep -q "error(es)" "$T/salida.txt"; then echo "$esc / $modo: OK"
  else echo "$esc / $modo: FALLO (esperado $want, obtenido $got)"; tail -20 "$T/salida.txt"; fallos=$((fallos+1)); fi
}
caso omitir     sin-duplicados cambiado-2
caso omitir     con-duplicados viejo-2
caso reemplazar con-duplicados cambiado-2
caso uno        con-duplicados cambiado-2
[ $fallos -eq 0 ] && echo "Todas las pruebas: OK" || { echo "$fallos prueba(s) fallida(s)"; exit 1; }
