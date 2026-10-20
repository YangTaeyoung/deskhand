package diag

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReportWerGivesTheEventTypeTimeAndApp(t *testing.T) {
	cases := []struct {
		folder, typ, app string
		time             time.Time
	}{
		{"AppCrash_TabTip.exe_b7738dd3318f02fa6fdf0d2b437804bcc94d6c2_188d341d_a2690f4b-4bba-40de-821a-fe8ca484e46b", "APPCRASH", "TabTip.exe", time.Unix(1784694235, 505032000)},
		{"AppCrash_AppleInc.iCloud__2eb7e6d610b635a2fb7e96ef735dd1d291c7d7b2_ba5d882e_cab_8c94e248-8f9b-466a-b3a1-da8bac6c461e", "MoAppCrash", "iCloudChrome.exe", time.Unix(1787881358, 579491300)},
		{"AppHang_Microsoft.Gaming_a056cec6e8c88e3521e677ba3c4a21746e51111_d6656dfd_d0fa8ee9-2f8a-4e2a-aee8-7087caa1bff8", "MoAppHang", "XboxPcAppFT.exe", time.Unix(1792464514, 433302500)},
		{"Kernel_193_51dffa681c171c9f82e30f41adc9b9aafc2777_00000000_cab_71f1589a-1e58-490d-8903-220740b3dae9", "LiveKernelEvent", "Windows", time.Unix(1778773099, 658115800)},
		{"AppCrash_Shop.exe_0f0e0d0c0b0a090807060504030201_00000000_cab_0b1c2d3e-0000-4000-8000-000000000001", "BEX64", "Shop.exe", time.Unix(1792466400, 0)},
	}
	for _, c := range cases {
		r, err := parseReport(readFixture(t, filepath.Join("wer", c.folder, "Report.wer")), c.folder)
		if err != nil {
			t.Fatalf("%s: %v", c.folder, err)
		}
		if r.EventType != c.typ || r.App != c.app || !r.Time.Equal(c.time) {
			t.Errorf("%s: %+v, expected %s %s %s", c.folder, r, c.typ, c.app, c.time.UTC())
		}
	}
}

func TestTheReportAppComesFromTheFolderNameWithoutAnyAppField(t *testing.T) {
	data := utf16File("Version=1\r\nEventType=APPCRASH\r\nEventTime=134369400000000000\r\n")
	r, err := parseReport(data, "AppCrash_Legacy.exe_0123456789abcdef_00000000_0b1c2d3e")
	if err != nil || r.App != "Legacy.exe" {
		t.Fatalf("report %+v %v", r, err)
	}
}

func TestOnlyCrashAndHangReportsCount(t *testing.T) {
	for _, typ := range []string{"APPCRASH", "appcrash", "BEX", "BEX64", "MoAppCrash", "MoBEX", "CLR20r3", "AppHangB1", "AppHangXProcB1", "MoAppHang", "MoAppHangXProc", "BlueScreen", "LiveKernelEvent"} {
		if !crashReport(typ) {
			t.Errorf("%s is not counted", typ)
		}
	}
	for _, typ := range []string{"WindowsWcpOtherFailure3", "WindowsUpdateFailure3", "StoreAgentInstallFailure1", ""} {
		if crashReport(typ) {
			t.Errorf("%s is counted", typ)
		}
	}
}

// utf16File encodes s as a UTF-16 LE file with a byte order mark.
func utf16File(s string) []byte {
	out := []byte{0xff, 0xfe}
	for _, r := range s {
		out = binary.LittleEndian.AppendUint16(out, uint16(r))
	}
	return out
}

// minidump returns the header of a user-mode minidump written at stamp with
// flags.
func minidump(stamp time.Time, flags uint64) []byte {
	b := []byte("MDMP")
	b = binary.LittleEndian.AppendUint32(b, 0xa793)
	b = binary.LittleEndian.AppendUint32(b, 3)
	b = binary.LittleEndian.AppendUint32(b, 32)
	b = binary.LittleEndian.AppendUint32(b, 0)
	b = binary.LittleEndian.AppendUint32(b, uint32(stamp.Unix()))
	b = binary.LittleEndian.AppendUint64(b, flags)
	return append(b, make([]byte, 64)...)
}

func TestTheDumpKindAndTimeComeFromTheMinidumpHeader(t *testing.T) {
	at := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	kind, stamp, ok := minidumpHeader(minidump(at, 0x0000_1000_0000_0001))
	if !ok || kind != KindMinidump || !stamp.Equal(at) {
		t.Fatalf("minidump %s %v %v", kind, stamp, ok)
	}
	kind, _, ok = minidumpHeader(minidump(at, 0x2|0x4))
	if !ok || kind != KindFull {
		t.Fatalf("full memory %s %v", kind, ok)
	}
	if _, _, ok = minidumpHeader([]byte("PAGEDU64")); ok {
		t.Fatal("a kernel dump was read as a minidump")
	}
}

