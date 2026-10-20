package main

import (
	"encoding/xml"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
)

type taskDoc struct {
	Trigger   string `xml:"Triggers>LogonTrigger>UserId"`
	Principal struct {
		UserID    string `xml:"UserId"`
		LogonType string `xml:"LogonType"`
		RunLevel  string `xml:"RunLevel"`
	} `xml:"Principals>Principal"`
	Limit   string `xml:"Settings>ExecutionTimeLimit"`
	Battery string `xml:"Settings>DisallowStartIfOnBatteries"`
	Command string `xml:"Actions>Exec>Command"`
	Args    string `xml:"Actions>Exec>Arguments"`
}

func parseTask(t *testing.T, doc string) taskDoc {
	t.Helper()
	var task taskDoc
	dec := xml.NewDecoder(strings.NewReader(doc))
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	if err := dec.Decode(&task); err != nil {
		t.Fatal(err)
	}
	return task
}

func TestTaskXMLStartsTheWindowlessAgentAtTheUsersLogon(t *testing.T) {
	task := parseTask(t, taskXML(`OFFICE-PC\kim & co`, "S-1-5-21-1-2-3-1001", ""))
	if task.Trigger != `OFFICE-PC\kim & co` || task.Principal.UserID != "S-1-5-21-1-2-3-1001" ||
		task.Principal.LogonType != "InteractiveToken" || task.Principal.RunLevel != "HighestAvailable" {
		t.Fatalf("task %+v", task)
	}
	if task.Command != `"C:\Program Files\Deskhand\deskhand-agentw.exe"` || task.Args != "run" || task.Limit != "PT0S" || task.Battery != "false" {
		t.Fatalf("task %+v", task)
	}
}

func TestTaskXMLPassesAConfigPathOutsideTheDefault(t *testing.T) {
	task := parseTask(t, taskXML(`PC\kim`, "S-1-5-21-1", `D:\Agent Data\a&b "x".json`))
	if want := `run --config "D:\Agent Data\a&b \"x\".json"`; task.Args != want {
		t.Fatalf("arguments %q, want %q", task.Args, want)
	}
}

func TestQuoteArg(t *testing.T) {
	for in, want := range map[string]string{
		`C:\plain\agent.json`:  `C:\plain\agent.json`,
		`C:\with space\a.json`: `"C:\with space\a.json"`,
		`C:\dir with space\`:   `"C:\dir with space\\"`,
		`a"b`:                  `"a\"b"`,
		``:                     `""`,
	} {
		if got := quoteArg(in); got != want {
			t.Fatalf("quoteArg(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTaskConfigPathLeavesTheDefaultOut(t *testing.T) {
	dir := t.TempDir()
	def := filepath.Join(dir, "Deskhand", "agent.json")
	for _, p := range []string{def, strings.ToUpper(def)} {
		if got, err := taskConfigPath(p, def); err != nil || got != "" {
			t.Fatalf("the default path %s gave %q, %v", p, got, err)
		}
	}
	other := filepath.Join(dir, "other", "agent.json")
	if got, err := taskConfigPath(other, def); err != nil || got != other {
		t.Fatalf("another path gave %q, %v", got, err)
	}
}

func TestUTF16FileHasABOMAndCRLF(t *testing.T) {
	b := utf16File("a\n가")
	if b[0] != 0xFF || b[1] != 0xFE {
		t.Fatalf("no byte order mark: % x", b[:2])
	}
	units := make([]uint16, 0, (len(b)-2)/2)
	for i := 2; i+1 < len(b); i += 2 {
		units = append(units, uint16(b[i])|uint16(b[i+1])<<8)
	}
	if got := string(utf16.Decode(units)); got != "a\r\n가" {
		t.Fatalf("decoded %q", got)
	}
}

func TestFirewallLines(t *testing.T) {
	want := `netsh advfirewall firewall add rule name="Deskhand Agent" dir=in action=allow protocol=UDP localport=47600 remoteip=LocalSubnet profile=any`
	if got := firewallAddLine(47600); got != want {
		t.Fatalf("add %s", got)
	}
	if got := firewallDeleteLine(); got != `netsh advfirewall firewall delete rule name="Deskhand Agent"` {
		t.Fatalf("delete %s", got)
	}
}

func TestListenPort(t *testing.T) {
	for in, want := range map[string]int{":47600": 47600, "0.0.0.0:1": 1, "[::]:65535": 65535} {
		if got, err := listenPort(in); err != nil || got != want {
			t.Fatalf("listenPort(%q) = %d, %v", in, got, err)
		}
	}
	for _, in := range []string{"47600", ":0", ":70000", ":x"} {
		if _, err := listenPort(in); err == nil {
			t.Fatalf("listenPort(%q) succeeded", in)
		}
	}
}

func TestTaskCommands(t *testing.T) {
	if got := taskCreateArgs(`C:\ProgramData\Deskhand\task.xml`); !reflect.DeepEqual(got,
		[]string{"/Create", "/TN", "Deskhand Agent", "/XML", `C:\ProgramData\Deskhand\task.xml`, "/F"}) {
		t.Fatalf("create %q", got)
	}
	if got := killOthersArgs("deskhand-agentw.exe", 42); !reflect.DeepEqual(got, []string{"/F", "/FI", "IMAGENAME eq deskhand-agentw.exe", "/FI", "PID ne 42"}) {
		t.Fatalf("taskkill %q", got)
	}
	if !reflect.DeepEqual(agentImages, []string{"deskhand-agent.exe", "deskhand-agentw.exe"}) {
		t.Fatalf("agent images %q", agentImages)
	}
}
