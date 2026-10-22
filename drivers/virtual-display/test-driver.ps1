param(
    [Parameter(Mandatory=$true)][string]$PackageDirectory,
    [switch]$Remove
)
$ErrorActionPreference = 'Stop'
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'Administrator access is required.' }
$package = (Resolve-Path -LiteralPath $PackageDirectory).Path
$stateFile = Join-Path $package 'test-install-state.json'
function Save-State($State) { $State | ConvertTo-Json -Depth 5 | Set-Content -Encoding UTF8 $stateFile }
function Find-Package {
    @(Get-WindowsDriver -Online -All | Where-Object {
        $_.ProviderName -eq 'Deskhand' -and (Split-Path $_.OriginalFileName -Leaf) -eq 'DeskhandVirtualDisplay.inf'
    })
}
function Undo-Installation($State) {
    $drivers = @(Find-Package | Where-Object {
        (Get-FileHash -Algorithm SHA256 -LiteralPath $_.OriginalFileName).Hash -eq $State.inf_sha256
    })
    if ($drivers.Count -gt 1) { throw 'Multiple matching driver packages; refusing an ambiguous removal.' }
    if ($drivers.Count -eq 1) {
        if ($State.published_name -and $drivers[0].Driver -ne $State.published_name) { throw 'Published driver identity changed.' }
        $instance = 'SWD\Deskhand\DeskhandVirtualDisplay'
        $node = Get-PnpDevice -InstanceId $instance -ErrorAction SilentlyContinue
        if ($node) {
            if (Get-PnpDevice -PresentOnly -InstanceId $instance -ErrorAction SilentlyContinue) {
                throw 'Virtual display is still present. Close the probe before removal.'
            }
            $boundInf = Get-PnpDeviceProperty -InstanceId $instance -KeyName 'DEVPKEY_Device_DriverInfPath' -ErrorAction SilentlyContinue
            if ($boundInf.Data -ne $drivers[0].Driver) { throw 'Device package identity changed; refusing removal.' }
            & pnputil.exe /remove-device $instance
            if ($LASTEXITCODE -ne 0) { throw 'Could not remove the inactive experimental device record.' }
        }
        # No /force or /uninstall: a live display must be closed before removal.
        & pnputil.exe /delete-driver $drivers[0].Driver
        if ($LASTEXITCODE -ne 0) { throw 'Driver is still in use or removal failed. Close the probe and retry -Remove.' }
    }
    foreach ($store in @('Root','TrustedPublisher')) {
        $existed = if ($store -eq 'Root') { $State.had_root } else { $State.had_publisher }
        $path = "Cert:\LocalMachine\$store\$($State.thumbprint)"
        if (-not $existed -and (Test-Path $path)) { Remove-Item $path }
    }
    $State.phase='removed'
    Save-State $State
}
if ($Remove) {
    if (-not (Test-Path -LiteralPath $stateFile)) { throw 'No installation record; refusing to guess what to remove.' }
    $state = Get-Content -Raw -LiteralPath $stateFile | ConvertFrom-Json
    Undo-Installation $state
    Write-Output 'Experimental driver and newly added machine trust removed.'
    exit 0
}
if (Test-Path -LiteralPath $stateFile) { throw 'An installation record exists. Remove the prior experiment first.' }
$manifest = Get-Content -Raw -LiteralPath (Join-Path $package 'manifest.json') | ConvertFrom-Json
if (-not $manifest.experimental) { throw 'Expected an experimental package manifest.' }
if ($manifest.verification -ne 'offline-signatures-verified') { throw 'Run verify-package.py on this package before staging it.' }
$expected = @('DeskhandVirtualDisplay.dll','DeskhandVirtualDisplay.inf','DeskhandVirtualDisplay.cat','DeskhandVirtualDisplay.cer')
if (@($manifest.files).Count -ne $expected.Count) { throw 'Unexpected package file list.' }
foreach ($name in $expected) {
    $entry = @($manifest.files | Where-Object { $_.name -eq $name })
    if ($entry.Count -ne 1 -or (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $package $name)).Hash -ne $entry[0].sha256) {
        throw "Package hash mismatch: $name"
    }
}
$certificatePath = Join-Path $package 'DeskhandVirtualDisplay.cer'
$certificate = New-Object Security.Cryptography.X509Certificates.X509Certificate2($certificatePath)
if ($certificate.Thumbprint -ne $manifest.certificate_thumbprint -or $certificate.Subject -notlike 'CN=Deskhand Virtual Display Test *') {
    throw 'Unexpected signing certificate.'
}
if (@(Find-Package).Count -ne 0) { throw 'A Deskhand display package already exists; refusing to replace it.' }
$infEntry = $manifest.files | Where-Object { $_.name -eq 'DeskhandVirtualDisplay.inf' }
$state = [ordered]@{phase='prepared'; thumbprint=$certificate.Thumbprint; inf_sha256=$infEntry.sha256;
    had_root=(Test-Path "Cert:\LocalMachine\Root\$($certificate.Thumbprint)");
    had_publisher=(Test-Path "Cert:\LocalMachine\TrustedPublisher\$($certificate.Thumbprint)"); published_name=$null}
# Record identities before the first machine mutation so interrupted installs
# can be removed with the same command and -Remove.
Save-State $state
try {
    Import-Certificate -FilePath $certificatePath -CertStoreLocation 'Cert:\LocalMachine\Root' | Out-Null
    Import-Certificate -FilePath $certificatePath -CertStoreLocation 'Cert:\LocalMachine\TrustedPublisher' | Out-Null
    foreach ($name in @('DeskhandVirtualDisplay.dll','DeskhandVirtualDisplay.cat')) {
        $signature = Get-AuthenticodeSignature -LiteralPath (Join-Path $package $name)
        if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Thumbprint -ne $certificate.Thumbprint) {
            throw "Signature validation failed: $name"
        }
    }
    & pnputil.exe /add-driver (Join-Path $package 'DeskhandVirtualDisplay.inf')
    if ($LASTEXITCODE -ne 0) { throw 'Driver package staging failed.' }
    $installed = @(Find-Package)
    if ($installed.Count -ne 1) { throw 'Could not identify the staged package.' }
    $state.published_name = $installed[0].Driver
    $state.phase='staged'
    Save-State $state
} catch {
    $failure=$_
    Undo-Installation $state
    throw $failure
}
Write-Output "Staged $($state.published_name). No display device has been created; run the bounded probe separately."