func TestTheAppOfALocalDumpIsItsFileNameWithoutThePID(t *testing.T) {
	for name, want := range map[string]string{
		"Shop.exe.4013.dmp":       "Shop.exe",
		"dotnet.exe.7712.dmp":     "dotnet.exe",
		"my.tool.v2.exe.1234.dmp": "my.tool.v2.exe",
		"crash.dmp":               "crash",
		"Shop.exe.dmp":            "Shop.exe",
	} {
		if got := localDumpApp(name); got != want {
			t.Errorf("%s: %s, expected %s", name, got, want)
		}
	}
}

func writeFile(t *testing.T, path string, data []byte, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
}

func copyReport(t *testing.T, folder, dest string) {
	t.Helper()
	writeFile(t, filepath.Join(dest, folder, "Report.wer"), readFixture(t, filepath.Join("wer", folder, "Report.wer")), time.Time{})
}

func TestCrashesAreListedNewestFirstFromEverySource(t *testing.T) {
	root := t.TempDir()
	day := func(d int, h int) time.Time { return time.Date(2026, 10, d, h, 0, 0, 0, time.UTC) }
	dumps := filepath.Join(root, "Users", "me", "AppData", "Local", "CrashDumps")
	writeFile(t, filepath.Join(dumps, "Shop.exe.4013.dmp"), minidump(day(20, 10), 0), day(20, 11))
	writeFile(t, filepath.Join(dumps, "dotnet.exe.7712.dmp"), minidump(day(19, 8), 2), day(19, 9))
	writeFile(t, filepath.Join(dumps, "old.exe.1.dmp"), minidump(day(1, 0), 0), day(1, 0))
	writeFile(t, filepath.Join(dumps, "torn.exe.5.dmp"), []byte("MD"), day(18, 7))
	writeFile(t, filepath.Join(dumps, "notes.txt"), []byte("x"), day(20, 12))
	if err := os.MkdirAll(filepath.Join(dumps, "sub.dmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "ProgramData", "Microsoft", "Windows", "WER", "ReportArchive")
	for _, f := range []string{
		"AppHang_Microsoft.Gaming_a056cec6e8c88e3521e677ba3c4a21746e51111_d6656dfd_d0fa8ee9-2f8a-4e2a-aee8-7087caa1bff8",
		"Critical_10.0.26100.9703__714a3ae146071845134ce387c7b5d68cd91f2_00000000_7256cae1-5ba0-4fbd-8031-37413e57e1ac",
		"AppCrash_TabTip.exe_b7738dd3318f02fa6fdf0d2b437804bcc94d6c2_188d341d_a2690f4b-4bba-40de-821a-fe8ca484e46b",
	} {
		copyReport(t, f, archive)
	}
	queue := filepath.Join(root, "ProgramData", "Microsoft", "Windows", "WER", "ReportQueue")
	shop := "AppCrash_Shop.exe_0f0e0d0c0b0a090807060504030201_00000000_cab_0b1c2d3e-0000-4000-8000-000000000001"
	copyReport(t, shop, queue)
	writeFile(t, filepath.Join(queue, shop, "minidump.mdmp"), minidump(day(20, 9), 0), time.Time{})
	writeFile(t, filepath.Join(queue, shop, "memory.hdmp"), minidump(day(20, 9), 0x1|0x2), time.Time{})
	writeFile(t, filepath.Join(queue, shop, "WERInternalMetadata.xml"), []byte("<x/>"), time.Time{})
	writeFile(t, filepath.Join(queue, "loose.txt"), []byte("x"), time.Time{})
	kernel := filepath.Join(root, "Windows", "Minidump")
	writeFile(t, filepath.Join(kernel, "102026-12345-01.dmp"), []byte("PAGEDU64"), day(17, 6))
	memory := filepath.Join(root, "Windows", "MEMORY.DMP")
	writeFile(t, memory, []byte("PAGEDU64"), day(16, 5))

	sources := []crashSource{
		{dumps, sourceDumps}, {archive, sourceReports}, {queue, sourceReports}, {kernel, sourceKernelDumps}, {memory, sourceKernelDump},
		{dumps, sourceDumps}, {filepath.Join(root, "missing"), sourceReports},
	}
	got := listCrashes(context.Background(), sources, day(10, 0))
	var lines []string
	for _, d := range got {
		rel, _ := filepath.Rel(root, d.Path)
		lines = append(lines, fmt.Sprintf("%s %s %s %s %d", d.Time, d.App, d.Kind, filepath.ToSlash(rel), d.SizeBytes))
	}
	shopTime := "2026-10-20T03:20:00.000Z"
	hang := "AppHang_Microsoft.Gaming_a056cec6e8c88e3521e677ba3c4a21746e51111_d6656dfd_d0fa8ee9-2f8a-4e2a-aee8-7087caa1bff8"
	want := []string{
		"2026-10-20T10:00:00.000Z Shop.exe minidump Users/me/AppData/Local/CrashDumps/Shop.exe.4013.dmp 96",
		shopTime + " Shop.exe full ProgramData/Microsoft/Windows/WER/ReportQueue/" + shop + "/memory.hdmp 96",
		shopTime + " Shop.exe minidump ProgramData/Microsoft/Windows/WER/ReportQueue/" + shop + "/minidump.mdmp 96",
		fmt.Sprintf("2026-10-20T02:48:34.433Z XboxPcAppFT.exe report ProgramData/Microsoft/Windows/WER/ReportArchive/%s/Report.wer %d", hang, len(readFixture(t, "wer/"+hang+"/Report.wer"))),
		"2026-10-19T08:00:00.000Z dotnet.exe full Users/me/AppData/Local/CrashDumps/dotnet.exe.7712.dmp 96",
		"2026-10-18T07:00:00.000Z torn.exe minidump Users/me/AppData/Local/CrashDumps/torn.exe.5.dmp 2",
		"2026-10-17T06:00:00.000Z Windows minidump Windows/Minidump/102026-12345-01.dmp 8",
		"2026-10-16T05:00:00.000Z Windows full Windows/MEMORY.DMP 8",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("crashes\n%s\nexpected\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

func TestAFolderListsItsNewestCrashesUpToTheCap(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i := range maxFolderCrashes + 5 {
		at := base.Add(time.Duration(i) * time.Minute)
		writeFile(t, filepath.Join(dir, fmt.Sprintf("app%04d.exe.%d.dmp", i, i)), minidump(at, 0), at)
	}
	got := listCrashes(context.Background(), []crashSource{{dir, sourceDumps}}, base.Add(-time.Hour))
	if len(got) != maxFolderCrashes {
		t.Fatalf("%d crashes listed", len(got))
	}
	if got[0].App != fmt.Sprintf("app%04d.exe", maxFolderCrashes+4) || got[len(got)-1].App != "app0005.exe" {
		t.Fatalf("listed %s to %s", got[0].App, got[len(got)-1].App)
	}
}

func TestFilesOlderThanSinceAreNotRead(t *testing.T) {
	root := t.TempDir()
	since := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	old := since.Add(-24 * time.Hour)
	hang := "AppHang_Microsoft.Gaming_a056cec6e8c88e3521e677ba3c4a21746e51111_d6656dfd_d0fa8ee9-2f8a-4e2a-aee8-7087caa1bff8"
	shop := "AppCrash_Shop.exe_0f0e0d0c0b0a090807060504030201_00000000_cab_0b1c2d3e-0000-4000-8000-000000000001"
	stale := filepath.Join(root, "stale")
	copyReport(t, hang, stale)
	if err := os.Chtimes(filepath.Join(stale, hang, "Report.wer"), old, old); err != nil {
		t.Fatal(err)
	}
	oldFolder := filepath.Join(root, "oldfolder")
	copyReport(t, shop, oldFolder)
	if err := os.Chtimes(filepath.Join(oldFolder, shop), old, old); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(root, "fresh")
	copyReport(t, hang, fresh)
	dumps := filepath.Join(root, "dumps")
	writeFile(t, filepath.Join(dumps, "late.exe.1.dmp"), minidump(since.Add(time.Hour), 0), old)
	got := listCrashes(context.Background(), []crashSource{{stale, sourceReports}, {oldFolder, sourceReports}, {fresh, sourceReports}, {dumps, sourceDumps}}, since)
	if len(got) != 1 || !strings.HasPrefix(got[0].Path, fresh) {
		t.Fatalf("crashes %+v", got)
	}
}

func TestASlowSourceIsLeftOutAtTheWaitLimit(t *testing.T) {
	wait, scan := crashScanWait, scanSource
	t.Cleanup(func() { crashScanWait, scanSource = wait, scan })
	crashScanWait = 200 * time.Millisecond
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	scanSource = func(ctx context.Context, s crashSource, since time.Time) []crash {
		if s.path == "slow" {
			<-release
			return nil
		}
		return scan(ctx, s, since)
	}
	dir := t.TempDir()
	at := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	writeFile(t, filepath.Join(dir, "a.exe.1.dmp"), minidump(at, 0), at)
	began := time.Now()
	got := listCrashes(context.Background(), []crashSource{{"slow", sourceDumps}, {dir, sourceDumps}}, at.Add(-time.Hour))
	if took := time.Since(began); took > 2*time.Second || len(got) != 1 || got[0].App != "a.exe" {
		t.Fatalf("after %s: %+v", took, got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := listCrashes(ctx, []crashSource{{"slow", sourceDumps}}, at); len(got) != 0 {
		t.Fatalf("a cancelled scan listed %+v", got)
	}
}
