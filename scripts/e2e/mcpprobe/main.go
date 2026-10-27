// mcpprobe starts `deskhand mcp` as an MCP client would, lists its tools
// and calls the tools given on the command line, for the end-to-end scripts.
//
//	mcpprobe [-deskhand path] [-timeout 2m] tool=params-json ...
//
// For every call it prints one JSON line {"tool", "is_error", "structured",
// "text", "elapsed_ms"} after one line {"tools": [names]}. It exits 1 when a
// call fails at the protocol level or returns an error result.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("mcpprobe", flag.ContinueOnError)
	bin := fs.String("deskhand", "deskhand", "the deskhand program")
	timeout := fs.Duration("timeout", 2*time.Minute, "how long the whole probe may take")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	type call struct {
		tool string
		args map[string]any
	}
	var calls []call
	for _, a := range fs.Args() {
		name, raw, _ := strings.Cut(a, "=")
		c := call{tool: name, args: map[string]any{}}
		if raw != "" {
			if err := json.Unmarshal([]byte(raw), &c.args); err != nil {
				fmt.Fprintf(os.Stderr, "mcpprobe: params of %s are not a JSON object: %v\n", name, err)
				return 2
			}
		}
		calls = append(calls, c)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	cl := mcp.NewClient(&mcp.Implementation{Name: "deskhand-e2e-mcpprobe", Version: "1"}, nil)
	cmd := exec.Command(*bin, "mcp")
	cmd.Stderr = os.Stderr
	cs, err := cl.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcpprobe: connect:", err)
		return 1
	}
	defer cs.Close()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mcpprobe: tools/list:", err)
		return 1
	}
	names := make([]string, 0, len(tools.Tools))
	for _, t := range tools.Tools {
		names = append(names, t.Name)
	}
	sort.Strings(names)
	printJSON(map[string]any{"server": cs.InitializeResult().ServerInfo, "tools": names})
	code := 0
	for _, c := range calls {
		start := time.Now()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: c.tool, Arguments: c.args})
		elapsed := time.Since(start).Milliseconds()
		if err != nil {
			printJSON(map[string]any{"tool": c.tool, "protocol_error": err.Error(), "elapsed_ms": elapsed})
			code = 1
			continue
		}
		var text []string
		images, imageBytes := 0, 0
		for _, ct := range res.Content {
			if tc, ok := ct.(*mcp.TextContent); ok {
				text = append(text, tc.Text)
			}
			if img, ok := ct.(*mcp.ImageContent); ok {
				images++
				imageBytes += len(img.Data)
			}
		}
		printJSON(map[string]any{"tool": c.tool, "is_error": res.IsError, "structured": res.StructuredContent,
			"text": strings.Join(text, "\n"), "elapsed_ms": elapsed, "images": images, "image_bytes": imageBytes})
		if res.IsError {
			code = 1
		}
	}
	return code
}

func printJSON(v any) {
	b, _ := json.Marshal(v)
	fmt.Println(string(b))
}
