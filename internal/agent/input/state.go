package input

import (
	"slices"
)

// Pressed is a key or mouse button the injector holds down: k "key" with
// sc (and ext) or vk, or k "button" with b.
type Pressed struct {
	K   string `json:"k"`
	SC  uint16 `json:"sc,omitempty"`
	Ext bool   `json:"ext,omitempty"`
	VK  uint16 `json:"vk,omitempty"`
	B   string `json:"b,omitempty"`
}

// keyID identifies a key or button: a mouse button by name, a key by scan
// code and E0 prefix or by virtual key, and a UTF-16 unit of a text.
type keyID struct {
	button  string
	sc      uint16
	ext     bool
	vk      uint16
	unicode bool
}

// heldInput is a held key or button: the input stream that pressed it and
// the input that did. A stale one is released before the next injection.
type heldInput struct {
	owner uint64
	down  raw
	stale bool
}

// buttonOrder orders the mouse buttons in Pressed lists.
var buttonOrder = map[string]int{"left": 0, "right": 1, "middle": 2, "x1": 3, "x2": 4}

// compareKeys orders buttons first, then keys by scan code, then keys by
// virtual key.
func compareKeys(a, b keyID) int {
	rank := func(k keyID) (int, int) {
		switch {
		case k.button != "":
			return 0, buttonOrder[k.button]
		case k.vk == 0:
			n := int(k.sc) << 1
			if k.ext {
				n++
			}
			return 1, n
		}
		return 2, int(k.vk)
	}
	ga, na := rank(a)
	gb, nb := rank(b)
	if ga != gb {
		return ga - gb
	}
	return na - nb
}

func (k keyID) pressed() Pressed {
	if k.button != "" {
		return Pressed{K: "button", B: k.button}
	}
	if k.vk != 0 {
		return Pressed{K: "key", VK: k.vk}
	}
	return Pressed{K: "key", SC: k.sc, Ext: k.ext}
}

// identify returns the key or button r presses or releases, and whether
// it presses it; ok is false for a move or a wheel.
func identify(r raw) (id keyID, down, ok bool) {
	if r.mouse {
		switch {
		case r.flags&mouseeventfLeftDown != 0:
			return keyID{button: "left"}, true, true
		case r.flags&mouseeventfLeftUp != 0:
			return keyID{button: "left"}, false, true
		case r.flags&mouseeventfRightDown != 0:
			return keyID{button: "right"}, true, true
		case r.flags&mouseeventfRightUp != 0:
			return keyID{button: "right"}, false, true
		case r.flags&mouseeventfMiddleDown != 0:
			return keyID{button: "middle"}, true, true
		case r.flags&mouseeventfMiddleUp != 0:
			return keyID{button: "middle"}, false, true
		case r.flags&(mouseeventfXDown|mouseeventfXUp) != 0:
			name := "x1"
			if r.data == xbutton2 {
				name = "x2"
			}
			return keyID{button: name}, r.flags&mouseeventfXDown != 0, true
		}
		return keyID{}, false, false
	}
	down = r.flags&keyeventfKeyUp == 0
	switch {
	case r.flags&keyeventfUnicode != 0:
		return keyID{unicode: true, sc: r.scan}, down, true
	case r.flags&keyeventfScancode != 0:
		return keyID{sc: r.scan, ext: r.flags&keyeventfExtendedKey != 0}, down, true
	}
	return keyID{vk: r.vk}, down, true
}

// releaseOf is the input that releases what down pressed.
func releaseOf(down raw) raw {
	if down.mouse {
		down.flags <<= 1
		return down
	}
	down.flags |= keyeventfKeyUp
	return down
}

// track records, with mu held, the key or button r pressed for owner or
// released. Text units are not held.
func (j *Injector) track(owner uint64, r raw) {
	id, down, ok := identify(r)
	if !ok || id.unicode {
		return
	}
	if down {
		j.held[id] = &heldInput{owner: owner, down: r}
	} else {
		delete(j.held, id)
	}
}

// unwind releases, with mu held, the keys and buttons sent pressed that
// rest, the inputs not injected after them, would have released.
func (j *Injector) unwind(owner uint64, sent, rest []raw) {
	pending := map[keyID]raw{}
	var order []keyID
	for _, r := range sent {
		id, down, ok := identify(r)
		switch {
		case !ok:
		case down:
			if _, seen := pending[id]; !seen {
				order = append(order, id)
			}
			pending[id] = r
		default:
			delete(pending, id)
		}
	}
	var ups []raw
	for _, id := range order {
		down, ok := pending[id]
		if !ok {
			continue
		}
		delete(pending, id)
		for _, r := range rest {
			if rid, rdown, rok := identify(r); rok && !rdown && rid == id {
				ups = append(ups, releaseOf(down))
				break
			}
		}
	}
	if len(ups) == 0 {
		return
	}
	n, _ := j.send(ups)
	for _, r := range ups[:n] {
		j.track(owner, r)
	}
	for _, r := range ups[n:] {
		if id, _, ok := identify(r); ok {
			if h := j.held[id]; h != nil {
				h.stale = true
			}
		}
	}
}

// heldIDs returns, with mu held, the held keys and buttons match accepts,
// in compareKeys order.
func (j *Injector) heldIDs(match func(*heldInput) bool) []keyID {
	var ids []keyID
	for id, h := range j.held {
		if match(h) {
			ids = append(ids, id)
		}
	}
	slices.SortFunc(ids, compareKeys)
	return ids
}

// releaseLocked releases, with mu held, the held keys and buttons match
// accepts and returns those it released. The ones Windows did not take
// stay held and stale, and the error says why.
func (j *Injector) releaseLocked(match func(*heldInput) bool) ([]Pressed, error) {
	ids := j.heldIDs(match)
	if len(ids) == 0 {
		return nil, nil
	}
	ups := make([]raw, len(ids))
	for i, id := range ids {
		ups[i] = releaseOf(j.held[id].down)
	}
	n, err := j.send(ups)
	released := make([]Pressed, 0, n)
	for i, id := range ids {
		if i < n {
			delete(j.held, id)
			released = append(released, id.pressed())
		} else {
			j.held[id].stale = true
		}
	}
	if n < len(ids) {
		return released, j.shortSend(n, len(ids), err)
	}
	return released, nil
}

// releaseStaleLocked releases, with mu held, the keys and buttons an
// earlier release could not.
func (j *Injector) releaseStaleLocked() {
	if _, err := j.releaseLocked(func(h *heldInput) bool { return h.stale }); err != nil {
		j.opt.Logger.Printf("input: releasing input left held: %v", err)
	}
}

// ReleaseAll releases every held key and button and returns those it
// released.
func (j *Injector) ReleaseAll() ([]Pressed, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.releaseLocked(func(*heldInput) bool { return true })
}

// releaseOwner releases the keys and buttons owner holds.
func (j *Injector) releaseOwner(owner uint64) ([]Pressed, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.releaseLocked(func(h *heldInput) bool { return h.owner == owner })
}

// Pressed returns the held keys and buttons: buttons first, then keys by
// scan code, then keys by virtual key.
func (j *Injector) Pressed() []Pressed {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := []Pressed{}
	for _, id := range j.heldIDs(func(*heldInput) bool { return true }) {
		out = append(out, id.pressed())
	}
	return out
}
