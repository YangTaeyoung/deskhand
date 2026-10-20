// apicall calls one method of the Deskhand daemon's local API, or prints its
// events, for the end-to-end scripts.
//
//	apicall [-timeout 60s] <method> [params-json]
//	apicall -events kind[,kind...] [-host H] -for 30s
//
// A call prints the result as one JSON line and exits 0, or prints the error
// as {"error":{...}} and exits 1. -events prints each event as one JSON line
// until the time passes.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/client"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("apicall", flag.ContinueOnError)
	timeout := fs.Duration("timeout", 60*time.Second, "how long a call may take")
	kinds := fs.String("events", "", "comma-separated event kinds to print instead of calling")
	host := fs.String("host", "", "host of the printed events")
	dur := fs.Duration("for", 30*time.Second, "how long -events prints")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ctx := context.Background()
	c, err := client.DialOptions(ctx, "cli", "e2e-apicall", client.Options{
		Start: func(context.Context) error {
			return errors.New("the daemon is not running; start it with deskhand daemon start")
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "apicall:", err)
		return 1
	}
	defer c.Close()
	if *kinds != "" {
		return events(ctx, c, strings.Split(*kinds, ","), *host, *dur)
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		fmt.Fprintln(os.Stderr, "usage: apicall [-timeout 60s] <method> [params-json]")
		return 2
	}
	params := map[string]any{}
	if fs.NArg() == 2 {
		if err := json.Unmarshal([]byte(fs.Arg(1)), &params); err != nil {
			fmt.Fprintln(os.Stderr, "apicall: params are not a JSON object:", err)
			return 2
		}
	}
	cctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	var res json.RawMessage
	if err := c.Call(cctx, fs.Arg(0), params, &res); err != nil {
		var ae *client.Error
		if errors.As(err, &ae) {
			printJSON(map[string]any{"error": map[string]any{"code": ae.Code, "message": ae.Message, "retryable": ae.Retryable, "next_action": ae.NextAction}})
		} else {
			printJSON(map[string]any{"error": map[string]any{"message": err.Error()}})
		}
		return 1
	}
	fmt.Println(string(res))
	return 0
}

func events(ctx context.Context, c *client.Client, kinds []string, host string, dur time.Duration) int {
	ectx, cancel := context.WithTimeout(ctx, dur)
	defer cancel()
	ch, err := c.SubscribeHost(ectx, host, kinds)
	if err != nil {
		fmt.Fprintln(os.Stderr, "apicall:", err)
		return 1
	}
	for {
		select {
		case <-ectx.Done():
			return 0
		case ev, ok := <-ch:
			if !ok {
				return 1
			}
			printJSON(ev.Params)
		}
	}
}

func printJSON(v any) {
	b, _ := json.Marshal(v)
	fmt.Println(string(b))
}
