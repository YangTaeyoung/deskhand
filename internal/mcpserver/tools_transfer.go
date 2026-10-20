package mcpserver

import (
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const onConflictDoc = "What to do when the destination already exists: fail (default, the transfer fails for that file), replace, skip, or rename (keep both; the new file gets a numbered name). A folder applies it to every file inside."

const moveDoc = "A move verifies every file on the destination first and removes the source only after that: a source on the PC goes to the Recycle Bin (recoverable), a source on this Mac is deleted. " +
	"If any file fails, is cancelled or skipped, or the source changed, the source is kept. The result's source field says which happened: trashed, deleted, retained, or pending while it runs."

type pushInput struct {
	Host       string `json:"host" jsonschema:"Host: host_id or name from hosts_list."`
	Local      string `json:"local" jsonschema:"Absolute path of the file or folder on this Mac to send, for example /Users/me/build."`
	Remote     string `json:"remote" jsonschema:"Full destination path on the PC (not the parent folder), for example C:\\qa\\build. The parent folder must exist."`
	OnConflict string `json:"on_conflict,omitempty" jsonschema:"Conflict policy; default fail."`
}

type pullInput struct {
	Host       string `json:"host" jsonschema:"Host: host_id or name from hosts_list."`
	Remote     string `json:"remote" jsonschema:"Absolute path of the file or folder on the PC to fetch, for example C:\\logs\\app.log."`
	Local      string `json:"local" jsonschema:"Full destination path on this Mac (not the parent folder), for example /Users/me/logs/app.log. The parent folder must exist."`
	OnConflict string `json:"on_conflict,omitempty" jsonschema:"Conflict policy; default fail."`
}

type transferMoveInput struct {
	Host       string `json:"host" jsonschema:"Host: host_id or name from hosts_list."`
	Direction  string `json:"direction" jsonschema:"push moves from this Mac to the PC; pull moves from the PC to this Mac."`
	Local      string `json:"local" jsonschema:"Path on this Mac: the source for push, the full destination for pull. For example /Users/me/build."`
	Remote     string `json:"remote" jsonschema:"Path on the PC: the full destination for push, the source for pull. For example C:\\qa\\build."`
	OnConflict string `json:"on_conflict,omitempty" jsonschema:"Conflict policy; default fail."`
}

type copyInput struct {
	Host       string `json:"host" jsonschema:"Host: host_id or name from hosts_list."`
	From       string `json:"from" jsonschema:"Existing absolute Windows path of the file or folder to copy."`
	To         string `json:"to" jsonschema:"Full destination path (not the parent folder)."`
	OnConflict string `json:"on_conflict,omitempty" jsonschema:"Conflict policy; default fail."`
	Recursive  bool   `json:"recursive,omitempty" jsonschema:"Needed to copy a folder with its contents."`
}

type hashInput struct {
	Host   string `json:"host" jsonschema:"Host: host_id or name from hosts_list."`
	Path   string `json:"path" jsonschema:"Absolute Windows path of a regular file."`
	Offset int64  `json:"offset,omitempty" jsonschema:"Byte offset to start hashing at; default 0."`
	Length int64  `json:"length,omitempty" jsonschema:"Bytes to hash; 0 or omitted hashes to the end of the file."`
}

type transferIDInput struct {
	TransferID string `json:"transfer_id" jsonschema:"transfer_id from files_push, files_pull, files_move or transfer_list."`
}

type transferListInput struct {
	Host string `json:"host,omitempty" jsonschema:"Only transfers of this host: host_id or name."`
}

func conflictEnum(s *jsonschema.Schema) {
	p := s.Properties["on_conflict"]
	for _, v := range wire.ConflictPolicies() {
		p.Enum = append(p.Enum, v)
	}
}

func (s *Server) registerTransfer(srv *mcp.Server) {
	startNote := " Returns at once with transfer_id and job_id; the copy runs in the background, so poll transfer_get until state is done, failed or cancelled. If the status is unknown, check transfer_list before starting it again."
	add[pushInput](s, srv, "files_push", "Copy a file or folder from this Mac to the PC (the source stays)."+startNote+" "+onConflictDoc,
		spec{Method: "transfer.start", Mutating: true, Extra: map[string]any{"direction": "push"}}, conflictEnum)
	add[pullInput](s, srv, "files_pull", "Copy a file or folder from the PC to this Mac (the source stays)."+startNote+" "+onConflictDoc,
		spec{Method: "transfer.start", Mutating: true, Extra: map[string]any{"direction": "pull"}}, conflictEnum)
	add[transferMoveInput](s, srv, "files_move", "Move a file or folder between this Mac and the PC. "+moveDoc+startNote+" "+onConflictDoc+" To rename inside the PC use files_rename.",
		spec{Method: "transfer.start", Mutating: true, Prepare: func(p map[string]any) {
			if d, _ := p["direction"].(string); d == "push" || d == "pull" {
				p["direction"] = "move_" + d
			}
		}}, func(s *jsonschema.Schema) {
			conflictEnum(s)
			s.Properties["direction"].Enum = []any{"push", "pull"}
		})
	add[copyInput](s, srv, "files_copy", "Copy a file or folder inside the PC (same host, full paths); the source stays. Result: path of the copy, copied (file count) and outcome written, skipped or renamed. "+onConflictDoc,
		spec{Method: "files.copy", Mutating: true}, conflictEnum)
	add[pathInput](s, srv, "files_trash", "Move a file or folder on the PC to the Recycle Bin (recoverable). The result's trashed is true only when the item is in the Recycle Bin; false means nothing was removed (the path did not exist). A failed status with CAPABILITY_UNSUPPORTED means the Recycle Bin does not accept the item and it was left untouched; there is no permanent-delete fallback.",
		spec{Method: "files.trash", Mutating: true}, nil)
	add[hashInput](s, srv, "files_hash", "Compute the SHA-256 of a file on the PC (optionally a byte range) without transferring it. Returns sha256, offset, length (bytes hashed) and size (file size).",
		spec{Method: "files.hash"}, nil)
	add[transferIDInput](s, srv, "transfer_get", "Get a transfer: state (queued, running, verifying, paused, done, failed, cancelled), bytes and file counts, rate_bps (bytes per second), outcome, error and the state of every file. For a move, source says whether the source was trashed, deleted, retained or pending. Poll it while state is queued, running or verifying.",
		spec{Method: "transfer.get"}, nil)
	add[transferListInput](s, srv, "transfer_list", "List transfers, newest first, with file counts and byte totals but no per-file list; use transfer_get for the files. Use it to find a transfer after an unknown outcome.",
		spec{Method: "transfer.list"}, nil)
	add[transferIDInput](s, srv, "transfer_pause", "Pause a running transfer; finished files stay. Resume with transfer_resume. A move that is already removing its source cannot be paused and runs to its end.",
		spec{Method: "transfer.pause", Mutating: true}, nil)
	add[transferIDInput](s, srv, "transfer_resume", "Restart a paused, failed or cancelled transfer; files already done are not sent again and failed files are retried. Poll transfer_get afterwards.",
		spec{Method: "transfer.resume", Mutating: true}, nil)
	add[transferIDInput](s, srv, "transfer_cancel", "Cancel a transfer; finished files stay at the destination and the rest are cancelled. A cancelled move keeps its source. A move that is already removing its source cannot be cancelled.",
		spec{Method: "transfer.cancel", Mutating: true}, nil)
}

func isMoveDirection(d string) bool { return d == "move_push" || d == "move_pull" }

func transferEnded(state string) bool {
	return state == "done" || state == "failed" || state == "cancelled"
}

// sourceOutcome says what became of the source of a move: trashed (PC),
// deleted (this Mac), retained, or pending while the transfer runs; "" for
// a transfer that is not a move.
func sourceOutcome(m map[string]any) string {
	dir, state := str(m, "direction"), str(m, "state")
	switch {
	case !isMoveDirection(dir):
		return ""
	case !transferEnded(state):
		return "pending"
	case state != "done" || str(m, "outcome") == "copied_source_retained":
		return "retained"
	case dir == "move_pull":
		return "trashed"
	}
	return "deleted"
}

func notMoved(m map[string]any) int {
	files, _ := m["files"].([]any)
	n := 0
	for _, f := range files {
		fm, _ := f.(map[string]any)
		if str(fm, "state") != "done" || str(fm, "outcome") == "skipped" {
			n++
		}
	}
	return n
}

// annotate adds the fields a transfer tool derives from the daemon result:
// source for a move, and next_action "transfer_get" for a transfer that is
// running.
func annotate(tool string, m map[string]any) {
	switch tool {
	case "files_push", "files_pull", "files_move":
		if id := str(m, "transfer_id"); id != "" && str(m, "status") == "running" {
			if m["next_action"] == nil {
				m["next_action"] = "transfer_get"
			}
		}
	case "qa_run", "qa_cancel":
		if str(m, "status") == "running" && m["next_action"] == nil {
			m["next_action"] = "qa_get"
		}
	case "qa_get":
		if str(m, "run_id") != "" && str(m, "finished_at") == "" && m["next_action"] == nil {
			m["next_action"] = "qa_get"
		}
	case "transfer_get", "transfer_pause", "transfer_resume", "transfer_cancel":
		if s := sourceOutcome(m); s != "" {
			m["source"] = s
		}
		if st := str(m, "state"); st != "" && !transferEnded(st) && st != "paused" && m["next_action"] == nil {
			m["next_action"] = "transfer_get"
		}
	}
}

// moveText states in a sentence what became of the source of a move.
func moveText(m map[string]any) string {
	switch sourceOutcome(m) {
	case "pending":
		return "Move in progress: the source is untouched until every file is verified on the destination."
	case "trashed":
		return "Move finished: every file was verified on this Mac and the source was moved to the PC's Recycle Bin (recoverable)."
	case "deleted":
		return "Move finished: every file was verified on the PC and the source was deleted from this Mac."
	case "retained":
		why := "the transfer did not finish"
		if n := notMoved(m); n > 0 {
			why = fmt.Sprintf("%d files were not moved", n)
		} else if e, _ := m["error"].(map[string]any); str(e, "message") != "" {
			why = str(e, "message")
		}
		return "Move NOT completed: the source was kept (" + why + "). Files marked done are on the destination too, so the data exists in both places."
	}
	return ""
}

func transferText(m map[string]any) string {
	var b strings.Builder
	done, _ := num(m, "bytes_done")
	total, _ := num(m, "bytes_total")
	fd, _ := num(m, "files_done")
	ft, _ := num(m, "files_total")
	ff, _ := num(m, "files_failed")
	rate, _ := num(m, "rate_bps")
	head := fmt.Sprintf("transfer %v %v state=%v outcome=%v", m["transfer_id"], m["direction"], m["state"], m["outcome"])
	if sourceOutcome(m) == "retained" && str(m, "state") == "done" {
		head = fmt.Sprintf("transfer %v %v COPIED but NOT MOVED (source kept) state=done outcome=%v", m["transfer_id"], m["direction"], m["outcome"])
		if files, _ := m["files"].([]any); len(files) > 0 {
			fd = int64(len(files) - notMoved(m))
		}
	}
	fmt.Fprintf(&b, "%s: %d/%d files copied, %d failed, %d/%d bytes, %d bytes/s", head, fd, ft, ff, done, total, rate)
	if t := moveText(m); t != "" {
		b.WriteString("\n" + t)
	}
	switch str(m, "state") {
	case "failed":
		b.WriteString("\nSome files failed (see files[].error). transfer_resume retries only the failed files.")
	case "cancelled":
		b.WriteString("\nCancelled: finished files stay at the destination; transfer_resume continues the rest.")
	case "paused":
		b.WriteString("\nPaused: transfer_resume continues it.")
	}
	return b.String()
}
