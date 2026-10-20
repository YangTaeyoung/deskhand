package wire

// PTYResize is the body of a KindPTYResize frame.
type PTYResize struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// PTYExit is the body of a KindPTYExit frame.
type PTYExit struct {
	Code int `json:"code"`
}

// PTYAttached is the body of a KindPTYAttached frame.
type PTYAttached struct {
	Job string `json:"job"`
	PID int    `json:"pid"`
}
