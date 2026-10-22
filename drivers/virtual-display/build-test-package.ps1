param(
    [Parameter(Mandatory=$true)][string]$DriverDll,
    [Parameter(Mandatory=$true)][string]$DriverInf,
    [Parameter(Mandatory=$true)][string]$Inf2Cat,
    [Parameter(Mandatory=$true)][string]$SignTool,
    [Parameter(Mandatory=$true)][string]$OutputDirectory
)
$ErrorActionPreference = 'Stop'
function Invoke-Checked([string]$Tool, [string[]]$Arguments) {
    & $Tool @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Tool failed with exit code $LASTEXITCODE" }
}
foreach ($file in @($DriverDll,$DriverInf,$Inf2Cat,$SignTool)) {
    if (-not (Test-Path -LiteralPath $file -PathType Leaf)) { throw "Missing input: $file" }
}
if (Test-Path -LiteralPath $OutputDirectory) { throw 'Use a new output directory to preserve prior packages.' }
$out = (New-Item -ItemType Directory -Path $OutputDirectory).FullName
$dll = Join-Path $out 'DeskhandVirtualDisplay.dll'
$inf = Join-Path $out 'DeskhandVirtualDisplay.inf'
$cat = Join-Path $out 'DeskhandVirtualDisplay.cat'
$cer = Join-Path $out 'DeskhandVirtualDisplay.cer'
Copy-Item -LiteralPath $DriverDll -Destination $dll
Copy-Item -LiteralPath $DriverInf -Destination $inf

# The signing key is current-user only. Verification uses a separate offline
# verifier so packaging never needs a trusted-root consent prompt over SSH.
# This script never changes machine trust, boot policy, or installed drivers.
$cert = $null
try {
    $cert = New-SelfSignedCertificate -Type CodeSigningCert -Subject "CN=Deskhand Virtual Display Test $([guid]::NewGuid())" `
        -CertStoreLocation 'Cert:\CurrentUser\My' -HashAlgorithm SHA256 -KeyAlgorithm RSA -KeyLength 3072 `
        -KeyExportPolicy NonExportable -NotAfter (Get-Date).AddDays(7)
    Export-Certificate -Cert $cert -FilePath $cer | Out-Null
    Invoke-Checked $SignTool @('sign','/fd','SHA256','/s','My','/sha1',$cert.Thumbprint,$dll)
    Invoke-Checked $Inf2Cat @("/driver:$out",'/os:10_CO_X64,10_NI_X64,10_GE_X64','/uselocaltime')
    Invoke-Checked $SignTool @('sign','/fd','SHA256','/s','My','/sha1',$cert.Thumbprint,$cat)
    $files = @('DeskhandVirtualDisplay.dll','DeskhandVirtualDisplay.inf','DeskhandVirtualDisplay.cat','DeskhandVirtualDisplay.cer') | ForEach-Object {
        [ordered]@{name=$_; sha256=(Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $out $_)).Hash}
    }
    [ordered]@{experimental=$true; verification='pending'; created=(Get-Date).ToUniversalTime().ToString('o');
        certificate_thumbprint=$cert.Thumbprint; files=@($files)} | ConvertTo-Json -Depth 5 | Set-Content -Encoding UTF8 (Join-Path $out 'manifest.json')
} finally {
    if ($cert) {
        if (Test-Path "Cert:\CurrentUser\My\$($cert.Thumbprint)") {
            Remove-Item "Cert:\CurrentUser\My\$($cert.Thumbprint)" -DeleteKey
        }
    }
}
Write-Output "Signed experimental package (offline verification required): $out"
