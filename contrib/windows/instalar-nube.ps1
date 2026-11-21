# Unidad de red «Nube» en Windows con rclone, contra el WebDAV de movistar-nube-webdav,
# y «Enviar a > Nube (subida directa)» para subir carpetas sin pasar por la caché.
#
# Ejecutar en PowerShell como administrador (hace falta para instalar WinFsp):
#   Set-ExecutionPolicy -Scope Process Bypass; .\instalar-nube.ps1
#   Set-ExecutionPolicy -Scope Process Bypass; .\instalar-nube.ps1 -Url http://SERVIDOR:5050/carpeta
#
# Pide la URL del WebDAV (si no se da con -Url) y el usuario y la contraseña del
# WebDAV (WEBDAV_USER y WEBDAV_PASSWORD del servidor; no son los del panel). La
# contraseña se guarda ofuscada en la configuración de rclone del usuario de Windows.
# Se puede volver a ejecutar para actualizar: conserva lo configurado.

param(
    [string]$Url = "",     # p. ej. http://192.168.1.10:5050/datos (una carpeta) o http://SERVIDOR:5050
    [string]$Letra = "N:"  # letra de la unidad
)

$ErrorActionPreference = "Stop"
$Remoto = "nube"
$cache  = "$env:LOCALAPPDATA\rclone-nube"

Write-Host "1/5 Instalando rclone y WinFsp..." -ForegroundColor Cyan
winget install --id Rclone.Rclone -e --accept-source-agreements --accept-package-agreements --silent | Out-Null
winget install --id WinFsp.WinFsp -e --accept-source-agreements --accept-package-agreements --silent | Out-Null
$env:Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [Environment]::GetEnvironmentVariable("Path", "User")
$rclone = (Get-Command rclone -ErrorAction SilentlyContinue).Source
if (-not $rclone) { $rclone = (Get-ChildItem "$env:LOCALAPPDATA\Microsoft\WinGet\Packages" -Recurse -Filter rclone.exe | Select-Object -First 1).FullName }
if (-not $rclone) { throw "No encuentro rclone.exe tras instalarlo" }

Write-Host "2/5 Datos del WebDAV" -ForegroundColor Cyan
$remotos = & $rclone listremotes
# Instalaciones anteriores usaban el remoto «nube-pi»: se pasa a «nube» con sus datos.
if (($remotos -contains "nube-pi:") -and -not ($remotos -contains "${Remoto}:")) {
    $viejo = (& $rclone config dump | ConvertFrom-Json)."nube-pi"
    & $rclone config create $Remoto webdav url=$($viejo.url) vendor=owncloud user=$($viejo.user) pass=$($viejo.pass) --no-obscure | Out-Null
    & $rclone config delete "nube-pi" | Out-Null
    $remotos = & $rclone listremotes
}
$existe = $remotos -contains "${Remoto}:"
if (-not $Url -and $existe) { $Url = (& $rclone config dump | ConvertFrom-Json).$Remoto.url }
while (-not $Url) { $Url = (Read-Host "URL del WebDAV (p. ej. http://192.168.1.10:5050/datos)").Trim() }
Write-Host "   $Url"
if ($existe -and (Read-Host "Ya está configurado. ¿Cambiar usuario y contraseña? (s/N)") -notmatch '^[sS]') {
    & $rclone config update $Remoto url=$Url | Out-Null
} else {
    $user = Read-Host "Usuario (WEBDAV_USER)"
    $sec  = Read-Host "Contraseña (WEBDAV_PASSWORD)" -AsSecureString
    $pass = [Runtime.InteropServices.Marshal]::PtrToStringAuto([Runtime.InteropServices.Marshal]::SecureStringToBSTR($sec))
    & $rclone config create $Remoto webdav url=$Url vendor=owncloud user=$user pass=$pass --obscure | Out-Null
    $pass = $null
}
& $rclone lsd "${Remoto}:" | Select-Object -First 3
if ($LASTEXITCODE -ne 0) { throw "No se pudo entrar en el WebDAV: revisa la URL, el usuario, la contraseña y que el servidor esté encendido" }

