package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/YangTaeyoung/deskhand/internal/client"
	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
)

type controlRequestInput struct {
	Host string `json:"host" jsonschema:"Host id or name from hosts_list."`
}

type captureInput struct {
	Host     string `json:"host" jsonschema:"Host id or name from hosts_list."`
	Display  int    `json:"display,omitempty" jsonschema:"Display index, default 0."`
	MaxWidth int    `json:"max_width,omitempty" jsonschema:"Maximum image width. Default 1568; pass 0 for the full display only through the local API."`
	Format   string `json:"format,omitempty" jsonschema:"Image format: png or jpeg; default png."`
}

type observeInput struct {
	Host     string `json:"host" jsonschema:"Host id or name from hosts_list."`
	Level    string `json:"level,omitempty" jsonschema:"Observation level: none, summary, image, or ui."`
	SettleMS int    `json:"settle_ms,omitempty" jsonschema:"Wait for the screen or window events to settle, in milliseconds."`
	Display  int    `json:"display,omitempty" jsonschema:"Display index, default 0."`
}

type afterInput struct {
	Level    string `json:"level,omitempty" jsonschema:"Optional post-action observation level: none, summary, image, or ui."`
	SettleMS int    `json:"settle_ms,omitempty" jsonschema:"Maximum wait for the screen to settle in milliseconds."`
}

type pointInput struct {
	Host      string      `json:"host" jsonschema:"Host id or name from hosts_list."`
	CaptureID string      `json:"capture_id" jsonschema:"capture_id from screen_capture; coordinates must refer to that image."`
	X         float64     `json:"x" jsonschema:"X coordinate in the captured image."`
	Y         float64     `json:"y" jsonschema:"Y coordinate in the captured image."`
	Button    string      `json:"button,omitempty" jsonschema:"Mouse button: left (default), right, middle, x1 or x2."`
	Count     int         `json:"count,omitempty" jsonschema:"Click count from 1 to 3; default 1."`
	ToX       *float64    `json:"to_x,omitempty" jsonschema:"Drag destination X in the same captured image, including zero."`
	ToY       *float64    `json:"to_y,omitempty" jsonschema:"Drag destination Y in the same captured image, including zero."`
	Steps     int         `json:"steps,omitempty" jsonschema:"Drag movement steps, 1–100; default 10."`
	Duration  int         `json:"duration_ms,omitempty" jsonschema:"Drag duration in milliseconds, 0–10000."`
	DX        float64     `json:"dx,omitempty" jsonschema:"Horizontal wheel notches, positive right."`
	DY        float64     `json:"dy,omitempty" jsonschema:"Vertical wheel notches, positive up."`
	Observe   *afterInput `json:"observe,omitempty" jsonschema:"Optional observation after the input; its failure is separate from the input result."`
}

type keyInput struct {
	Host    string      `json:"host" jsonschema:"Host id or name from hosts_list."`
	Keys    string      `json:"keys" jsonschema:"Key or combination, for example enter or ctrl+shift+esc."`
	Observe *afterInput `json:"observe,omitempty" jsonschema:"Optional observation after the input."`
}

type typeInput struct {
	Host    string      `json:"host" jsonschema:"Host id or name from hosts_list."`
	Text    string      `json:"text" jsonschema:"Text to enter; inspect the result before retrying any partial or unknown outcome."`
	Method  string      `json:"method,omitempty" jsonschema:"auto (default), unicode, or paste. Auto chooses paste up front for emoji, controls or long text."`
	Observe *afterInput `json:"observe,omitempty" jsonschema:"Optional observation after the input."`
}

