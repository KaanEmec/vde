package job

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestEncodeULID: the vectors are what github.com/oklog/ulid/v2, which NewULID replaced,
// printed for the same time and random part.
func TestEncodeULID(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		ms     uint64
		random [10]byte
		want   string
	}{
		{0, [10]byte{}, "00000000000000000000000000"},
		{1, [10]byte{9: 1}, "00000000010000000000000001"},
		{1729350000123, [10]byte{0xde, 0xad, 0xbe, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab}, "01JAJMNQFVVTPVXVR14D2PF2DB"},
		{1<<48 - 1, [10]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, "7ZZZZZZZZZZZZZZZZZZZZZZZZZ"},
	} {
		if got := encodeULID(tt.ms, tt.random); got != tt.want {
			t.Errorf("encodeULID(%d, %x) = %s, want %s", tt.ms, tt.random, got, tt.want)
		}
	}
}

// TestNewULID: 26 Crockford characters whose time is now, increasing in the order made,
// also within one millisecond, and unique across goroutines.
func TestNewULID(t *testing.T) {
	t.Parallel()
	before := time.Now().UnixMilli()
	id := NewULID()
	after := time.Now().UnixMilli()
	if len(id) != 26 || strings.Trim(id, crockford) != "" || id[0] > '7' {
		t.Fatalf("NewULID() = %q, want 26 Crockford base32 characters", id)
	}
	var ms int64
	for _, c := range id[:10] {
		ms = ms<<5 | int64(strings.IndexRune(crockford, c))
	}
	if ms < before || ms > after {
		t.Errorf("time of %s = %d, want between %d and %d", id, ms, before, after)
	}

	const goroutines, each = 8, 2000
	ids := make([][]string, goroutines)
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Go(func() {
			for range each {
				ids[g] = append(ids[g], NewULID())
			}
		})
	}
	wg.Wait()
	seen := map[string]bool{}
	for g, list := range ids {
		if !slices.IsSorted(list) {
			t.Errorf("goroutine %d: IDs not increasing", g)
		}
		for _, id := range list {
			if seen[id] {
				t.Fatalf("duplicate ID %s", id)
			}
			seen[id] = true
		}
	}
}

// TestNewULIDStep: IDs made within one millisecond increase, but not by one, so the next
// ID cannot be guessed from the last.
func TestNewULIDStep(t *testing.T) {
	t.Parallel()
	consecutive := 0
	const n = 1000
	for range n {
		a, b := NewULID(), NewULID()
		if a >= b {
			t.Fatalf("NewULID() = %s then %s, want increasing", a, b)
		}
		if a[:10] == b[:10] && decodeRandom(t, b)-decodeRandom(t, a) == 1 {
			consecutive++
		}
	}
	if consecutive > 1 { // a step of exactly one has probability 2^-32
		t.Errorf("%d of %d same-millisecond pairs differ by one, want random steps", consecutive, n)
	}
}

// decodeRandom returns the low 64 bits of the random part of id (its last 13 characters
// hold 65 bits; the top one is dropped).
func decodeRandom(t *testing.T, id string) uint64 {
	t.Helper()
	var v uint64
	for _, c := range id[13:] {
		v = v<<5 | uint64(strings.IndexRune(crockford, c))
	}
	return v
}

func TestAdd(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		b        []byte
		step     uint64
		want     []byte
		overflow bool
	}{
		{"one", []byte{0x00, 0x00, 0x00}, 1, []byte{0x00, 0x00, 0x01}, false},
		{"carry", []byte{0x00, 0xff, 0xff}, 1, []byte{0x01, 0x00, 0x00}, false},
		{"max step", []byte{0, 0, 0, 0, 0, 0}, 1 << 32, []byte{0, 1, 0, 0, 0, 0}, false},
		{"multi-byte carry", []byte{0x00, 0x00, 0xff, 0xff, 0xff, 0xff}, 0x0000_0001_0000_0001, []byte{0x00, 0x02, 0x00, 0x00, 0x00, 0x00}, false},
		{"wrap", []byte{0xff, 0xff}, 1, []byte{0x00, 0x00}, true},
		{"wrap with remainder", []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xf0}, 0x20, []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x10}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := slices.Clone(tt.b)
			if ok := add(b, tt.step); ok == tt.overflow || !slices.Equal(b, tt.want) {
				t.Errorf("add(%x, %#x) = %x, no overflow %t; want %x, no overflow %t", tt.b, tt.step, b, ok, tt.want, !tt.overflow)
			}
		})
	}
}

func TestRandomStep(t *testing.T) {
	t.Parallel()
	for range 1000 {
		if s := randomStep(); s < 1 || s > 1<<32 {
			t.Fatalf("randomStep() = %d, want 1 to 2^32", s)
		}
	}
}
