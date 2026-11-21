# Comprueba la sintaxis del instalador y de los scripts que genera (vigilante.ps1 y subir.ps1).
param([Parameter(Mandatory)][string]$Instalador)
$src = Get-Content -LiteralPath $Instalador -Raw
function Comprueba([string]$nombre, [string]$codigo) {
    $t = $null; $e = $null
    [System.Management.Automation.Language.Parser]::ParseInput($codigo, [ref]$t, [ref]$e) | Out-Null
    if ($e) { $e | ForEach-Object { "{0} línea {1}: {2}" -f $nombre, $_.Extent.StartLineNumber, $_.Message }; exit 1 }
}
Comprueba "instalador" $src
foreach ($v in @('vigilante', 'subir')) {
    $m = [regex]::Match($src, '\$' + $v + ' = @''\r?\n(.*?)\r?\n''@', 'Singleline')
    if (-not $m.Success) { "falta $v.ps1 dentro del instalador"; exit 1 }
    Comprueba "$v.ps1" $m.Groups[1].Value
}
# Un «else» o «elseif» al principio de una línea no es parte de un if: PowerShell lo
# toma como un comando que no existe y falla al ejecutarse (pasó en el instalador).
$sueltos = [regex]::Matches($src, '(?m)^\s*else(if)?\b')
if ($sueltos.Count) { "«else» al principio de una línea (fallaría al ejecutarse): $($sueltos.Count)"; exit 1 }
"sintaxis: OK"