// uiInput exposes the selector fields accepted by the Windows UI Automation
// helper; the helper's camelCase fields are preserved verbatim.
type uiInput struct {
	Host         string `json:"host" jsonschema:"Host id or name from hosts_list."`
	Window       string `json:"window,omitempty" jsonschema:"Window id from win_list, title substring, or foreground."`
	Ref          string `json:"ref,omitempty" jsonschema:"Element reference from ui_tree or ui_find; stale references must be re-observed."`
	Root         string `json:"root,omitempty" jsonschema:"Element reference to use as a subtree root."`
	Role         string `json:"role,omitempty" jsonschema:"UI Automation role to find."`
	Name         string `json:"name,omitempty" jsonschema:"Exact UI Automation name to find."`
	NameContains string `json:"nameContains,omitempty" jsonschema:"Substring of UI Automation name."`
	AutomationID string `json:"automationId,omitempty" jsonschema:"UI Automation automation id."`
	Index        *int   `json:"index,omitempty" jsonschema:"Zero-based match index."`
	Filter       string `json:"filter,omitempty" jsonschema:"Tree filter: interactive (default) or all."`
	Depth        *int   `json:"depth,omitempty" jsonschema:"Maximum tree depth."`
	Max          *int   `json:"max,omitempty" jsonschema:"Maximum number of tree nodes."`
	Limit        *int   `json:"limit,omitempty" jsonschema:"Maximum matching elements."`
	Mode         string `json:"mode,omitempty" jsonschema:"Text mode: auto, pattern or descendants."`
	Offset       *int   `json:"offset,omitempty" jsonschema:"Text byte offset."`
	Length       *int   `json:"length,omitempty" jsonschema:"Text length."`
	Action       string `json:"action,omitempty" jsonschema:"UI action, for example invoke, focus or set_value."`
	Value        string `json:"value,omitempty" jsonschema:"Value for set_value; keep secrets out of transcripts."`
	CaptureID    string `json:"capture_id,omitempty" jsonschema:"For ui_at: capture_id from screen_capture, with x and y in that image."`
	X            *int   `json:"x,omitempty" jsonschema:"For ui_at: X in the captured image or virtual desktop."`
	Y            *int   `json:"y,omitempty" jsonschema:"For ui_at: Y in the captured image or virtual desktop."`
	Focused      bool   `json:"focused,omitempty" jsonschema:"For ui_at: inspect the focused element."`
}

func (s *Server) registerScreen(srv *mcp.Server) {
	add[hostInput](s, srv, "control_status", "Read who controls this PC and whether AI input is paused. Check before input; observing remains available while a person controls the PC.", spec{Method: "control.get"}, nil)
	add[controlRequestInput](s, srv, "control_request", "Request the AI input lease for this PC. It never takes control from a person or another AI client; CONTROL_HELD_BY_HUMAN includes the action to wait for.", spec{Method: "control.acquire", Mutating: true, Extra: map[string]any{"as": "ai"}}, nil)
	add[hostInput](s, srv, "control_release", "Release this MCP client's AI input lease. It cannot release a person's or another AI client's control.", spec{Method: "control.release", Mutating: true}, nil)
	add[observeInput](s, srv, "observe", "Read the foreground window, focused element and optional still image/UI tree. settle_ms waits for two stable samples; changed reports whether the screen changed since the prior observation.", spec{Method: "observe"}, nil)
	for _, op := range []struct{ name, method, description string }{
		{"input_click", "input.click", "Click a point in a recent screen_capture image. Pass its capture_id and image x,y; inspect failed, partial and unknown outcomes before any retry."},
		{"input_move", "input.move", "Move the pointer to x,y in a recent screen_capture image."},
		{"input_drag", "input.drag", "Drag from x,y to to_x,to_y in the same recent screen_capture image."},
		{"input_scroll", "input.scroll", "Scroll at x,y of a recent screen_capture image; dx and dy are wheel notches, positive dy is up."},
	} {
		add[pointInput](s, srv, op.name, op.description, spec{Method: op.method, Mutating: true}, nil)
	}
	add[keyInput](s, srv, "input_key", "Press a key or combination on the PC, for example enter or ctrl+shift+esc. Input is denied while a person holds control.", spec{Method: "input.key", Mutating: true}, nil)
	add[typeInput](s, srv, "input_type", "Enter text on the PC. auto selects unicode or paste before sending; partial and unknown outcomes must be checked on the screen before any retry.", spec{Method: "input.type", Mutating: true}, nil)
	for _, op := range []struct {
		name, method, description string
		mutating                  bool
	}{
		{"ui_tree", "ui.tree", "Read the Windows UI Automation tree of a window, with element refs, names and roles. Password fields are masked.", false},
		{"ui_find", "ui.find", "Find UI Automation elements by window, role, name, nameContains or automationId; inspect refs before acting.", false},
		{"ui_text", "ui.text", "Read text of a UI Automation element or window; password fields are masked.", false},
		{"ui_act", "ui.act", "Act on an element ref or selector, for example invoke or set_value. Control ownership and writer generation are checked before the action.", true},
		{"ui_at", "ui.at", "Find the element at x,y of a screen_capture image, or inspect a ref or the focused element.", false},
		{"win_list", "win.list", "List the top-level windows of the Windows desktop, with title, process and window id.", false},
		{"win_action", "win.action", "Focus, minimize, maximize, restore or close a window from win_list. Control ownership is checked.", true},
	} {
		add[uiInput](s, srv, op.name, op.description, spec{Method: op.method, Mutating: op.mutating}, nil)
	}
	s.registerScreenCapture(srv)
}