Write-Host "3/5 Lanzador de la unidad $Letra al iniciar sesión" -ForegroundColor Cyan
# El control remoto de rclone (solo local y con clave) sirve para que la unidad vea al
# momento lo subido por «Enviar a», sin esperar a que caduque la lista de carpetas.
$rcPass = -join ((48..57) + (97..122) | Get-Random -Count 24 | ForEach-Object { [char]$_ })
$rc = "--rc --rc-addr 127.0.0.1:5572 --rc-user nube --rc-pass $rcPass"
$cmd = "@echo off`r`n`"$rclone`" mount ${Remoto}: $Letra --vfs-cache-mode writes --cache-dir `"$cache`" --vfs-cache-max-age 1h --vfs-write-back 5s --dir-cache-time 10m $rc --volname Nube --no-console --log-file `"$cache\mount.log`" --log-level NOTICE`r`n"
New-Item -ItemType Directory -Force -Path $cache | Out-Null
$lanzador = "$env:APPDATA\Microsoft\Windows\Start Menu\Programs\Startup\Nube.cmd"
Set-Content -Path $lanzador -Value $cmd -Encoding ASCII

Write-Host "4/5 Enviar a > Nube (subida directa)" -ForegroundColor Cyan
# Sube leyendo el fichero original a la velocidad de la subida: sin copia en la caché y
# con el tamaño por delante, así el servidor tampoco guarda trozos en memoria.
$subir = @'
param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Rutas)
$rclone = "__RCLONE__"; $Remoto = "__REMOTO__"; $Letra = "__LETRA__"; $RcPass = "__RCPASS__"
$Host.UI.RawUI.WindowTitle = "Nube: subida directa"
if (-not $Rutas) { Write-Host "Selecciona ficheros o carpetas y usa Enviar a > Nube (subida directa)."; Read-Host "Intro para cerrar"; exit 1 }

