//go:build windows

package diag

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

var (
	wevtapi                      = windows.NewLazySystemDLL("wevtapi.dll")
	procEvtQuery                 = wevtapi.NewProc("EvtQuery")
	procEvtNext                  = wevtapi.NewProc("EvtNext")
	procEvtRender                = wevtapi.NewProc("EvtRender")
	procEvtClose                 = wevtapi.NewProc("EvtClose")
	procEvtOpenPublisherMetadata = wevtapi.NewProc("EvtOpenPublisherMetadata")
	procEvtFormatMessage         = wevtapi.NewProc("EvtFormatMessage")
)

const (
	evtQueryChannelPath      = 0x1
	evtQueryReverseDirection = 0x200
	evtRenderEventXML        = 1
	evtFormatMessageEvent    = 1
	// evtNextTimeout bounds one EvtNext call in milliseconds.
	evtNextTimeout = 30000
	// evtBatch is the most events one EvtNext call returns.
	evtBatch = 64

	errorNoMoreItems              syscall.Errno = 259
	errorInsufficientBuffer       syscall.Errno = 122
	errorEvtUnresolvedValueInsert syscall.Errno = 15029
)

// readEvents returns the records of q's log that match it, newest first:
// at most q.Max, with Truncated set when more match. A record whose XML
// cannot be read is left out; one whose provider has no message for it has
// its data values as message.
func readEvents(ctx context.Context, q wire.DiagEventsQuery) (Events, error) {
	xpath, err := eventXPath(q)
	if err != nil {
		return Events{}, err
	}
	path, _ := windows.UTF16PtrFromString(q.Log)
	query, err := windows.UTF16PtrFromString(xpath)
	if err != nil {
		return Events{}, &InvalidError{Text: "the provider name cannot hold a NUL character; pass the provider name as the event log shows it"}
	}
	rs, _, e := procEvtQuery.Call(0, uintptr(unsafe.Pointer(path)), uintptr(unsafe.Pointer(query)), evtQueryChannelPath|evtQueryReverseDirection)
	if rs == 0 {
		return Events{}, fmt.Errorf("the %s event log could not be read (%v); retry", q.Log, e)
	}
	defer evtClose(rs)
	pubs := publishers{}
	defer pubs.close()
	want := q.Max + 1
	out := Events{Events: []Event{}}
	handles := make([]uintptr, evtBatch)
	render := make([]uint16, 16<<10)
	for len(out.Events) < want {
		if err := ctx.Err(); err != nil {
			return Events{}, err
		}
		var n uint32
		r, _, e := procEvtNext.Call(rs, uintptr(min(evtBatch, want-len(out.Events))), uintptr(unsafe.Pointer(&handles[0])), evtNextTimeout, 0, uintptr(unsafe.Pointer(&n)))
		if r == 0 {
			if errors.Is(e, errorNoMoreItems) {
				break
			}
			return Events{}, fmt.Errorf("the %s event log could not be read (%v); retry", q.Log, e)
		}
		for _, h := range handles[:n] {
			if len(out.Events) < want {
				if ev, ok := readEvent(h, &render, pubs); ok {
					out.Events = append(out.Events, ev)
				}
			}
			evtClose(h)
		}
	}
	if len(out.Events) > q.Max {
		out.Events, out.Truncated = out.Events[:q.Max], true
	}
	return out, nil
}

// readEvent renders the event h with the message its publisher formats.
func readEvent(h uintptr, render *[]uint16, pubs publishers) (Event, bool) {
	xml, err := renderXML(h, render)
	if err != nil {
		return Event{}, false
	}
	raw, err := parseEventXML(xml)
	if err != nil {
		return Event{}, false
	}
	return newEvent(raw, eventMessage(pubs.format(raw.Provider, h), raw.Data)), true
}

// renderXML renders the event h as XML, growing buf as needed.
func renderXML(h uintptr, buf *[]uint16) ([]byte, error) {
	for range 4 {
		var used, props uint32
		r, _, e := procEvtRender.Call(0, h, evtRenderEventXML, uintptr(2*len(*buf)), uintptr(unsafe.Pointer(&(*buf)[0])),
			uintptr(unsafe.Pointer(&used)), uintptr(unsafe.Pointer(&props)))
		if r != 0 {
			return []byte(windows.UTF16ToString((*buf)[:used/2])), nil
		}
		if !errors.Is(e, errorInsufficientBuffer) {
			return nil, e
		}
		*buf = make([]uint16, used/2+1)
	}
	return nil, errors.New("the event XML kept growing")
}

// publishers caches the publisher metadata handles by provider name; 0
// marks a provider whose metadata cannot be opened.
type publishers map[string]uintptr

// format returns the message the provider's metadata formats for the event
// h, "" when there is none.
func (p publishers) format(provider string, h uintptr) string {
	if provider == "" {
		return ""
	}
	pub, ok := p[provider]
	if !ok {
		name, err := windows.UTF16PtrFromString(provider)
		if err == nil {
			pub, _, _ = procEvtOpenPublisherMetadata.Call(0, uintptr(unsafe.Pointer(name)), 0, 0, 0)
		}
		p[provider] = pub
	}
	if pub == 0 {
		return ""
	}
	buf := make([]uint16, 2048)
	for range 3 {
		var used uint32
		r, _, e := procEvtFormatMessage.Call(pub, h, 0, 0, 0, evtFormatMessageEvent, uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&used)))
		switch {
		case r != 0, errors.Is(e, errorEvtUnresolvedValueInsert):
			return windows.UTF16ToString(buf[:min(int(used), len(buf))])
		case errors.Is(e, errorInsufficientBuffer) && int(used) > len(buf):
			buf = make([]uint16, used)
		default:
			return ""
		}
	}
	return ""
}

// close closes the publisher metadata handles.
func (p publishers) close() {
	for _, h := range p {
		if h != 0 {
			evtClose(h)
		}
	}
}

func evtClose(h uintptr) { procEvtClose.Call(h) }
