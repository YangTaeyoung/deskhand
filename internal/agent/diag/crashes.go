package diag

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// sourceKind is how listCrashes reads a crash source.
type sourceKind int

const (
	// sourceDumps is a folder of user-mode dumps named <app>.<pid>.dmp, as
	// Windows Error Reporting LocalDumps writes them.
	sourceDumps sourceKind = iota
	// sourceReports is a folder of Windows Error Reporting report folders,
	// each holding Report.wer and possibly dump files.
	sourceReports
	// sourceKernelDumps is a folder of kernel minidumps.
	sourceKernelDumps
	// sourceKernelDump is the kernel memory dump file.
	sourceKernelDump
)

// crashSource is a folder or file listCrashes reads.
type crashSource struct {
	path string
	kind sourceKind
}

// windowsApp is the app of kernel dumps and kernel reports.
const windowsApp = "Windows"

// maxReportBytes bounds the Report.wer files read.
const maxReportBytes = 4 << 20

// crashTypes are the report event types, in lower case, of a crash or of a
// hang that ended a program; kernelTypes those of them that are the
// kernel's.
var (
	crashTypes = map[string]bool{
		"appcrash": true, "bex": true, "bex64": true, "moappcrash": true, "mobex": true, "clr20r3": true,
		"apphangb1": true, "apphangxprocb1": true, "moapphang": true, "moapphangxproc": true,
		"bluescreen": true, "livekernelevent": true,
	}
	kernelTypes = map[string]bool{"bluescreen": true, "livekernelevent": true}
)

// crashReport reports whether a report of eventType is a crash or hang
// report.
func crashReport(eventType string) bool { return crashTypes[strings.ToLower(eventType)] }

// report is what diag.crashes reads of a Report.wer; Time is zero when the
// report has no event time.
type report struct {
	EventType string
	Time      time.Time
	App       string
}

// parseReport reads the Report.wer of the report folder named folder: its
// "Key=Value" lines in UTF-16 LE with a byte order mark, or in UTF-8. The
// app is "Windows" for a kernel report, else OriginalFilename, the file
// name of AppPath, NsAppName without its "praid:" prefix, AppName or the
// second "_" part of the folder name, the first of them that is set.
func parseReport(data []byte, folder string) (report, error) {
	fields := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(reportText(data)))
	sc.Buffer(make([]byte, 64<<10), maxReportBytes)
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimRight(sc.Text(), "\r"), "=")
		if ok {
			if _, dup := fields[k]; !dup {
				fields[k] = strings.TrimSpace(v)
			}
		}
	}
	r := report{EventType: fields["EventType"]}
	if r.EventType == "" {
		return report{}, errors.New("the report has no EventType")
	}
	if ft, err := strconv.ParseUint(fields["EventTime"], 10, 64); err == nil && ft > 0 {
		r.Time = fileTimeToTime(ft)
	}
	_, part, _ := strings.Cut(folder, "_")
	part, _, _ = strings.Cut(part, "_")
	for _, app := range []string{fields["OriginalFilename"], windowsBase(fields["AppPath"]), strings.TrimPrefix(fields["NsAppName"], "praid:"), fields["AppName"], part, folder} {
		if app != "" {
			r.App = app
			break
		}
	}
	if kernelTypes[strings.ToLower(r.EventType)] {
		r.App = windowsApp
	}
	return r, nil
}

// reportText decodes a report file: UTF-16 LE after a byte order mark,
// otherwise UTF-8 without one.
func reportText(data []byte) string {
	if len(data) >= 2 && data[0] == 0xff && data[1] == 0xfe {
		units := make([]uint16, (len(data)-2)/2)
		for i := range units {
			units[i] = binary.LittleEndian.Uint16(data[2+2*i:])
		}
		return string(utf16.Decode(units))
	}
	return string(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))
}

