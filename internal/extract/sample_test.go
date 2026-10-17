package extract

import (
	"math/rand/v2"
	"testing"
)

func TestSampled(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for _, rate := range []float64{0, 0.05, 0.5, 1} {
		hits := 0
		const n = 10_000
		for range n {
			key := make([]byte, 16)
			for i := range key {
				key[i] = byte(r.UintN(256))
			}
			got := Sampled(key, rate)
			if got != Sampled(key, rate) {
				t.Fatal("the same key gave two answers")
			}
			if got {
				hits++
			}
		}
		share := float64(hits) / n
		if share < rate-0.01 || share > rate+0.01 {
			t.Errorf("rate %g: share %g, want within 1 %%", rate, share)
		}
		if (rate == 0 && hits != 0) || (rate == 1 && hits != n) {
			t.Errorf("rate %g: %d of %d sampled", rate, hits, n)
		}
	}
	if Sampled([]byte("j_1"), -1) || !Sampled(nil, 1) {
		t.Error("a negative rate sampled, or a rate of 1 did not")
	}
}
