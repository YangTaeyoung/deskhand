// e2ehost reads or makes the controller certificate of the daemon home of
// DESKHAND_HOME and stores host rows in it, for scripts/e2e/phase3.sh.
//
//	e2ehost fingerprint
//	e2ehost put -id ID -name NAME -address ADDR -port PORT -fp AGENT_FP [-os windows] [-arch amd64]
//
// fingerprint prints the SHA-256 fingerprint of the home's controller
// certificate, creating the certificate as the daemon does when it is
// missing. put stores the host row the daemon connects to when it starts;
// the daemon must not be running.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon"
	"github.com/YangTaeyoung/deskhand/internal/daemon/paths"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "e2ehost:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: e2ehost fingerprint | e2ehost put -id ID -name NAME -address ADDR -port PORT -fp FP")
	}
	if os.Getenv("DESKHAND_HOME") == "" {
		return fmt.Errorf("DESKHAND_HOME is not set; e2ehost only works on a test home")
	}
	if err := paths.Ensure(); err != nil {
		return err
	}
	switch args[0] {
	case "fingerprint":
		id, err := daemon.ControllerIdentity()
		if err != nil {
			return err
		}
		fmt.Println(id.Fingerprint)
		return nil
	case "put":
		fs := flag.NewFlagSet("put", flag.ContinueOnError)
		hostID := fs.String("id", "", "host_id")
		name := fs.String("name", "", "host name")
		address := fs.String("address", "127.0.0.1", "address the daemon dials")
		port := fs.Int("port", 0, "UDP port the daemon dials")
		fp := fs.String("fp", "", "agent certificate fingerprint")
		osName := fs.String("os", "windows", "host OS")
		arch := fs.String("arch", "amd64", "host architecture")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *hostID == "" || *name == "" || *port < 1 || *port > 65535 || len(*fp) != 64 {
			return fmt.Errorf("put needs -id, -name, -port 1-65535 and a 64-digit -fp")
		}
		st, err := store.Open(paths.DB())
		if err != nil {
			return err
		}
		defer st.Close()
		row := store.Host{HostID: *hostID, Name: *name, Address: *address, Port: *port, AgentFP: *fp,
			OS: *osName, Arch: *arch, CreatedAt: time.Now()}
		if old, err := st.GetHost(*hostID); err == nil {
			row.CreatedAt = old.CreatedAt
			err = st.UpdateHost(row)
			if err != nil {
				return err
			}
		} else if err := st.PutHost(row); err != nil {
			return err
		}
		return os.MkdirAll(paths.HostDir(*hostID), 0o700)
	}
	return fmt.Errorf("unknown command %q", args[0])
}
