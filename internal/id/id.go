// Package id generates prefixed ULIDs such as task_01J9ZK3M5Q8R2T4V6W8X0Y2Z4A.
// ULIDs sort by creation time, so IDs double as a stable pagination order.
// Within a process they are monotonic: IDs made in the same millisecond
// still sort in the order they were made.
package id

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"time"
)

const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ" // Crockford base32

var ids generator

// New returns prefix + "_" + a ULID for the current time.
func New(prefix string) string { return prefix + "_" + ids.ulid(time.Now()) }

type generator struct {
	mu   sync.Mutex
	last [16]byte
}

// ulid returns a ULID for t, or, if that would not sort after the previous
// one (the same millisecond, or the clock went back), the previous one
// plus one.
func (g *generator) ulid(t time.Time) string {
	var b [16]byte
	ms := uint64(t.UnixMilli())
	b[0], b[1] = byte(ms>>40), byte(ms>>32)
	binary.BigEndian.PutUint32(b[2:6], uint32(ms))

	g.mu.Lock()
	defer g.mu.Unlock()
	if string(b[:6]) <= string(g.last[:6]) {
		b = g.last
		for i := 15; i >= 6; i-- {
			b[i]++
			if b[i] != 0 {
				break
			}
		}
	} else {
		_, _ = rand.Read(b[6:])
	}
	g.last = b
	return encode(b)
}

// encode writes 128 bits as 26 base32 characters; the first carries 3 bits.
func encode(b [16]byte) string {
	var out [26]byte
	hi, lo := binary.BigEndian.Uint64(b[:8]), binary.BigEndian.Uint64(b[8:])
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = alphabet[lo&31]
		lo = lo>>5 | hi<<59
		hi >>= 5
	}
	return string(out[:])
}
