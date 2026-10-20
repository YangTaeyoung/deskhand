// Package artifacts reads and exports the artifact folders of jobs
// (<home>/artifacts/<job_id>/) and their manifest.json (docs/local-api.md
// §10).
package artifacts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/ids"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/paths"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// ManifestName is the manifest file of a job folder.
const ManifestName = "manifest.json"

// Manifest lists the files of a job folder with their hashes.
type Manifest struct {
	SchemaVersion int            `json:"schema_version"`
	RunID         string         `json:"run_id,omitempty"`
	JobID         string         `json:"job_id"`
	GeneratedAt   string         `json:"generated_at"`
	Files         []ManifestFile `json:"files"`
}

// ManifestFile is one file of a manifest; Path is relative to the folder
// with / between its parts.
type ManifestFile struct {
	Path       string `json:"path"`
	ArtifactID string `json:"artifact_id"`
	Kind       string `json:"kind"`
	MIME       string `json:"mime"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
}

// Dir returns the artifact folder of a job.
func Dir(jobID string) string { return filepath.Join(paths.Artifacts(), jobID) }

// Digest returns the sha256 and size of a file.
func Digest(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// ReadManifest reads the manifest of dir; ok is false when it has none.
func ReadManifest(dir string) (Manifest, bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, false, nil
	}
	if err != nil {
		return Manifest{}, false, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, false, fmt.Errorf("%s를 읽지 못했습니다: %w", filepath.Join(dir, ManifestName), err)
	}
	return m, true, nil
}

// WriteManifest writes m as the manifest of dir.
func WriteManifest(dir string, m Manifest) error {
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ManifestName), append(data, '\n'), 0o600)
}

// Describe returns the manifest entry of the file rel of dir with its
// hash; known gives the id, kind and type of the files the caller made.
func Describe(dir, rel string, known map[string]ManifestFile) (ManifestFile, error) {
	sum, size, err := Digest(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return ManifestFile{}, err
	}
	f, ok := known[rel]
	if !ok {
		f = ManifestFile{ArtifactID: ids.New(ids.Artifact), Kind: KindOf(rel), MIME: MIMEOf(rel)}
	}
	f.Path, f.Size, f.SHA256 = rel, size, sum
	return f, nil
}

// KindOf returns the artifact kind of a file name.
func KindOf(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg":
		return "screenshot"
	case ".log", ".txt":
		return "log"
	}
	return "file"
}

// MIMEOf returns the media type of a file name.
func MIMEOf(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md":
		return "text/markdown; charset=utf-8"
	case ".log", ".txt":
		return "text/plain; charset=utf-8"
	case ".json":
		return "application/json"
	case ".html":
		return "text/html; charset=utf-8"
	}
	if t := mime.TypeByExtension(filepath.Ext(name)); t != "" {
		return t
	}
	return "application/octet-stream"
}

// files returns the regular files of dir as paths relative to it, without
// the manifest.
func files(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if rel = filepath.ToSlash(rel); rel != ManifestName {
			out = append(out, rel)
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	sort.Strings(out)
	return out, err
}

// List returns the artifacts of a job: the files of its manifest, else the
// artifacts of its result, else the files of its folder.
func List(job model.Job) (string, []model.Artifact, error) {
	dir := Dir(job.JobID)
	m, ok, err := ReadManifest(dir)
	if err != nil {
		return dir, nil, apierr.Internal(fmt.Sprintf("산출물 목록을 읽지 못했습니다(%v). 데몬 로그를 확인하세요", err))
	}
	if ok {
		out := make([]model.Artifact, 0, len(m.Files))
		for _, f := range m.Files {
			p := filepath.Join(dir, filepath.FromSlash(f.Path))
			out = append(out, model.Artifact{ArtifactID: f.ArtifactID, JobID: job.JobID, Kind: f.Kind, Name: filepath.Base(p), Path: p,
				Size: f.Size, MIME: f.MIME, SHA256: f.SHA256, CreatedAt: modTime(p)})
		}
		return dir, out, nil
	}
	var res struct {
		Artifacts []model.Artifact `json:"artifacts"`
	}
	if len(job.Result) > 0 && json.Unmarshal(job.Result, &res) == nil && len(res.Artifacts) > 0 {
		return dir, res.Artifacts, nil
	}
	rels, err := files(dir)
	if err != nil {
		return dir, nil, apierr.Internal(fmt.Sprintf("산출물 폴더 %s를 읽지 못했습니다(%v). 폴더 권한을 확인하세요", dir, err))
	}
	out := []model.Artifact{}
	for _, rel := range rels {
		f, err := Describe(dir, rel, nil)
		if err != nil {
			return dir, nil, apierr.Internal(fmt.Sprintf("산출물 %s를 읽지 못했습니다(%v). 폴더 권한을 확인하세요", rel, err))
		}
		p := filepath.Join(dir, filepath.FromSlash(rel))
		out = append(out, model.Artifact{ArtifactID: f.ArtifactID, JobID: job.JobID, Kind: f.Kind, Name: filepath.Base(p), Path: p,
			Size: f.Size, MIME: f.MIME, SHA256: f.SHA256, CreatedAt: modTime(p)})
	}
	return dir, out, nil
}

func modTime(p string) string {
	if fi, err := os.Stat(p); err == nil {
		return model.Time(fi.ModTime())
	}
	return ""
}

// Exported is what Export copied.
type Exported struct {
	JobID    string `json:"job_id"`
	Dir      string `json:"dir"`
	Files    int    `json:"files"`
	Bytes    int64  `json:"bytes"`
	Manifest string `json:"manifest,omitempty"`
}

// Export copies the artifact folder of a job into a new folder of dest
// named after the job and checks every copy against the artifact hashes.
func Export(job model.Job, dest string) (Exported, error) {
	if dest == "" || !filepath.IsAbs(dest) {
		return Exported{}, apierr.Invalid(fmt.Sprintf("dir %q는 이 Mac의 폴더 절대 경로여야 합니다. /Users/…처럼 주세요", dest))
	}
	if fi, err := os.Stat(dest); err != nil || !fi.IsDir() {
		return Exported{}, apierr.Invalid(fmt.Sprintf("내보낼 폴더 %s가 없습니다. 있는 폴더를 고르세요", dest))
	}
	src := Dir(job.JobID)
	_, list, err := List(job)
	if err != nil {
		return Exported{}, err
	}
	want := map[string]string{}
	for _, a := range list {
		if rel, err := filepath.Rel(src, a.Path); err == nil && !strings.HasPrefix(rel, "..") {
			want[filepath.ToSlash(rel)] = a.SHA256
		}
	}
	rels, err := files(src)
	if err != nil {
		return Exported{}, apierr.Internal(fmt.Sprintf("산출물 폴더 %s를 읽지 못했습니다(%v). 폴더 권한을 확인하세요", src, err))
	}
	_, hasManifest, _ := ReadManifest(src)
	if hasManifest {
		rels = append(rels, ManifestName)
	}
	if len(rels) == 0 {
		return Exported{}, apierr.NotFound(fmt.Sprintf("job %s에는 내보낼 산출물이 없습니다. artifacts.list로 확인하세요", job.JobID))
	}
	target := freeName(dest, job.JobID)
	part := target + ".deskhand-part"
	_ = os.RemoveAll(part)
	out := Exported{JobID: job.JobID, Dir: target}
	fail := func(e *apierr.Error) (Exported, error) {
		_ = os.RemoveAll(part)
		return Exported{}, e
	}
	if err := os.MkdirAll(part, 0o700); err != nil {
		return fail(apierr.Internal(fmt.Sprintf("%s에 폴더를 만들지 못했습니다(%v). 폴더 권한과 남은 공간을 확인하세요", dest, err)))
	}
	for _, rel := range rels {
		to := filepath.Join(part, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
			return fail(apierr.Internal(fmt.Sprintf("%s를 만들지 못했습니다(%v). 폴더 권한을 확인하세요", filepath.Dir(to), err)))
		}
		sum, n, err := copyFile(filepath.Join(src, filepath.FromSlash(rel)), to)
		if err != nil {
			return fail(apierr.Internal(fmt.Sprintf("%s를 복사하지 못했습니다(%v). 남은 공간과 권한을 확인하세요", rel, err)))
		}
		if w, ok := want[rel]; ok && w != "" && w != sum {
			return fail(apierr.New(wire.CodeHashMismatch, fmt.Sprintf("%s의 sha256이 기록과 다릅니다. 산출물이 바뀌었을 수 있으니 원본 폴더 %s를 확인하세요", rel, src), false, ""))
		}
		out.Files++
		out.Bytes += n
	}
	if err := os.Rename(part, target); err != nil {
		return fail(apierr.Internal(fmt.Sprintf("%s로 이름을 바꾸지 못했습니다(%v). 폴더 권한을 확인하세요", target, err)))
	}
	if hasManifest {
		out.Manifest = filepath.Join(target, ManifestName)
	}
	return out, nil
}

// freeName returns dir/name, or dir/"name (n)" for the first n that is free.
func freeName(dir, name string) string {
	p := filepath.Join(dir, name)
	for n := 1; ; n++ {
		if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
			return p
		}
		p = filepath.Join(dir, fmt.Sprintf("%s (%d)", name, n))
	}
}

func copyFile(from, to string) (string, int64, error) {
	in, err := os.Open(from)
	if err != nil {
		return "", 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", 0, err
	}
	if fi, err := os.Stat(from); err == nil {
		_ = os.Chtimes(to, time.Now(), fi.ModTime())
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
