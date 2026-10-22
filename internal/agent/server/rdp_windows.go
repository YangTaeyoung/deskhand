//go:build windows

package server

import (
	"context"
	"fmt"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// Query only RDP-Tcp's binding: never choose an arbitrary certificate or
// modify Remote Desktop, certificates, credentials, NLA or security policy.
const rdpListenerScript = `
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$key = 'HKLM:\SYSTEM\CurrentControlSet\Control\Terminal Server'
$port = [int](Get-ItemPropertyValue -LiteralPath ($key + '\WinStations\RDP-Tcp') -Name PortNumber)
if ($port -lt 1 -or $port -gt 65535) { throw 'Invalid RDP-Tcp port' }
$settings = @(Get-CimInstance -Namespace root/cimv2/TerminalServices -ClassName Win32_TSGeneralSetting -Filter "TerminalName='RDP-tcp'" -OperationTimeoutSec 3)
if ($settings.Count -ne 1) { throw 'RDP-Tcp listener binding is unavailable' }
$thumb = ([string]$settings[0].SSLCertificateSHA1Hash -replace '\s', '').ToUpperInvariant()
if ($thumb -notmatch '^[0-9A-F]{40}$') { throw 'RDP-Tcp certificate binding is missing or invalid' }
$cert = $null
foreach ($storeName in @('Remote Desktop', 'My')) {
    $store = [Security.Cryptography.X509Certificates.X509Store]::new($storeName, [Security.Cryptography.X509Certificates.StoreLocation]::LocalMachine)
    try {
        $store.Open([Security.Cryptography.X509Certificates.OpenFlags]::ReadOnly -bor [Security.Cryptography.X509Certificates.OpenFlags]::OpenExistingOnly)
        foreach ($candidate in $store.Certificates) {
            if ($candidate.Thumbprint -eq $thumb) { $cert = $candidate; break }
        }
    } finally { $store.Close() }
    if ($null -ne $cert) { break }
}
if ($null -eq $cert) { throw 'Bound RDP-Tcp certificate was not found' }
$sha = [Security.Cryptography.SHA256]::Create()
try { $hash = [BitConverter]::ToString($sha.ComputeHash($cert.RawData)).Replace('-', '') } finally { $sha.Dispose() }
$service = Get-CimInstance -ClassName Win32_Service -Filter "Name='TermService'" -OperationTimeoutSec 3
$listening = @(Get-NetTCPConnection | Where-Object { $_.State -eq 'Listen' -and $_.LocalPort -eq $port -and $_.OwningProcess -eq $service.ProcessId }).Count -gt 0
$denied = [int](Get-ItemPropertyValue -LiteralPath $key -Name fDenyTSConnections)
[ordered]@{enabled=($denied -eq 0 -and $service.State -eq 'Running' -and $listening); port=$port; certificate_sha256=$hash} | ConvertTo-Json -Compress
`

func currentRDPListener(ctx context.Context) (wire.RDPListener, error) {
	cmd, err := recipeCommand(ctx, windowsFolder, rdpListenerScript)
	if err != nil {
		return wire.RDPListener{}, err
	}
	stdout, stderr := &rdpOutput{}, &rdpOutput{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return wire.RDPListener{}, ctx.Err()
		}
		return wire.RDPListener{}, fmt.Errorf("RDP listener PowerShell query failed: %w", err)
	}
	if stdout.overflow || stderr.overflow {
		return wire.RDPListener{}, fmt.Errorf("RDP listener query exceeded 64 KiB")
	}
	return parseRDPListener(stdout.data)
}
