# Run in an elevated interactive Windows PowerShell session on the dedicated PC.
# No password is accepted as an argument, read from storage, or printed.
[CmdletBinding()]
param([switch]$OpenAutologon, [switch]$EnableConsoleHandoff, [switch]$DisableConsoleHandoff)

$ErrorActionPreference = 'Stop'
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = New-Object Security.Principal.WindowsPrincipal($identity)
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'Run this script in an elevated PowerShell window on the Windows PC.'
}
if ($EnableConsoleHandoff -and $DisableConsoleHandoff) { throw 'Choose enable or disable console handoff.' }

function Assert-DeskhandNoReparsePoint([string]$Path) {
    $item = [IO.DirectoryInfo]::new([IO.Path]::GetFullPath($Path))
    while ($null -ne $item) {
        if (Test-Path -LiteralPath $item.FullName) {
            if (((Get-Item -LiteralPath $item.FullName -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                throw ('Deskhand setup refuses a reparse point: ' + $item.FullName)
            }
        }
        $item = $item.Parent
    }
}

function Assert-DeskhandTrustedDirectory([string]$Path) {
    Assert-DeskhandNoReparsePoint $Path
    if (-not (Test-Path -LiteralPath $Path -PathType Container)) { throw ('Deskhand folder is missing: ' + $Path) }
    $trusted = @('S-1-5-18', 'S-1-5-32-544')
    $acl = Get-Acl -LiteralPath $Path
    if ($trusted -notcontains $acl.GetOwner([Security.Principal.SecurityIdentifier]).Value) {
        throw ('Deskhand folder owner is not trusted; reinstall the agent: ' + $Path)
    }
    $writeRights = [Security.AccessControl.FileSystemRights]'Write,Delete,ChangePermissions,TakeOwnership,DeleteSubdirectoriesAndFiles'
    foreach ($rule in $acl.Access) {
        if ($rule.AccessControlType -eq 'Allow' -and ($rule.FileSystemRights -band $writeRights) -ne 0 -and
            $trusted -notcontains $rule.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value) {
            throw ('Deskhand folder allows an unelevated writer; reinstall the agent: ' + $Path)
        }
    }
}

function Protect-DeskhandSetupDirectory([string]$Path) {
    Assert-DeskhandNoReparsePoint $Path
    if (Test-Path -LiteralPath $Path) { Assert-DeskhandTrustedDirectory $Path }
    New-Item -ItemType Directory -Force -Path $Path | Out-Null
    Assert-DeskhandNoReparsePoint $Path
    $acl = [Security.AccessControl.DirectorySecurity]::new()
    $acl.SetAccessRuleProtection($true, $false)
    $admins = [Security.Principal.SecurityIdentifier]::new('S-1-5-32-544')
    $acl.SetOwner($admins)
    foreach ($sid in 'S-1-5-18', 'S-1-5-32-544') {
        $rule = [Security.AccessControl.FileSystemAccessRule]::new(
            [Security.Principal.SecurityIdentifier]::new($sid), 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow')
        $acl.AddAccessRule($rule)
    }
    Set-Acl -LiteralPath $Path -AclObject $acl
}

$dataRoot = Join-Path ([Environment]::GetFolderPath('CommonApplicationData')) 'Deskhand'
$setupRoot = Join-Path $dataRoot 'setup'
$handoffName = 'Deskhand Console Handoff'
$handoffScript = Join-Path $setupRoot 'console-handoff.ps1'
$powerShell = Join-Path ([Environment]::SystemDirectory) 'WindowsPowerShell\v1.0\powershell.exe'
$handoffTask = Get-ScheduledTask -TaskPath '\' -TaskName $handoffName -ErrorAction SilentlyContinue
if (($EnableConsoleHandoff -or $DisableConsoleHandoff) -and $handoffTask) {
    $owner = [Security.Principal.NTAccount]::new($handoffTask.Principal.UserId)
    try { $ownerSid = $owner.Translate([Security.Principal.SecurityIdentifier]).Value }
    catch { $ownerSid = $handoffTask.Principal.UserId }
    if ($ownerSid -ne $identity.User.Value -or
        $handoffTask.Description -ne 'Deskhand console handoff (explicit unattended mode)' -or
        $handoffTask.Actions.Count -ne 1 -or
        $handoffTask.Actions[0].Execute -ine $powerShell -or
        -not $handoffTask.Actions[0].Arguments.Contains('"' + $handoffScript + '"')) {
        throw 'An unrelated task uses the handoff task name; it was not changed.'
    }
    if ($handoffTask.State -eq 'Running') { Stop-ScheduledTask -TaskPath '\' -TaskName $handoffName }
}
if ($DisableConsoleHandoff -and $handoffTask) {
    Unregister-ScheduledTask -TaskPath '\' -TaskName $handoffName -Confirm:$false
}
if ($EnableConsoleHandoff) {
    Assert-DeskhandTrustedDirectory $dataRoot
    Protect-DeskhandSetupDirectory $setupRoot
    $source = Join-Path $PSScriptRoot 'console-handoff.ps1'
    Assert-DeskhandNoReparsePoint $source
    Assert-DeskhandNoReparsePoint $handoffScript
    if (-not (Test-Path -LiteralPath $source -PathType Leaf)) { throw 'Put console-handoff.ps1 beside this setup script.' }
    $temporary = Join-Path $setupRoot ('handoff-' + [Guid]::NewGuid().ToString('N') + '.tmp')
    try {
        [IO.File]::WriteAllBytes($temporary, [IO.File]::ReadAllBytes($source))
        if (Test-Path -LiteralPath $handoffScript) { Remove-Item -LiteralPath $handoffScript -Force }
        Move-Item -LiteralPath $temporary -Destination $handoffScript
    } finally {
        if (Test-Path -LiteralPath $temporary) { Remove-Item -LiteralPath $temporary -Force }
    }
    # Recreate the log as well: an older entry could be an inherited hard link.
    $logPath = Join-Path $setupRoot 'console-handoff.log'
    Assert-DeskhandNoReparsePoint $logPath
    if (Test-Path -LiteralPath $logPath) { Remove-Item -LiteralPath $logPath -Force }
    [IO.File]::WriteAllText($logPath, '')
    . $handoffScript
    $xml = New-DeskhandHandoffTaskXml $identity.User.Value $powerShell $handoffScript
    Register-ScheduledTask -TaskPath '\' -TaskName $handoffName -Xml $xml -Force | Out-Null
}

$program = $null
$signature = $null
if ($OpenAutologon -or (-not $EnableConsoleHandoff -and -not $DisableConsoleHandoff)) {
Assert-DeskhandTrustedDirectory $dataRoot
Protect-DeskhandSetupDirectory $setupRoot
$setupDir = Join-Path $setupRoot 'Autologon'
Protect-DeskhandSetupDirectory $setupDir
$architecture = [Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
$image = switch ($architecture) {
    'X64' { 'Autologon64.exe' }
    'Arm64' { 'Autologon64a.exe' }
    default { throw "Unsupported Windows architecture: $architecture" }
}
$program = Join-Path $setupDir $image
# Microsoft's linked archive can be unavailable; Sysinternals Live is the
# official direct-binary distribution and is validated below before execution.
Assert-DeskhandNoReparsePoint $program
$download = Join-Path $setupDir ([Guid]::NewGuid().ToString('N') + '.exe')
try {
    Invoke-WebRequest -UseBasicParsing -Uri ('https://live.sysinternals.com/' + $image) -OutFile $download
    $signature = Get-AuthenticodeSignature -LiteralPath $download
    if ($signature.Status -ne 'Valid' -or
        $signature.SignerCertificate.Subject -notmatch '(^|,\s*)O=Microsoft Corporation(,|$)') {
        throw "Autologon must have a valid Microsoft signature; found $($signature.Status)."
    }
    if (Test-Path -LiteralPath $program) { Remove-Item -LiteralPath $program -Force }
    Move-Item -LiteralPath $download -Destination $program
} finally {
    if (Test-Path -LiteralPath $download) { Remove-Item -LiteralPath $download -Force }
}
}

$winlogon = Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon'
$task = Get-ScheduledTask -TaskPath '\' -TaskName 'Deskhand Agent' -ErrorAction SilentlyContinue
$ssh = Get-Service sshd -ErrorAction SilentlyContinue
[pscustomobject]@{
    AutologonProgram = $program
    Signature = if ($signature) { $signature.Status.ToString() } else { $null }
    SHA256 = if ($program) { (Get-FileHash -LiteralPath $program -Algorithm SHA256).Hash } else { $null }
    Account = $identity.Name
    AutoLogonEnabled = ($winlogon.AutoAdminLogon -eq '1')
    AutoLogonLimitedCount = ($null -ne $winlogon.AutoLogonCount)
    PlaintextPasswordPresent = ($null -ne $winlogon.DefaultPassword)
    AgentTaskPresent = ($null -ne $task)
    AgentTaskLogon = if ($task) { $task.Principal.LogonType.ToString() } else { $null }
    SSHStatus = if ($ssh) { $ssh.Status.ToString() } else { $null }
    ConsoleHandoffEnabled = [bool](Get-ScheduledTask -TaskPath '\' -TaskName $handoffName -ErrorAction SilentlyContinue)
} | ConvertTo-Json

if ($OpenAutologon) {
    if ([Diagnostics.Process]::GetCurrentProcess().SessionId -eq 0) {
        throw 'OpenAutologon needs the interactive user session, not an SSH or service session.'
    }
    # The account password is entered by the person directly into Microsoft's UI.
    # Do not use Autologon's command-line password arguments.
    Start-Process -FilePath $program
}
