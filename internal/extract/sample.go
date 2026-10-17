package extract

import (
	"hash/fnv"
	"math"
)

// Sampled reports whether a job whose grounding check alone would accept it goes to the
// judge anyway under judge policy auto (ADR 0009): when the FNV-1a 64 hash of key,
// mapped to [0, 1), is below rate. The key is the job id in the service and the
// document bytes in the CLI and the local evaluation, so reruns sample the same jobs.
// A rate of 0 never samples and a rate of 1 always does.
func Sampled(key []byte, rate float64) bool {
	if !(rate > 0) { // NaN, zero and negative rates never sample
		return false
	}
	if rate >= 1 {
		return true
	}
	h := fnv.New64a()
	_, _ = h.Write(key) // never fails
	return float64(h.Sum64())/math.Exp2(64) < rate
}