// windowsBase returns the last element of a Windows path.
func windowsBase(p string) string {
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// fileTimeToTime converts a Windows FILETIME count of 100 ns since 1601.
func fileTimeToTime(ft uint64) time.Time {
	const unixEpoch = 116444736000000000
	return time.Unix(0, (int64(ft)-unixEpoch)*100).UTC()
}

// minidumpHeaderSize is the size of MINIDUMP_HEADER.
const minidumpHeaderSize = 32

// miniDumpWithFullMemory is the MINIDUMP_TYPE flag of a dump with all the
// process's memory.
const miniDumpWithFullMemory = 0x2

// minidumpHeader reads the MINIDUMP_HEADER at the start of a user-mode dump:
// the kind (full with all the process's memory, else minidump) and the time
// it was written, zero when the header has none. It reports false for data
// that is no minidump.
func minidumpHeader(b []byte) (kind string, stamp time.Time, ok bool) {
	if len(b) < minidumpHeaderSize || string(b[:4]) != "MDMP" {
		return "", time.Time{}, false
	}
	if s := binary.LittleEndian.Uint32(b[20:]); s != 0 {
		stamp = time.Unix(int64(s), 0).UTC()
	}
	kind = KindMinidump
	if binary.LittleEndian.Uint64(b[24:])&miniDumpWithFullMemory != 0 {
		kind = KindFull
	}
	return kind, stamp, true
}

// localDumpApp returns the app of a LocalDumps file name <app>.<pid>.dmp:
// the name without ".dmp" and without a last part of digits.
func localDumpApp(name string) string {
	base := name
	if strings.EqualFold(filepath.Ext(base), ".dmp") {
		base = base[:len(base)-4]
	}
	if i := strings.LastIndexByte(base, '.'); i > 0 {
		if _, err := strconv.ParseUint(base[i+1:], 10, 64); err == nil {
			base = base[:i]
		}
	}
	return base
}

// crash is a listed dump with its time.
type crash struct {
	Dump
	at time.Time
}

// listCrashes reads each source once and returns its dumps and crash
// reports from since on, newest first and by path at the same time. A
// report folder with dump files lists them with the report's app and time;
// one without lists its Report.wer as a report. A dump's kind and time come
// from its minidump header, a kernel dump's from its source and file time,
// and a dump without a readable header is a minidump of its file time.
func listCrashes(sources []crashSource, since time.Time) []Dump {
	seen := map[string]bool{}
	var all []crash
	for _, s := range sources {
		key := strings.ToLower(filepath.Clean(s.path))
		if seen[key] {
			continue
		}
		seen[key] = true
		switch s.kind {
		case sourceDumps:
			all = append(all, folderDumps(s.path, nil)...)
		case sourceReports:
			all = append(all, reportCrashes(s.path)...)
		case sourceKernelDumps:
			all = append(all, folderDumps(s.path, &crash{Dump: Dump{App: windowsApp, Kind: KindMinidump}})...)
		case sourceKernelDump:
			if fi, err := os.Stat(s.path); err == nil && fi.Mode().IsRegular() {
				all = append(all, crash{Dump: Dump{Path: s.path, App: windowsApp, SizeBytes: fi.Size(), Kind: KindFull}, at: fi.ModTime()})
			}
		}
	}
	out := []Dump{}
	sort.SliceStable(all, func(i, j int) bool {
		if !all[i].at.Equal(all[j].at) {
			return all[i].at.After(all[j].at)
		}
		return all[i].Path < all[j].Path
	})
	for _, c := range all {
		if c.at.Before(since) {
			continue
		}
		c.Time = wire.FormatDiagTime(c.at)
		out = append(out, c.Dump)
	}
	return out
}

// folderDumps returns the .dmp files of dir. With kernel nil each is a
// user-mode dump of its LocalDumps app and minidump header; otherwise each
// has kernel's app and kind and its file time.
func folderDumps(dir string, kernel *crash) []crash {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []crash
	for _, e := range entries {
		if !strings.EqualFold(filepath.Ext(e.Name()), ".dmp") || !e.Type().IsRegular() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if kernel != nil {
			out = append(out, crash{Dump: Dump{Path: path, App: kernel.App, SizeBytes: fi.Size(), Kind: kernel.Kind}, at: fi.ModTime()})
			continue
		}
		c := userDump(path, fi)
		c.App = localDumpApp(e.Name())
		out = append(out, c)
	}
	return out
}

// userDump returns the user-mode dump at path, whose file info is fi, with
// the kind and time of its minidump header, or as a minidump of its file
// time without one.
func userDump(path string, fi os.FileInfo) crash {
	c := crash{Dump: Dump{Path: path, SizeBytes: fi.Size(), Kind: KindMinidump}, at: fi.ModTime()}
	f, err := os.Open(path)
	if err != nil {
		return c
	}
	defer f.Close()
	head := make([]byte, minidumpHeaderSize)
	if _, err := io.ReadFull(f, head); err != nil {
		return c
	}
	if kind, stamp, ok := minidumpHeader(head); ok {
		c.Kind = kind
		if !stamp.IsZero() {
			c.at = stamp
		}
	}
	return c
}

// reportDumpExts are the extensions of the dump files of a report folder.
var reportDumpExts = map[string]bool{".dmp": true, ".mdmp": true, ".hdmp": true}

// reportCrashes returns the crashes of the crash and hang reports among the
// report folders of dir.
func reportCrashes(dir string) []crash {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []crash
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		folder := filepath.Join(dir, e.Name())
		wer := filepath.Join(folder, "Report.wer")
		fi, err := os.Stat(wer)
		if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxReportBytes {
			continue
		}
		data, err := os.ReadFile(wer)
		if err != nil {
			continue
		}
		r, err := parseReport(data, e.Name())
		if err != nil || !crashReport(r.EventType) {
			continue
		}
		files, _ := os.ReadDir(folder)
		var dumps []crash
		for _, f := range files {
			if !reportDumpExts[strings.ToLower(filepath.Ext(f.Name()))] || !f.Type().IsRegular() {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			c := userDump(filepath.Join(folder, f.Name()), info)
			c.App = r.App
			if !r.Time.IsZero() {
				c.at = r.Time
			}
			dumps = append(dumps, c)
		}
		if len(dumps) == 0 {
			at := r.Time
			if at.IsZero() {
				at = fi.ModTime()
			}
			dumps = append(dumps, crash{Dump: Dump{Path: wer, App: r.App, SizeBytes: fi.Size(), Kind: KindReport}, at: at})
		}
		out = append(out, dumps...)
	}
	return out
}
