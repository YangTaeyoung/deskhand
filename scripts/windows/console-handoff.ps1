[CmdletBinding()]
param([string]$ExpectedSid)

function Test-DeskhandHandoffCandidate {
    param($Snapshot, [string]$Sid)
    return $null -ne $Snapshot -and $Snapshot.SessionId -gt 0 -and
        $Snapshot.State -eq 4 -and (@(0, 2) -contains $Snapshot.Protocol) -and $Snapshot.Sid -eq $Sid
}

function New-DeskhandHandoffTaskXml {
    param([string]$Sid, [string]$PowerShell, [string]$Script)
    if ($Sid -notmatch '^S-1-5-21-\d+-\d+-\d+-\d+$') { throw 'Use the dedicated account SID.' }
    $sidXml = [Security.SecurityElement]::Escape($Sid)
    $programXml = [Security.SecurityElement]::Escape($PowerShell)
    if ($Script.Contains('"')) { throw 'The handoff script path cannot contain a quote.' }
    $arguments = '-NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass -File "' + $Script + '" -ExpectedSid ' + $Sid
    $argumentsXml = [Security.SecurityElement]::Escape($arguments)
    return @"
<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><Description>Deskhand console handoff (explicit unattended mode)</Description></RegistrationInfo>
  <Triggers><SessionStateChangeTrigger><Enabled>true</Enabled><StateChange>RemoteDisconnect</StateChange><UserId>$sidXml</UserId></SessionStateChangeTrigger></Triggers>
  <Principals><Principal id="Author"><UserId>$sidXml</UserId><LogonType>InteractiveToken</LogonType><RunLevel>HighestAvailable</RunLevel></Principal></Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate><StartWhenAvailable>false</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable><AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled><Hidden>true</Hidden><RunOnlyIfIdle>false</RunOnlyIfIdle><WakeToRun>false</WakeToRun>
    <ExecutionTimeLimit>PT30S</ExecutionTimeLimit><Priority>7</Priority>
  </Settings>
  <Actions Context="Author"><Exec><Command>$programXml</Command><Arguments>$argumentsXml</Arguments></Exec></Actions>
</Task>
"@
}

function Invoke-DeskhandConsoleHandoff {
    param([string]$Sid, [scriptblock]$ReadSnapshot, [scriptblock]$Connect, [scriptblock]$Wait,
        [scriptblock]$Trace = {})
    $before = & $ReadSnapshot
    & $Trace 'before' $before
    if (-not (Test-DeskhandHandoffCandidate $before $Sid)) { return 'skipped' }
    & $Wait
    $after = & $ReadSnapshot
    if (-not (Test-DeskhandHandoffCandidate $after $Sid) -or $after.SessionId -ne $before.SessionId) {
        & $Trace 'after' $after
        return 'skipped'
    }
    & $Connect $after.SessionId
    & $Trace 'after' $after
    return 'handed_off'
}

if ($MyInvocation.InvocationName -eq '.') { return }
$ErrorActionPreference = 'Stop'
if ($ExpectedSid -notmatch '^S-1-5-21-\d+-\d+-\d+-\d+$' -or
    [Security.Principal.WindowsIdentity]::GetCurrent().User.Value -ne $ExpectedSid) {
    throw 'The handoff must run as its explicitly configured dedicated account.'
}

Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
namespace Deskhand {
    public static class ConsoleHandoffNative {
        [DllImport("wtsapi32.dll", EntryPoint="WTSQuerySessionInformationW", SetLastError=true)]
        static extern bool Query(IntPtr server, int session, int kind, out IntPtr data, out int size);
        [DllImport("wtsapi32.dll")] static extern void WTSFreeMemory(IntPtr data);
        public static byte[] Read(int session, int kind) {
            IntPtr data; int size;
            if (!Query(IntPtr.Zero, session, kind, out data, out size)) throw new Win32Exception();
            try { var result = new byte[size]; Marshal.Copy(data, result, 0, size); return result; }
            finally { WTSFreeMemory(data); }
        }
    }
}
'@

function Get-DeskhandSessionSnapshot {
    $id = [Diagnostics.Process]::GetCurrentProcess().SessionId
    if ($id -le 0) { return $null }
    $user = [Text.Encoding]::Unicode.GetString([Deskhand.ConsoleHandoffNative]::Read($id, 5)).TrimEnd([char]0)
    $domain = [Text.Encoding]::Unicode.GetString([Deskhand.ConsoleHandoffNative]::Read($id, 7)).TrimEnd([char]0)
    if ([string]::IsNullOrEmpty($user)) { return $null }
    $name = if ($domain) { $domain + '\' + $user } else { $user }
    $accountSid = ([Security.Principal.NTAccount]::new($name)).Translate([Security.Principal.SecurityIdentifier]).Value
    [pscustomobject]@{
        SessionId = $id
        State = [BitConverter]::ToInt32([Deskhand.ConsoleHandoffNative]::Read($id, 8), 0)
        Protocol = [BitConverter]::ToUInt16([Deskhand.ConsoleHandoffNative]::Read($id, 16), 0)
        Sid = $accountSid
    }
}

$logPath = Join-Path $PSScriptRoot 'console-handoff.log'
try {
    $result = Invoke-DeskhandConsoleHandoff $ExpectedSid { Get-DeskhandSessionSnapshot } {
        param($id)
        # Only the currently disconnected dedicated session is eligible; no password is passed.
        & (Join-Path ([Environment]::SystemDirectory) 'tscon.exe') $id '/dest:console' | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "tscon failed with exit code $LASTEXITCODE" }
    } { Start-Sleep -Milliseconds 2000 } {
        param($phase, $snapshot)
        $details = if ($null -eq $snapshot) { 'null' } else { $snapshot | ConvertTo-Json -Compress }
        Add-Content -LiteralPath $logPath -Value ((Get-Date).ToString('o') + ' ' + $phase + ' ' + $details)
    }
    Add-Content -LiteralPath $logPath -Value ((Get-Date).ToString('o') + ' ' + $result)
} catch {
    Add-Content -LiteralPath $logPath -Value ((Get-Date).ToString('o') + ' failed: ' + $_.Exception.Message)
    throw
}
