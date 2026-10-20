package wire

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// PTYNew describes a new pseudo-terminal session.
type PTYNew struct {
	Shell string            `json:"shell,omitempty"`
	Cwd   string            `json:"cwd,omitempty"`
	Cols  int               `json:"cols,omitempty"`
	Rows  int               `json:"rows,omitempty"`
	Env   map[string]string `json:"env,omitempty"`
}

// StreamHeader is the first line of every non-rpc stream.
type StreamHeader struct {
	S          string   `json:"s"`
	Display    int      `json:"display,omitempty"`
	MaxFPS     int      `json:"max_fps,omitempty"`
	Bitrate    int      `json:"bitrate,omitempty"`
	MaxWidth   int      `json:"max_width,omitempty"`
	Codec      string   `json:"codec,omitempty"`
	Gen        uint64   `json:"gen,omitempty"`
	Job        string   `json:"job,omitempty"`
	New        *PTYNew  `json:"new,omitempty"`
	Port       int      `json:"port,omitempty"`
	TransferID string   `json:"transfer_id,omitempty"`
	Path       string   `json:"path,omitempty"`
	Offset     int64    `json:"offset,omitempty"`
	Size       int64    `json:"size,omitempty"`
	Length     int64    `json:"length,omitempty"`
	MTime      string   `json:"mtime,omitempty"`
	Roots      []string `json:"roots,omitempty"`
}

// WriteHeader writes v as one JSON line.
func WriteHeader(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > MaxHeader {
		return ErrHeaderTooLarge
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// ReadHeader reads one newline-terminated JSON line of at most MaxHeader
// bytes into v, leaving the rest of r unread.
func ReadHeader(r *bufio.Reader, v any) error {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > MaxHeader+1 {
			return ErrHeaderTooLarge
		}
		if err == nil {
			break
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return err
	}
	line = bytes.TrimSuffix(line, []byte{'\n'})
	if len(line) > MaxHeader {
		return ErrHeaderTooLarge
	}
	return json.Unmarshal(line, v)
}
