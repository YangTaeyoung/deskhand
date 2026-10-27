$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'console-handoff.ps1')

function Check([bool]$Condition, [string]$Message) {
    if (-not $Condition) { throw $Message }
}
$sid = 'S-1-5-21-1-2-3-1001'
function Snapshot([int]$Id, [int]$State, [int]$Protocol, [string]$UserSid) {
    [pscustomobject]@{ SessionId = $Id; State = $State; Protocol = $Protocol; Sid = $UserSid }
}
$disconnected = Snapshot 7 4 2 $sid
Check (Test-DeskhandHandoffCandidate $disconnected $sid) 'disconnected RDP of the selected account must be eligible'
foreach ($state in 0, 1, 2, 3, 5, 6, 7, 8, 9) {
    Check (-not (Test-DeskhandHandoffCandidate (Snapshot 7 $state 2 $sid) $sid)) "state $state must not disconnect a person"
}
Check (-not (Test-DeskhandHandoffCandidate (Snapshot 0 4 2 $sid) $sid)) 'Session 0 must never be transferred'
Check (Test-DeskhandHandoffCandidate (Snapshot 7 4 0 $sid) $sid) 'remote disconnect may reset the protocol to console before the task runs'
Check (-not (Test-DeskhandHandoffCandidate (Snapshot 7 0 0 $sid) $sid)) 'an active console must never be transferred'
foreach ($protocol in 1, 3, 65535) {
    Check (-not (Test-DeskhandHandoffCandidate (Snapshot 7 4 $protocol $sid) $sid)) 'an unknown protocol must never be transferred'
}
Check (-not (Test-DeskhandHandoffCandidate (Snapshot 7 4 2 'S-1-5-21-9') $sid)) 'another user must never be transferred'
Check (-not (Test-DeskhandHandoffCandidate $null $sid)) 'unknown state must fail closed'

[xml]$xml = New-DeskhandHandoffTaskXml $sid 'C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe' 'C:\ProgramData\Deskhand\setup\console-handoff.ps1'
$ns = New-Object Xml.XmlNamespaceManager($xml.NameTable)
$ns.AddNamespace('t', 'http://schemas.microsoft.com/windows/2004/02/mit/task')
Check ($xml.SelectSingleNode('//t:SessionStateChangeTrigger/t:StateChange', $ns).InnerText -eq 'RemoteDisconnect') 'task must only trigger on remote disconnect'
Check ($xml.SelectSingleNode('//t:SessionStateChangeTrigger/t:UserId', $ns).InnerText -eq $sid) 'trigger must be restricted to selected SID'
Check ($xml.SelectSingleNode('//t:Principal/t:UserId', $ns).InnerText -eq $sid) 'task principal must match selected SID'
Check ($xml.SelectSingleNode('//t:Principal/t:LogonType', $ns).InnerText -eq 'InteractiveToken') 'task must use the existing interactive session'
Check ($xml.SelectSingleNode('//t:Principal/t:RunLevel', $ns).InnerText -eq 'HighestAvailable') 'tscon needs the elevated account token'
Check ($xml.SelectSingleNode('//t:Settings/t:MultipleInstancesPolicy', $ns).InnerText -eq 'IgnoreNew') 'overlapping handoffs must not run'
Check ($xml.SelectSingleNode('//t:Exec/t:Arguments', $ns).InnerText.Contains('-ExpectedSid ' + $sid)) 'action must bind the same SID'

$probe = @{ Reads = 0; Connects = @(); Samples = @($disconnected, $disconnected) }
$read = { $sample = $probe.Samples[$probe.Reads]; $probe.Reads++; $sample }.GetNewClosure()
$connect = { param($id) $probe.Connects += $id }.GetNewClosure()
$wait = {}
$result = Invoke-DeskhandConsoleHandoff $sid $read $connect $wait
Check ($probe.Reads -eq 2 -and $probe.Connects.Count -eq 1 -and $probe.Connects[0] -eq 7) 'handoff must re-read and use the dynamic session ID'
Check ($result -eq 'handed_off') 'successful handoff must be reported'

$probe.Connects = @(); $probe.Reads = 0
$probe.Samples = @((Snapshot 7 4 0 $sid), (Snapshot 7 4 0 $sid)); $probe.Trace = @(); $probe.Events = @()
$readWithOrder = { $sample = $probe.Samples[$probe.Reads]; $probe.Reads++; $probe.Events += 'read'; $sample }.GetNewClosure()
$connectWithOrder = { param($id) $probe.Connects += $id; $probe.Events += 'connect' }.GetNewClosure()
$trace = { param($phase, $snapshot) $probe.Trace += @{ Phase = $phase; Snapshot = $snapshot }; $probe.Events += ('trace-' + $phase) }.GetNewClosure()
$result = Invoke-DeskhandConsoleHandoff $sid $readWithOrder $connectWithOrder $wait $trace
Check ($result -eq 'handed_off' -and $probe.Connects.Count -eq 1 -and $probe.Connects[0] -eq 7) 'a disconnected session with a reset protocol must hand off'
Check ($probe.Trace.Count -eq 2 -and $probe.Trace[0].Phase -eq 'before' -and $probe.Trace[1].Phase -eq 'after' -and $probe.Trace[1].Snapshot.Protocol -eq 0) 'diagnostics must preserve both actual snapshots'
Check (($probe.Events -join ',') -eq 'read,trace-before,read,connect,trace-after') 'final snapshot check and connect must have no trace IO between them'

$probe.Connects = @(); $probe.Reads = 0; $probe.Samples = @($disconnected, (Snapshot 7 0 2 $sid))
$result = Invoke-DeskhandConsoleHandoff $sid $read $connect $wait
Check ($probe.Connects.Count -eq 0 -and $result -eq 'skipped') 'a person who reconnects during grace must not be disconnected'

$probe.Connects = @(); $probe.Reads = 0; $probe.Samples = @($disconnected, (Snapshot 8 4 2 $sid))
$result = Invoke-DeskhandConsoleHandoff $sid $read $connect $wait
Check ($probe.Connects.Count -eq 0) 'a changed session must not be transferred'

$probe.Connects = @(); $probe.Reads = 0; $probe.Samples = @((Snapshot 7 0 2 $sid))
$result = Invoke-DeskhandConsoleHandoff $sid $read $connect $wait
Check ($probe.Connects.Count -eq 0 -and $result -eq 'skipped') 'manual start while a person is connected must be harmless'
Write-Output 'PASS console handoff policy, task XML, reconnect and identity guards'