// registerScreenCapture uses the daemon's screenshot artifact as real MCP
// image content while retaining the capture_id and display metadata.
func (s *Server) registerScreenCapture(srv *mcp.Server) {
	schema, err := jsonschema.For[captureInput](nil)
	if err != nil {
		panic(err)
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "screen_capture", Description: "Capture a still image of the PC and return the image plus capture_id, dimensions, origin, display, connection_generation and expiry. Use that capture_id with image coordinates for input_click, input_move, input_drag, input_scroll or ui_at.", InputSchema: schema},
		func(ctx context.Context, req *mcp.CallToolRequest, in captureInput) (*mcp.CallToolResult, any, error) {
			sp := spec{Method: "screen.capture"}
			params, err := toParams(in)
			if err != nil {
				return failure(sp, apierr.Invalid("Capture parameters could not be encoded.")), nil, nil
			}
			if _, ok := params["max_width"]; !ok {
				params["max_width"] = 1568
			}
			c, err := s.conn(req)
			if err != nil {
				return failure(sp, err), nil, nil
			}
			cctx, cancel := context.WithTimeout(ctx, callTimeout)
			defer cancel()
			var raw json.RawMessage
			if err := c.Call(cctx, sp.Method, params, &raw); err != nil {
				if client.IsConnectionLost(err) {
					s.drop(c)
				}
				return failure(sp, err), nil, nil
			}
			var result map[string]any
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.UseNumber()
			if err := dec.Decode(&result); err != nil {
				return failure(sp, apierr.Internal("The screenshot metadata could not be read; check the daemon version.")), nil, nil
			}
			art, _ := result["artifact"].(map[string]any)
			path, _ := art["path"].(string)
			if path == "" {
				return failure(sp, apierr.Internal("The screenshot has no artifact path; check the daemon log.")), nil, nil
			}
			f, err := os.Open(path)
			if err != nil {
				return failure(sp, apierr.Internal("The screenshot file could not be opened; capture again.")), nil, nil
			}
			defer f.Close()
			fi, err := f.Stat()
			if err != nil || fi.Size() > 32<<20 {
				return failure(sp, apierr.Internal("The screenshot is too large to return; lower max_width or use jpeg.")), nil, nil
			}
			data := make([]byte, fi.Size())
			if _, err := io.ReadFull(f, data); err != nil {
				return failure(sp, apierr.Internal("The screenshot could not be read; capture again.")), nil, nil
			}
			mime := "image/png"
			if strings.EqualFold(fmt.Sprint(result["format"]), "jpeg") {
				mime = "image/jpeg"
			}
			return &mcp.CallToolResult{StructuredContent: result, Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("capture_id=%v host_id=%v display=%v %v×%v; use this image's coordinates for input", result["capture_id"], result["host_id"], result["display"], result["width"], result["height"])},
				&mcp.ImageContent{MIMEType: mime, Data: data},
			}}, nil, nil
		})
}
