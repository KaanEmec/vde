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

func TestIncrement(t *testing.T) {
	t.Parallel()
	b := []byte{0x00, 0xff, 0xff}
	if !increment(b) || !slices.Equal(b, []byte{0x01, 0x00, 0x00}) {
		t.Errorf("increment carried to %x, want 010000", b)
	}
	b = []byte{0xff, 0xff}
	if increment(b) || !slices.Equal(b, []byte{0x00, 0x00}) {
		t.Errorf("increment of ffff = %x and no wrap, want 0000 and a wrap", b)
	}
}
