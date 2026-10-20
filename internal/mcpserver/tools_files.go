package mcpserver

import (
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxReadBytes is the most files_read returns per call.
const maxReadBytes = 262144

// maxWriteBase64 is the longest base64 data of one files_write call.
const maxWriteBase64 = 1048576

type pathInput struct {
	Host string `json:"host" jsonschema:"Host: host_id or name from hosts_list."`
	Path string `json:"path" jsonschema:"Absolute Windows path, for example C:\\Users\\me\\notes.txt."`
}

type statInput struct {
	Host   string `json:"host" jsonschema:"Host: host_id or name from hosts_list."`
	Path   string `json:"path" jsonschema:"Absolute Windows path, for example C:\\Users\\me\\notes.txt."`
	SHA256 bool   `json:"sha256,omitempty" jsonschema:"Also compute the SHA-256 of a file."`
}

type readInput struct {
	Host   string `json:"host" jsonschema:"Host: host_id or name from hosts_list."`
	Path   string `json:"path" jsonschema:"Absolute Windows path of a regular file, for example C:\\Users\\me\\log.txt."`
	Offset int64  `json:"offset,omitempty" jsonschema:"Byte offset to start at; default 0."`
	Length int    `json:"length,omitempty" jsonschema:"Bytes to read; default and maximum 262144 (256 KiB) per call. Page through larger files by raising offset until eof is true."`
}

type writeInput struct {
	Host     string `json:"host" jsonschema:"Host: host_id or name from hosts_list."`
	Path     string `json:"path" jsonschema:"Absolute Windows path of the file to write."`
	Data     string `json:"data" jsonschema:"File bytes encoded as base64, at most 1048576 characters (768 KiB decoded) per call; an empty string writes an empty file."`
	Offset   int64  `json:"offset,omitempty" jsonschema:"Byte offset to write at; default 0."`
	Append   bool   `json:"append,omitempty" jsonschema:"Append to the end of the file instead of writing at offset."`
	Truncate bool   `json:"truncate,omitempty" jsonschema:"Cut the file to the end of this write."`
	Mkdirs   bool   `json:"mkdirs,omitempty" jsonschema:"Create missing parent folders."`
}

type moveInput struct {
	Host      string `json:"host" jsonschema:"Host: host_id or name from hosts_list."`
	From      string `json:"from" jsonschema:"Existing absolute Windows path, for example C:\\tmp\\a.txt."`
	To        string `json:"to" jsonschema:"New absolute Windows path, for example C:\\tmp\\b.txt."`
	Overwrite bool   `json:"overwrite,omitempty" jsonschema:"Replace the destination if it exists."`
}

type deleteInput struct {
	Host      string `json:"host" jsonschema:"Host: host_id or name from hosts_list."`
	Path      string `json:"path" jsonschema:"Absolute Windows path of the file or folder to delete."`
	Recursive bool   `json:"recursive,omitempty" jsonschema:"Delete a folder with its contents."`
}

func (s *Server) registerFiles(srv *mcp.Server) {
	add[pathInput](s, srv, "files_list", "List the entries of a folder on the PC (name, path, dir, size, mtime). Example path: C:\\Users\\me.",
		spec{Method: "files.list"}, nil)
	add[statInput](s, srv, "files_stat", "Tell whether a path exists and whether it is a file or folder, with size and optional SHA-256. Use it to verify the result of a write, move or delete, especially after an unknown outcome.",
		spec{Method: "files.stat"}, nil)
	add[readInput](s, srv, "files_read",
		"Read bytes of a file as base64 in data. Returns at most 256 KiB (262144 bytes) per call; page with offset and length until eof is true. size is the whole file size.",
		spec{Method: "files.read"}, func(s *jsonschema.Schema) {
			props := s.Properties
			zero, max := 0.0, float64(maxReadBytes)
			props["offset"].Minimum = &zero
			one := 1.0
			props["length"].Minimum = &one
			props["length"].Maximum = &max
			props["length"].Default = []byte("262144")
		})
	add[writeInput](s, srv, "files_write", "Write base64 data to a file on the PC (at most 768 KiB of decoded bytes, 1048576 base64 characters, per call; split larger files with offset or append) (replaces the file unless offset or append is set). Verify with files_stat if the outcome is unknown.",
		spec{Method: "files.write", Mutating: true}, func(s *jsonschema.Schema) {
			n := maxWriteBase64
			s.Properties["data"].MaxLength = &n
		})
	add[pathInput](s, srv, "files_mkdir", "Create a folder (and missing parents) on the PC.",
		spec{Method: "files.mkdir", Mutating: true}, nil)
	add[moveInput](s, srv, "files_move", "Move or rename a file or folder on the PC.",
		spec{Method: "files.rename", Mutating: true}, nil)
	add[deleteInput](s, srv, "files_delete", "Permanently delete a file or folder on the PC. This cannot be undone; there is no recycle bin.",
		spec{Method: "files.delete", Mutating: true, Extra: map[string]any{"permanent": true}}, nil)
}
