package main

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"
)

const (
	// taskName names the scheduled task that starts the agent at logon.
	taskName = "Deskhand Agent"
	// consoleHandoffTaskName is reserved for explicitly enabled unattended mode.
	consoleHandoffTaskName = "Deskhand Console Handoff"
	// firewallRule names the inbound firewall rule of the agent's UDP port.
	firewallRule = "Deskhand Agent"
	// installDir holds the installed agent.
	installDir = `C:\Program Files\Deskhand`
	// installedExe is the installed console build of the agent, run by
	// people, SSH and scripts.
	installedExe = installDir + `\deskhand-agent.exe`
	// windowExeName is the file name of the windowsgui build of the agent,
	// the program of the scheduled task.
	windowExeName = "deskhand-agentw.exe"
	// installedWindowExe is the installed windowsgui build.
	installedWindowExe = installDir + `\` + windowExeName
)

// agentImages are the image names of agent processes.
var agentImages = []string{"deskhand-agent.exe", windowExeName}

// removeManagedTasks always attempts both app-owned task names. In particular,
// a failed agent removal must not leave automatic console handoff enabled.
func removeManagedTasks(remove func(string) error) error {
	var errs []error
	for _, name := range []string{taskName, consoleHandoffTaskName} {
		if err := remove(name); err != nil {
			errs = append(errs, fmt.Errorf("remove task %q: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// xmlText escapes s for XML element content.
func xmlText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// quoteArg quotes s for a Windows command line the way CommandLineToArgvW
// splits it, when s is empty or holds a space, a tab or a quote.
func quoteArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for _, c := range s {
		switch c {
		case '\\':
			slashes++
			continue
		case '"':
			b.WriteString(strings.Repeat(`\`, 2*slashes+1))
		default:
			b.WriteString(strings.Repeat(`\`, slashes))
		}
		slashes = 0
		b.WriteRune(c)
	}
	b.WriteString(strings.Repeat(`\`, 2*slashes))
	b.WriteByte('"')
	return b.String()
}

// taskArguments returns the arguments of the task's action: run, with
// --config and configPath when configPath is not empty.
func taskArguments(configPath string) string {
	if configPath == "" {
		return "run"
	}
	return "run --config " + quoteArg(configPath)
}

// taskConfigPath returns the absolute form of path for the task's --config,
// or "" when it names defaultPath, compared without regard to case.
func taskConfigPath(path, defaultPath string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("the config path %s cannot be made absolute (%v); pass --config with a full path", path, err)
	}
	def, err := filepath.Abs(defaultPath)
	if err == nil && strings.EqualFold(filepath.Clean(abs), filepath.Clean(def)) {
		return "", nil
	}
	return abs, nil
}

// taskXML returns the Task Scheduler definition of the agent's task: it
// starts the quoted installedWindowExe with taskArguments(configPath) at the logon
// of user (DOMAIN\name), runs as the account with the SID sid only while it
// is logged on, at the highest run level, without a time limit, and starts
// again a minute after a failure.
func taskXML(user, sid, configPath string) string {
	return `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Deskhand Agent</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>` + xmlText(user) + `</UserId>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>` + xmlText(sid) + `</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>HighestAvailable</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>false</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings>
      <StopOnIdleEnd>false</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <WakeToRun>false</WakeToRun>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>999</Count>
    </RestartOnFailure>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>` + xmlText(quoteArg(installedWindowExe)) + `</Command>
      <Arguments>` + xmlText(taskArguments(configPath)) + `</Arguments>
    </Exec>
  </Actions>
</Task>
`
}

// utf16File returns s as UTF-16LE with a byte order mark and CRLF line ends,
// the form schtasks /XML reads.
func utf16File(s string) []byte {
	s = strings.ReplaceAll(s, "\n", "\r\n")
	units := utf16.Encode([]rune(s))
	out := make([]byte, 2, 2+2*len(units))
	out[0], out[1] = 0xFF, 0xFE
	for _, u := range units {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

// listenPort returns the port of a listen address such as ":47600".
func listenPort(listen string) (int, error) {
	_, p, err := net.SplitHostPort(listen)
	if err != nil {
		return 0, fmt.Errorf("the listen address %q of agent.json has no port; run deskhand-agent init --force --listen :47600", listen)
	}
	port, err := strconv.Atoi(p)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("the listen port %q of agent.json is not 1 to 65535; run deskhand-agent init --force --listen :47600", p)
	}
	return port, nil
}

// firewallAddLine is the netsh command line that lets the local subnet reach
// UDP port on every profile.
func firewallAddLine(port int) string {
	return fmt.Sprintf(`netsh advfirewall firewall add rule name="%s" dir=in action=allow protocol=UDP localport=%d remoteip=LocalSubnet profile=any`,
		firewallRule, port)
}

// firewallDeleteLine is the netsh command line that removes the agent's rule.
func firewallDeleteLine() string {
	return fmt.Sprintf(`netsh advfirewall firewall delete rule name="%s"`, firewallRule)
}

// schtasks argument lists of the agent's task.
func taskCreateArgs(xmlPath string) []string {
	return []string{"/Create", "/TN", taskName, "/XML", xmlPath, "/F"}
}
func taskRunArgs() []string    { return []string{"/Run", "/TN", taskName} }
func taskEndArgs() []string    { return []string{"/End", "/TN", taskName} }
func taskDeleteArgs() []string { return []string{"/Delete", "/TN", taskName, "/F"} }

// killOthersArgs are the taskkill arguments that end every process of image
// but the one with pid self.
func killOthersArgs(image string, self int) []string {
	return []string{"/F", "/FI", "IMAGENAME eq " + image, "/FI", fmt.Sprintf("PID ne %d", self)}
}
