package harnessx

import (
	"fmt"
	"hash/fnv"
	"math"
	"math/bits"
	"strings"
	"unicode"
)

const titleBaselineDeviation = "Response deviates from baseline"

// CompareStatusAndLength returns a BaselineComparator that flags a change in
// status code, or a body length that moves outside ±tolerance of the
// baseline's length (tolerance 0.1 allows ±10%). A negative tolerance is
// treated as 0, so any length change is flagged. Against an empty baseline
// body only an empty current body is within tolerance.
//
// Length is len(Snapshot.Body), so it is capped by the probe's body size limit.
func CompareStatusAndLength(tolerance float64) BaselineComparator {
	tolerance = math.Max(tolerance, 0)
	return func(baseline, current Snapshot) []Observation {
		if baseline.StatusCode != current.StatusCode {
			return statusDeviation(baseline, current)
		}
		bl, cl := float64(len(baseline.Body)), float64(len(current.Body))
		if math.Abs(cl-bl) > tolerance*bl {
			return []Observation{{
				Title:       titleBaselineDeviation,
				Description: fmt.Sprintf("baseline body was %d bytes, got %d (tolerance ±%.0f%%)", len(baseline.Body), len(current.Body), tolerance*100),
			}}
		}
		return nil
	}
}

// CompareSimilarity returns a BaselineComparator that flags a change in
// status code, or a body whose similarity to the baseline's falls below
// threshold (0..1, 1 meaning identical). Similarity is 1 minus the normalised
// Hamming distance between the SimHash of the two bodies (see SimHash), so
// near-identical pages — such as a catch-all "not found" page that echoes the
// requested path — score high while a genuinely different page scores low.
// A typical threshold is 0.8–0.95.
//
// If a Snapshot carries a non-zero BodyHash it is used instead of hashing Body.
func CompareSimilarity(threshold float64) BaselineComparator {
	return func(baseline, current Snapshot) []Observation {
		if baseline.StatusCode != current.StatusCode {
			return statusDeviation(baseline, current)
		}
		sim := simHashSimilarity(baseline.bodyHash(), current.bodyHash())
		if sim >= threshold {
			return nil
		}
		return []Observation{{
			Title:       titleBaselineDeviation,
			Description: fmt.Sprintf("body similarity to baseline was %.2f, below threshold %.2f", sim, threshold),
		}}
	}
}

func statusDeviation(baseline, current Snapshot) []Observation {
	return []Observation{{
		Title:       titleBaselineDeviation,
		Description: fmt.Sprintf("baseline status was %d, got %d", baseline.StatusCode, current.StatusCode),
	}}
}

// bodyHash returns the recorded BodyHash, falling back to hashing Body.
func (s Snapshot) bodyHash() uint64 {
	if s.BodyHash != 0 {
		return s.BodyHash
	}
	return SimHash(s.Body)
}

// SimHash returns a 64-bit locality-sensitive fingerprint of body: bodies
// sharing most of their words get fingerprints that differ in few bits. It
// tokenises on runs of letters and digits (case-insensitive), so it works on
// HTML and JSON alike. An empty or token-free body hashes to 0.
func SimHash(body []byte) uint64 {
	var weights [64]int
	tokens := 0
	for _, tok := range strings.FieldsFunc(string(body), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		h := fnv.New64a()
		_, _ = h.Write([]byte(strings.ToLower(tok)))
		sum := h.Sum64()
		for i := range weights {
			if sum&(1<<uint(i)) != 0 {
				weights[i]++
			} else {
				weights[i]--
			}
		}
		tokens++
	}
	if tokens == 0 {
		return 0
	}
	var out uint64
	for i, w := range weights {
		if w > 0 {
			out |= 1 << uint(i)
		}
	}
	return out
}

// simHashSimilarity maps two SimHashes to [0, 1]: 1 for equal hashes, 0 when
// every bit differs.
func simHashSimilarity(a, b uint64) float64 {
	return 1 - float64(bits.OnesCount64(a^b))/64
}
