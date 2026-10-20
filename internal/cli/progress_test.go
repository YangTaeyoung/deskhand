package cli

import (
	"strings"
	"testing"
)

func tobj(t *testing.T, s string) obj {
	t.Helper()
	o, err := decodeObj([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestFormatRateShowsMegabytesPerSecond(t *testing.T) {
	cases := map[int64]string{0: "0.0 MB/s", 1500000: "1.5 MB/s", 123456789: "123.5 MB/s"}
	for in, want := range cases {
		if got := formatRate(in); got != want {
			t.Errorf("formatRate(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatETA(t *testing.T) {
	cases := []struct {
		remaining, rate int64
		want            string
	}{
		{100, 0, "계산 중"},
		{0, 100, "곧 끝남"},
		{5 * 1000000, 1000000, "5초"},
		{80 * 1000000, 1000000, "1분 20초"},
		{3700 * 1000000, 1000000, "1시간 1분"},
	}
	for _, c := range cases {
		if got := formatETA(c.remaining, c.rate); got != c.want {
			t.Errorf("formatETA(%d,%d) = %q, want %q", c.remaining, c.rate, got, c.want)
		}
	}
}

func TestProgressLineShowsBarPercentRateAndETA(t *testing.T) {
	tr := tobj(t, `{"state":"running","bytes_done":50000000,"bytes_total":100000000,"files_done":1,"files_total":4,"rate_bps":10000000}`)
	line := progressLine(tr, 10)
	for _, want := range []string{"[#####-----]", " 50%", "10.0 MB/s", "남은 시간 5초", "1/4개"} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q lacks %q", line, want)
		}
	}
}

func TestProgressLineWithoutTotalShowsZeroPercent(t *testing.T) {
	line := progressLine(tobj(t, `{"state":"queued","bytes_done":0,"bytes_total":0,"files_done":0,"files_total":0,"rate_bps":0}`), 10)
	if !strings.Contains(line, "[----------]") || !strings.Contains(line, "  0%") || !strings.Contains(line, "대기 중") {
		t.Errorf("line %q", line)
	}
}

func TestSourceOutcome(t *testing.T) {
	cases := []struct {
		json string
		want string
	}{
		{`{"direction":"push","state":"done"}`, ""},
		{`{"direction":"move_pull","state":"done","outcome":"written"}`, "trashed"},
		{`{"direction":"move_push","state":"done","outcome":"written"}`, "deleted"},
		{`{"direction":"move_pull","state":"done","outcome":"copied_source_retained"}`, "retained"},
		{`{"direction":"move_push","state":"failed","outcome":"partial"}`, "retained"},
		{`{"direction":"move_push","state":"cancelled"}`, "retained"},
		{`{"direction":"move_push","state":"running"}`, "pending"},
	}
	for _, c := range cases {
		if got := sourceOutcome(tobj(t, c.json)); got != c.want {
			t.Errorf("%s: sourceOutcome = %q, want %q", c.json, got, c.want)
		}
	}
}

func TestMoveSentenceStatesWhatHappenedToTheSource(t *testing.T) {
	pull := tobj(t, `{"direction":"move_pull","state":"done","outcome":"written","files_total":2,"files_done":2}`)
	if got := moveSentence(pull); got != "원본을 휴지통으로 옮겼습니다" {
		t.Errorf("pull = %q", got)
	}
	push := tobj(t, `{"direction":"move_push","state":"done","outcome":"written","files_total":2,"files_done":2}`)
	if got := moveSentence(push); got != "원본을 지웠습니다" {
		t.Errorf("push = %q", got)
	}
	kept := tobj(t, `{"direction":"move_pull","state":"done","outcome":"copied_source_retained","files_total":5,"files_done":5,
		"files":[{"rel":"a","state":"done","outcome":"skipped"},{"rel":"b","state":"done","outcome":"skipped"},{"rel":"c","state":"failed"},{"rel":"d","state":"done","outcome":"written"}]}`)
	if got := moveSentence(kept); got != "원본을 남겼습니다: 3개 파일이 옮겨지지 않았습니다" {
		t.Errorf("kept = %q", got)
	}
	reason := tobj(t, `{"direction":"move_pull","state":"done","outcome":"copied_source_retained","error":{"code":"INTERNAL","message":"휴지통이 거절했습니다"},"files":[{"rel":"a","state":"done","outcome":"written"}]}`)
	if got := moveSentence(reason); got != "원본을 남겼습니다: 휴지통이 거절했습니다" {
		t.Errorf("reason = %q", got)
	}
	failed := tobj(t, `{"direction":"move_push","state":"failed","outcome":"partial","files":[{"rel":"a","state":"failed"}]}`)
	if got := moveSentence(failed); got != "원본을 남겼습니다: 1개 파일이 옮겨지지 않았습니다" {
		t.Errorf("failed = %q", got)
	}
	cancelled := tobj(t, `{"direction":"move_push","state":"cancelled","files":[]}`)
	if got := moveSentence(cancelled); got != "원본을 남겼습니다: 전송이 끝나기 전에 멈췄습니다" {
		t.Errorf("cancelled = %q", got)
	}
}

func TestTransferExitCodes(t *testing.T) {
	cases := []struct {
		json string
		want int
	}{
		{`{"direction":"push","state":"done","outcome":"written"}`, exitOK},
		{`{"direction":"push","state":"done","outcome":"skipped"}`, exitOK},
		{`{"direction":"move_push","state":"done","outcome":"written"}`, exitOK},
		{`{"direction":"move_push","state":"done","outcome":"copied_source_retained"}`, exitFailure},
		{`{"direction":"push","state":"failed","outcome":"partial"}`, exitFailure},
		{`{"direction":"push","state":"failed","outcome":"failed"}`, exitFailure},
		{`{"direction":"push","state":"cancelled"}`, exitFailure},
		{`{"direction":"push","state":"paused"}`, exitFailure},
		{`{"direction":"push","state":"failed","error":{"code":"OFFLINE","message":"x"}}`, exitOffline},
	}
	for _, c := range cases {
		if got := transferExit(tobj(t, c.json)); got != c.want {
			t.Errorf("%s: exit = %d, want %d", c.json, got, c.want)
		}
	}
}

func TestRemoteBaseHandlesBothSeparators(t *testing.T) {
	for in, want := range map[string]string{`C:\qa\build\`: "build", "/srv/data/": "data", `C:\x.txt`: "x.txt", `C:\`: ""} {
		if got := remoteBase(in); got != want {
			t.Errorf("remoteBase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveRemoteTargetAppendsSourceNameForFolderTarget(t *testing.T) {
	cases := []struct{ remote, src, want string }{
		{`C:\qa\build`, "/tmp/build", `C:\qa\build`},
		{`C:\qa\`, "/tmp/build", `C:\qa\build`},
		{`C:\`, "/tmp/x.txt", `C:\x.txt`},
		{"/srv/data/", "/tmp/x.txt", "/srv/data/x.txt"},
	}
	for _, c := range cases {
		if got := resolveRemoteTarget(c.remote, c.src); got != c.want {
			t.Errorf("resolveRemoteTarget(%q,%q) = %q, want %q", c.remote, c.src, got, c.want)
		}
	}
}

func TestIsLocalPath(t *testing.T) {
	for in, want := range map[string]bool{"./a": true, "../a": true, "/tmp/a": true, "~/a": true, "plain": true, `win:C:\x`: false, "pc1:/tmp": false} {
		if got := isLocalPath(in); got != want {
			t.Errorf("isLocalPath(%q) = %v, want %v", in, got, want)
		}
	}
}
