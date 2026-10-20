// Package ids makes the daemon's identifiers: a prefix followed by a ULID.
package ids

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"time"
)

// Prefixes of the identifiers.
const (
	Request  = "r-"
	Job      = "j-"
	Client   = "c-"
	Host     = "h-"
	Session  = "s-"
	Artifact = "a-"
)

// crockford is the base32 alphabet of ULIDs.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var (
	mu       sync.Mutex
	lastMS   uint64
	lastRand [10]byte
)

// New returns prefix followed by a 26-character ULID. ULIDs made in the
// same millisecond increase.
func New(prefix string) string {
	return prefix + ULID(time.Now())
}

// ULID returns a ULID of time t whose random part follows the one made
// last when t falls in the same millisecond.
func ULID(t time.Time) string {
	ms := uint64(t.UnixMilli())
	mu.Lock()
	var r [10]byte
	if ms == lastMS {
		r = lastRand
		for i := len(r) - 1; i >= 0; i-- {
			r[i]++
			if r[i] != 0 {
				break
			}
		}
	} else {
		_, _ = rand.Read(r[:])
	}
	lastMS, lastRand = ms, r
	mu.Unlock()

	var b [16]byte
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], ms)
	copy(b[:6], ts[2:])
	copy(b[6:], r[:])
	return encode(b)
}

// encode writes 128 bits as 26 base32 digits, the first carrying 3 bits.
func encode(b [16]byte) string {
	hi := binary.BigEndian.Uint64(b[:8])
	lo := binary.BigEndian.Uint64(b[8:])
	var out [26]byte
	for i := 25; i >= 0; i-- {
		out[i] = crockford[lo&31]
		lo = lo>>5 | hi<<59
		hi >>= 5
	}
	return string(out[:])
}