Add-Type -AssemblyName System.Windows.Forms
$dlg = New-Object System.Windows.Forms.FolderBrowserDialog
$dlg.Description = "Carpeta de destino en la Nube ($Letra)"
$dlg.SelectedPath = "$Letra\"
$owner = New-Object System.Windows.Forms.Form -Property @{ TopMost = $true }
if ($dlg.ShowDialog($owner) -ne [System.Windows.Forms.DialogResult]::OK) { exit 0 }
$dest = $dlg.SelectedPath.TrimEnd('\')
if ($dest -ne $Letra -and -not $dest.StartsWith("$Letra\", [StringComparison]::OrdinalIgnoreCase)) {
    Write-Host "El destino tiene que estar dentro de la unidad $Letra (Nube)." -ForegroundColor Red
    Read-Host "Intro para cerrar"; exit 1
}
$rel = $dest.Substring($Letra.Length).TrimStart('\').Replace('\', '/')
$base = if ($rel) { "${Remoto}:$rel" } else { "${Remoto}:" }

# Cola: cada envío espera su turno (por orden de llegada) para no sumar subidas en
# paralelo contra el servidor. Un turno cuyo proceso ya no existe se descarta.
$cola = Join-Path (Split-Path $PSCommandPath) "cola"
New-Item -ItemType Directory -Force -Path $cola | Out-Null
$turno = Join-Path $cola ("{0:D20}-{1}.txt" -f [DateTime]::UtcNow.Ticks, $PID)
Set-Content -Path $turno -Value $dest
try {
while ($true) {
    $delante = @(Get-ChildItem $cola -Filter *.txt | Sort-Object Name | Where-Object {
        $p = [int]($_.BaseName -split '-')[1]
        # Un PID reutilizado por otro programa no debe bloquear la cola: solo cuenta si es otro envío (PowerShell).
        if ($p -eq $PID -or (Get-Process -Id $p -ErrorAction SilentlyContinue).ProcessName -eq 'powershell') { $true } else { Remove-Item $_.FullName -ErrorAction SilentlyContinue; $false }
    })
    $pos = [array]::IndexOf(@($delante | ForEach-Object { $_.FullName }), $turno)
    if ($pos -le 0) { break }
    Write-Host ("`rEn cola: {0} envío(s) delante; empieza en cuanto terminen.   " -f $pos) -NoNewline -ForegroundColor Yellow
    Start-Sleep -Seconds 2
}

# Estado para el panel del servidor: total copiado, pendiente y tiempo restante. Se le
# pregunta al rclone de la copia (control remoto solo local) y se manda al servidor
# (POST /.nube-estado) con las credenciales del WebDAV. Si algo falla, la copia sigue igual.
$rcCopia = @("--rc", "--rc-addr", "127.0.0.1:5574", "--rc-user", "nube", "--rc-pass", $RcPass,
    # Paciencia ante cortes de red: cada archivo se vuelve a mandar entero hasta 20 veces
    # y la copia completa se repite hasta 10 veces.
    "--low-level-retries", "20", "--retries", "10", "--retries-sleep", "30s")
$destFile = Join-Path $cola ("{0}.destino" -f $PID) # no .txt: esos son los turnos de la cola
$informe = $null
try {
    $cfg = (& $rclone config dump | ConvertFrom-Json).$Remoto
    $estadoUrl = ([Uri]$cfg.url).GetLeftPart([UriPartial]::Authority) + "/.nube-estado"
    $davPass = (& $rclone reveal $cfg.pass)
    $auth = "Basic " + [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes("$($cfg.user):$davPass"))
    $idCopia = "$env:COMPUTERNAME-$PID"
    $informe = Start-Job -ArgumentList $RcPass, $estadoUrl, $auth, $idCopia, $destFile -ScriptBlock {
        param($rcPass, $url, $auth, $id, $destFile)
        $rcAuth = "Basic " + [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes("nube:$rcPass"))
        while ($true) {
            Start-Sleep -Seconds 3
            try {
                $st = Invoke-RestMethod -Method Post -Uri "http://127.0.0.1:5574/core/stats" -Headers @{ Authorization = $rcAuth } -TimeoutSec 5
                $dest = if (Test-Path -LiteralPath $destFile) { (Get-Content -LiteralPath $destFile -Raw -Encoding UTF8).Trim() } else { "" }
                $json = @{ id = $id; dest = $dest; bytes = [int64]$st.bytes; total_bytes = [int64]$st.totalBytes; speed = [double]$st.speed; eta = $st.eta; errors = [int]$st.errors } | ConvertTo-Json -Compress
                Invoke-RestMethod -Method Post -Uri $url -Headers @{ Authorization = $auth } -ContentType "application/json" -Body ([Text.Encoding]::UTF8.GetBytes($json)) -TimeoutSec 5 | Out-Null
            } catch { }
        }
    }
} catch { }

$fallos = 0
$items = $Rutas | ForEach-Object { Get-Item -LiteralPath $_ }

# Archivos que ya existen en la nube: se pregunta una vez qué hacer con todos.
# Cada conflicto: Rel (ruta relativa a la carpeta enviada o al destino), y tamaño y
# fecha de los dos lados.
function Get-Remotos([string]$ruta, [bool]$recursivo) {
    $args = @("lsjson", $ruta, "--files-only", "--no-mimetype")
    if ($recursivo) { $args += "-R" }
    $j = & $rclone @args 2>$null
    if ($LASTEXITCODE -ne 0 -or -not $j) { return @{} }
    $m = @{}
    foreach ($f in ($j | ConvertFrom-Json)) { $m[$f.Path.ToLowerInvariant()] = $f }
    return $m
}
$conflictos = New-Object System.Collections.Generic.List[object]
foreach ($d in $items | Where-Object { $_.PSIsContainer }) {
    $remotos = Get-Remotos "$base/$($d.Name)" $true
    if ($remotos.Count -eq 0) { continue }
    foreach ($f in Get-ChildItem -LiteralPath $d.FullName -Recurse -File -Force) {
        if ($f.Name -in @("Thumbs.db", "desktop.ini")) { continue }
        $rel = $f.FullName.Substring($d.FullName.Length).TrimStart('\').Replace('\', '/')
        $r = $remotos[$rel.ToLowerInvariant()]
        if ($r) { $conflictos.Add([pscustomobject]@{ Grupo = $d.FullName; Rel = $rel; Local = $f; Nube = $r }) }
    }
}
foreach ($g in $items | Where-Object { -not $_.PSIsContainer } | Group-Object DirectoryName) {
    $remotos = Get-Remotos $base $false
    foreach ($f in $g.Group) {
        $r = $remotos[$f.Name.ToLowerInvariant()]
        if ($r) { $conflictos.Add([pscustomobject]@{ Grupo = $g.Name; Rel = $f.Name; Local = $f; Nube = $r }) }
    }
}

$modo = "nuevos"   # sin conflictos: rclone salta lo que ya está igual
$reemplazar = @{}  # modo «uno»: Grupo -> lista de rutas relativas a reemplazar
if ($conflictos.Count -gt 0) {
    $tam = { param($b) if ($b -ge 1GB) { "{0:N1} GB" -f ($b / 1GB) } else { "{0:N1} MB" -f ($b / 1MB) } }
    $f = New-Object System.Windows.Forms.Form -Property @{ Text = "Nube: archivos que ya existen"; Width = 460; Height = 190; StartPosition = "CenterScreen"; TopMost = $true; FormBorderStyle = "FixedDialog"; MaximizeBox = $false; MinimizeBox = $false }
    $l = New-Object System.Windows.Forms.Label -Property @{ Left = 15; Top = 15; Width = 420; Height = 50; Text = "$($conflictos.Count) archivo(s) ya existen en la nube con el mismo nombre. ¿Qué hago con ellos?" }
    $f.Controls.Add($l)
    $opciones = @(@("Omitir todos", "omitir"), @("Reemplazar todos", "reemplazar"), @("Decidir uno a uno", "uno"))
    $x = 15
    foreach ($o in $opciones) {
        $b = New-Object System.Windows.Forms.Button -Property @{ Left = $x; Top = 80; Width = 130; Height = 32; Text = $o[0]; Tag = $o[1] }
        $b.Add_Click({ $f.Tag = $this.Tag; $f.Close() })
        $f.Controls.Add($b); $x += 140
    }
    [void]$f.ShowDialog()
    if (-not $f.Tag) { Write-Host "Cancelado: no se ha subido nada."; exit 0 }
    $modo = $f.Tag
    if ($modo -eq "uno") {
        $u = New-Object System.Windows.Forms.Form -Property @{ Text = "Nube: marca los que quieres reemplazar"; Width = 900; Height = 520; StartPosition = "CenterScreen"; TopMost = $true }
        $lista = New-Object System.Windows.Forms.CheckedListBox -Property @{ Left = 10; Top = 10; Width = 865; Height = 410; CheckOnClick = $true; HorizontalScrollbar = $true; Anchor = "Top,Bottom,Left,Right" }
        foreach ($c in $conflictos) {
            $fn = if ($c.Nube.ModTime) { ([DateTime]$c.Nube.ModTime).ToString("dd/MM/yyyy HH:mm") } else { "?" }
            $txt = "{0}   (aquí: {1}, {2} · en la nube: {3}, {4})" -f $c.Rel, (& $tam $c.Local.Length), $c.Local.LastWriteTime.ToString("dd/MM/yyyy HH:mm"), (& $tam ([int64]$c.Nube.Size)), $fn
            [void]$lista.Items.Add($txt)
        }
        $ok = New-Object System.Windows.Forms.Button -Property @{ Left = 760; Top = 430; Width = 115; Height = 32; Text = "Aceptar"; Anchor = "Bottom,Right" }
        $ok.Add_Click({ $u.Tag = "ok"; $u.Close() })
        $todos = New-Object System.Windows.Forms.Button -Property @{ Left = 10; Top = 430; Width = 140; Height = 32; Text = "Marcar todos"; Anchor = "Bottom,Left" }
        $todos.Add_Click({ for ($i = 0; $i -lt $lista.Items.Count; $i++) { $lista.SetItemChecked($i, $true) } })
        $u.Controls.AddRange(@($lista, $ok, $todos))
        [void]$u.ShowDialog()
        if ($u.Tag -ne "ok") { Write-Host "Cancelado: no se ha subido nada."; exit 0 }
        for ($i = 0; $i -lt $conflictos.Count; $i++) {
            if ($lista.GetItemChecked($i)) {
                $c = $conflictos[$i]
                if (-not $reemplazar.ContainsKey($c.Grupo)) { $reemplazar[$c.Grupo] = New-Object System.Collections.Generic.List[string] }
                $reemplazar[$c.Grupo].Add($c.Rel)
            }
        }
    }
    $n = ($reemplazar.Values | ForEach-Object { $_.Count } | Measure-Object -Sum).Sum
    $resumen = switch ($modo) { "omitir" { "se omiten los $($conflictos.Count)" } "reemplazar" { "se reemplazan los $($conflictos.Count)" } "uno" { "se reemplazan $n y se omiten $($conflictos.Count - $n)" } }
    Write-Host "Archivos que ya existían: $resumen." -ForegroundColor Yellow
}
# Opciones de rclone según la decisión. En «uno», la copia principal omite todo lo
# existente y luego se reenvían solo los marcados.
$dup = switch ($modo) { "omitir" { @("--ignore-existing") } "uno" { @("--ignore-existing") } "reemplazar" { @("--ignore-times") } default { @() } }
function Reenviar-Marcados([string]$origen, [string]$destino) {
    if (-not $reemplazar.ContainsKey($origen)) { return }
    $l = New-TemporaryFile
    [IO.File]::WriteAllLines($l.FullName, [string[]]$reemplazar[$origen])
    & $rclone copy $origen $destino --files-from-raw $l --no-traverse --ignore-times --transfers 4 --progress --stats 2s @rcCopia
    if ($LASTEXITCODE -ne 0) { $script:fallos++ }
    Remove-Item $l
}
# Carpetas: cada una con su nombre dentro del destino.
foreach ($d in $items | Where-Object { $_.PSIsContainer }) {
    Write-Host "`n$($d.Name)\  ->  $dest\$($d.Name)" -ForegroundColor Cyan
    Set-Content -LiteralPath $destFile -Value ((($rel, $d.Name) | Where-Object { $_ }) -join '/') -Encoding UTF8
    & $rclone copy $d.FullName "$base/$($d.Name)" --create-empty-src-dirs --exclude Thumbs.db --exclude desktop.ini --transfers 4 --progress --stats 2s @dup @rcCopia
    if ($LASTEXITCODE -ne 0) { $fallos++ }
    Reenviar-Marcados $d.FullName "$base/$($d.Name)"
}
# Ficheros sueltos: juntos por carpeta de origen, varios a la vez.
foreach ($g in $items | Where-Object { -not $_.PSIsContainer } | Group-Object DirectoryName) {
    Write-Host "`n$($g.Count) fichero(s) de $($g.Name)  ->  $dest" -ForegroundColor Cyan
    Set-Content -LiteralPath $destFile -Value $(if ($rel) { $rel } else { "/" }) -Encoding UTF8
    $lista = New-TemporaryFile
    [IO.File]::WriteAllLines($lista.FullName, [string[]]($g.Group | ForEach-Object { $_.Name }))
    & $rclone copy $g.Name $base --files-from-raw $lista --no-traverse --transfers 4 --progress --stats 2s @dup @rcCopia
    if ($LASTEXITCODE -ne 0) { $fallos++ }
    Remove-Item $lista
    Reenviar-Marcados $g.Name $base
}

# Que la unidad vea ya lo subido.
$olvidar = if ($rel) { @("dir=$rel") } else { @() }
& $rclone rc vfs/forget @olvidar --url http://127.0.0.1:5572 --user nube --pass $RcPass 2>$null | Out-Null
} finally {
    Remove-Item $turno, $destFile -ErrorAction SilentlyContinue
    if ($informe) {
        Stop-Job $informe -ErrorAction SilentlyContinue; Remove-Job $informe -Force -ErrorAction SilentlyContinue
        try { Invoke-RestMethod -Method Post -Uri $estadoUrl -Headers @{ Authorization = $auth } -ContentType "application/json" -Body ([Text.Encoding]::UTF8.GetBytes((@{ id = $idCopia; done = $true } | ConvertTo-Json -Compress))) -TimeoutSec 5 | Out-Null } catch { }
    }
}

if ($fallos) {
    Write-Host "`nHa habido $fallos error(es); lo que no se subió se puede volver a enviar (lo ya subido se salta)." -ForegroundColor Red
    Read-Host "Intro para cerrar"
} else {
    Write-Host "`nListo: todo subido a $dest" -ForegroundColor Green
    Start-Sleep -Seconds 4
}
'@
$subir = $subir.Replace("__RCLONE__", $rclone).Replace("__REMOTO__", $Remoto).Replace("__LETRA__", $Letra).Replace("__RCPASS__", $rcPass)
$script = "$cache\subir.ps1"
Set-Content -Path $script -Value $subir -Encoding UTF8
$acceso = (New-Object -ComObject WScript.Shell).CreateShortcut("$env:APPDATA\Microsoft\Windows\SendTo\Nube (subida directa).lnk")
$acceso.TargetPath = "$env:SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe"
$acceso.Arguments = "-NoProfile -ExecutionPolicy Bypass -File `"$script`""
$acceso.Save()

Write-Host "5/5 Montando $Letra" -ForegroundColor Cyan
# Si ya estaba montada, se desmonta para arrancarla con la configuración nueva. Lo que
# quedara pendiente en la caché se sube al volver a montar.
Get-CimInstance Win32_Process -Filter "Name = 'rclone.exe'" |
    Where-Object { $_.CommandLine -like "* mount ${Remoto}:*" } |
    ForEach-Object { Stop-Process -Id $_.ProcessId -Force; Start-Sleep -Seconds 2 }
# A través del Explorador, para que el montaje corra como el usuario normal y no como
# administrador: una unidad montada desde una ventana de administrador no aparece en
# el Explorador (Windows separa las dos sesiones).
Start-Process -FilePath "$env:SystemRoot\explorer.exe" -ArgumentList "`"$lanzador`""
# Desde esta ventana (administrador) la unidad no se ve: se pregunta al rclone del
# montaje por su control remoto.
$rcAuth = "Basic " + [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes("nube:$rcPass"))
$montada = $false
for ($i = 0; $i -lt 15 -and -not $montada; $i++) {
    Start-Sleep -Seconds 2
    try { Invoke-RestMethod -Method Post -Uri "http://127.0.0.1:5572/mount/listmounts" -Headers @{ Authorization = $rcAuth } -TimeoutSec 3 | Out-Null; $montada = $true } catch { }
}
if ($montada) {
    Write-Host "Listo: la unidad $Letra (Nube) está montada y se montará sola al iniciar sesión. Para subir sin caché: clic derecho > Enviar a > Nube (subida directa)." -ForegroundColor Green
} else {
    Write-Host "No he podido comprobar el montaje. Mira $cache\mount.log o reinicia la sesión de Windows." -ForegroundColor Yellow
}
else { Write-Host "La unidad no aparece aún; mira $cache\mount.log" -ForegroundColor Yellow }
