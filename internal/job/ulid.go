package job

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"time"
)

// crockford is the base32 alphabet of a ULID: digits and capitals without I, L, O and U.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// ulidState is the last ULID's time and random part, so that IDs of one millisecond
// increase.
var ulidState struct {
	sync.Mutex
	ms     uint64
	random [10]byte
}

// NewULID returns a ULID: 26 characters of Crockford base32 encoding a 48-bit Unix time in
// milliseconds and 80 random bits from crypto/rand. Within one millisecond (or when the
// clock steps back) the random part of the previous ID is incremented instead, so the IDs
// of this process sort in the order they were made.
func NewULID() string {
	now := uint64(time.Now().UnixMilli())
	ulidState.Lock()
	if now > ulidState.ms {
		ulidState.ms = now
		_, _ = rand.Read(ulidState.random[:]) // never fails
	} else if !increment(ulidState.random[:]) {
		ulidState.ms++ // 2^80 IDs in one millisecond: carry into the time
	}
	ms, random := ulidState.ms, ulidState.random
	ulidState.Unlock()
	return encodeULID(ms, random)
}

// increment adds one to the big-endian number b and reports false when it wrapped to zero.
func increment(b []byte) bool {
	for i := len(b) - 1; i >= 0; i-- {
		if b[i]++; b[i] != 0 {
			return true
		}
	}
	return false
}

// encodeULID writes the 128 bits of ms (48) and random (80) as 26 base32 characters, five
// bits each from the right; the first character holds the top three bits.
func encodeULID(ms uint64, random [10]byte) string {
	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], ms<<16)
	copy(b[6:], random[:])
	hi, lo := binary.BigEndian.Uint64(b[:8]), binary.BigEndian.Uint64(b[8:])
	var out [26]byte
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = crockford[lo&31]
		lo = lo>>5 | hi<<59
		hi >>= 5
	}
	return string(out[:])
}
