//go:build windows

package diag

import (
	"maps"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/YangTaeyoung/deskhand/internal/agent/sysfolder"
)

const (
	// localDumpsKey holds the Windows Error Reporting LocalDumps settings.
	localDumpsKey = `SOFTWARE\Microsoft\Windows\Windows Error Reporting\LocalDumps`
	// crashControlKey holds the kernel dump settings.
	crashControlKey = `SYSTEM\CurrentControlSet\Control\CrashControl`
)

// crashSources returns the folders and files diag.crashes reads: of every
// profile the LocalDumps default folder and the user's report archive and
// queue, the machine's report archive and queue, the DumpFolder of
// LocalDumps and of each of its program keys (for every profile when it
// names a folder of the profile), and the kernel's minidump folder and
// memory dump file of CrashControl.
func crashSources() []crashSource {
	vars, err := sysfolder.MachineVars()
	if err != nil {
		vars = map[string]string{}
	}
	if pd, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0); err == nil {
		vars["programdata"], vars["allusersprofile"] = pd, pd
	}
	profiles := profileFolders(vars)
	var out []crashSource
	reports := func(base string) {
		wer := filepath.Join(base, "Microsoft", "Windows", "WER")
		out = append(out, crashSource{filepath.Join(wer, "ReportArchive"), sourceReports}, crashSource{filepath.Join(wer, "ReportQueue"), sourceReports})
	}
	for _, p := range profiles {
		local := filepath.Join(p, "AppData", "Local")
		out = append(out, crashSource{filepath.Join(local, "CrashDumps"), sourceDumps})
		reports(local)
	}
	if pd := vars["programdata"]; pd != "" {
		reports(pd)
	}
	for _, raw := range localDumpFolders() {
		for _, dir := range expandForProfiles(raw, vars, profiles) {
			out = append(out, crashSource{dir, sourceDumps})
		}
	}
	if vars["windir"] != "" {
		if dir, err := sysfolder.Expand(crashControlString("MinidumpDir", `%SystemRoot%\Minidump`), vars); err == nil {
			out = append(out, crashSource{dir, sourceKernelDumps})
		}
		if file, err := sysfolder.Expand(crashControlString("DumpFile", `%SystemRoot%\MEMORY.DMP`), vars); err == nil {
			out = append(out, crashSource{file, sourceKernelDump})
		}
	}
	return out
}

// profileFolders returns the profile folder of every account of the
// ProfileList key whose path expands with vars.
func profileFolders(vars map[string]string) []string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, sysfolder.ProfileListKey, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil
	}
	defer k.Close()
	sids, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return nil
	}
	var out []string
	for _, sid := range sids {
		if dir, err := sysfolder.ProfileFolder(sid, vars); err == nil {
			out = append(out, dir)
		}
	}
	return out
}

// localDumpFolders returns the DumpFolder values of the LocalDumps key and
// of its program keys, unexpanded.
func localDumpFolders() []string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, localDumpsKey, registry.QUERY_VALUE|registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil
	}
	defer k.Close()
	var out []string
	if v, _, err := k.GetStringValue("DumpFolder"); err == nil && v != "" {
		out = append(out, v)
	}
	apps, _ := k.ReadSubKeyNames(-1)
	for _, app := range apps {
		sub, err := registry.OpenKey(k, app, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		if v, _, err := sub.GetStringValue("DumpFolder"); err == nil && v != "" {
			out = append(out, v)
		}
		sub.Close()
	}
	return out
}

// profileVars are the variables of a profile folder a DumpFolder can name.
var profileVars = []string{"%localappdata%", "%appdata%", "%userprofile%", "%temp%", "%tmp%"}

// expandForProfiles expands a folder value with vars, and once for every
// profile with that profile's folders when it names one of profileVars.
func expandForProfiles(raw string, vars map[string]string, profiles []string) []string {
	lower := strings.ToLower(raw)
	perProfile := false
	for _, v := range profileVars {
		perProfile = perProfile || strings.Contains(lower, v)
	}
	if !perProfile {
		if dir, err := sysfolder.Expand(raw, vars); err == nil {
			return []string{dir}
		}
		return nil
	}
	var out []string
	for _, p := range profiles {
		pv := maps.Clone(vars)
		local := filepath.Join(p, "AppData", "Local")
		pv["userprofile"], pv["localappdata"], pv["appdata"] = p, local, filepath.Join(p, "AppData", "Roaming")
		pv["temp"], pv["tmp"] = filepath.Join(local, "Temp"), filepath.Join(local, "Temp")
		if dir, err := sysfolder.Expand(raw, pv); err == nil {
			out = append(out, dir)
		}
	}
	return out
}

// crashControlString returns a value of the CrashControl key, or def when
// it is not set.
func crashControlString(name, def string) string {
	if v := registryString(crashControlKey, name); v != "" {
		return v
	}
	return def
}
