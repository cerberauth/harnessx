package harnessx

import (
	"hash/fnv"
	"math/rand/v2"
	"strings"
	"testing"
	"unicode"
)

// simHashReference is the straightforward SimHash: split into tokens, lower-case
// each, hash it with hash/fnv. SimHash must produce exactly the same values.
func simHashReference(body []byte) uint64 {
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

func TestSimHash_MatchesReference(t *testing.T) {
	bodies := []string{
		"",
		"   \n\t",
		"hello world",
		"Hello WORLD",
		`{"a":1,"b":[1,2,3]}`,
		"<html><body>Not Found: /some/path</body></html>",
		"Ünïcödé ΣΑΣ İstanbul ß ǅ",
		"日本語 テキスト 123",
		"a\xffb c\xc3 \xe2\x82",
		"trailing-token",
	}
	for _, b := range bodies {
		if got, want := SimHash([]byte(b)), simHashReference([]byte(b)); got != want {
			t.Errorf("SimHash(%q) = %x, reference = %x", b, got, want)
		}
	}

	// Random bytes, biased towards letters, separators and multi-byte sequences.
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // G404: deterministic test data
	alphabet := []byte("abcXYZ019 \n<>\"{}:,.-_\xc3\xa9\xe6\x97\xa5\xff")
	for range 2000 {
		b := make([]byte, rng.IntN(64))
		for i := range b {
			b[i] = alphabet[rng.IntN(len(alphabet))]
		}
		if got, want := SimHash(b), simHashReference(b); got != want {
			t.Fatalf("SimHash(%q) = %x, reference = %x", b, got, want)
		}
	}
}

func BenchmarkSimHash(b *testing.B) {
	body := []byte(strings.Repeat(`<li class="item">Users: 1204 and Active sessions: 87</li>`, 2000))
	b.ReportAllocs()
	for b.Loop() {
		SimHash(body)
	}
}
