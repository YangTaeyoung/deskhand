package wire

import (
	"encoding/json"
	"testing"
)

func TestPTYMessagesJSON(t *testing.T) {
	cases := []struct {
		v    any
		want string
	}{
		{PTYResize{Cols: 80, Rows: 24}, `{"cols":80,"rows":24}`},
		{PTYExit{Code: 0}, `{"code":0}`},
		{PTYAttached{Job: "j-1", PID: 42}, `{"job":"j-1","pid":42}`},
	}
	for _, c := range cases {
		b, err := json.Marshal(c.v)
		if err != nil || string(b) != c.want {
			t.Fatalf("got %s err=%v want %s", b, err, c.want)
		}
	}
}
