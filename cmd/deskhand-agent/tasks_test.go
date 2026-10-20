package main

import (
	"encoding/xml"
	"io"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestTaskXMLStartsTheInstalledAgentAtTheUsersLogon(t *testing.T) {
	doc := taskXML(`OFFICE-PC\kim & co`, "S-1-5-21-1-2-3-1001")
	var task struct {
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
	dec := xml.NewDecoder(strings.NewReader(doc))
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	if err := dec.Decode(&task); err != nil {
		t.Fatal(err)
	}
	if task.Trigger != `OFFICE-PC\kim & co` || task.Principal.UserID != "S-1-5-21-1-2-3-1001" ||
		task.Principal.LogonType != "InteractiveToken" || task.Principal.RunLevel != "HighestAvailable" {
		t.Fatalf("task %+v", task)
	}
	if task.Command != `C:\Program Files\Deskhand\deskhand-agent.exe` || task.Args != "run" || task.Limit != "PT0S" || task.Battery != "false" {
		t.Fatalf("task %+v", task)
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
	if got := killOthersArgs(42); !reflect.DeepEqual(got, []string{"/F", "/FI", "IMAGENAME eq deskhand-agent.exe", "/FI", "PID ne 42"}) {
		t.Fatalf("taskkill %q", got)
	}
}
