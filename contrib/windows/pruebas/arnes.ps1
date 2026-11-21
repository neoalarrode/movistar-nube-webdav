# Prepara una copia del script «Enviar a» (subir.ps1) para probarlo sin Windows:
# destino fijo en lugar de la ventana de carpetas y la pregunta de duplicados
# contestada con -Modo (en «uno» se marca el primer conflicto para reemplazar).
param([Parameter(Mandatory)][string]$Modo, [Parameter(Mandatory)][string]$Instalador, [Parameter(Mandatory)][string]$Destino, [Parameter(Mandatory)][string]$Salida)
$src = Get-Content -LiteralPath $Instalador -Raw
$subir = [regex]::Match($src, '\$subir = @''\r?\n(.*?)\r?\n''@', 'Singleline').Groups[1].Value
if (-not $subir) { throw "No encuentro subir.ps1 dentro del instalador" }
$subir = $subir.Replace("__RCLONE__", (Get-Command rclone).Source).Replace("__REMOTO__", "nubeprueba").Replace("__LETRA__", "N:").Replace("__RCPASS__", "x")
$i = $subir.IndexOf('Add-Type -AssemblyName System.Windows.Forms'); $j = $subir.IndexOf('$base = if ($rel)')
$j = $subir.IndexOf("`n", $j) + 1
$subir = $subir.Substring(0, $i) + ('$rel = "destino"; $dest = "N:\destino"; $base = "${Remoto}:' + $Destino + '"') + "`n" + $subir.Substring($j)
$i = $subir.IndexOf('    $tam = { param($b)'); $j = $subir.IndexOf('    $n = ($reemplazar.Values')
if ($i -lt 0 -or $j -lt 0) { throw "No encuentro la pregunta de duplicados en subir.ps1" }
$auto = @"
    `$modo = '$Modo'
    if (`$modo -eq 'uno') { `$c = `$conflictos[0]; `$reemplazar[`$c.Grupo] = New-Object System.Collections.Generic.List[string]; `$reemplazar[`$c.Grupo].Add(`$c.Rel) }
"@
$subir = $subir.Substring(0, $i) + $auto + "`n" + $subir.Substring($j)
$subir = $subir.Replace('Read-Host "Intro para cerrar"', '').Replace('Start-Sleep -Seconds 4', '')
Set-Content -LiteralPath $Salida -Value $subir
