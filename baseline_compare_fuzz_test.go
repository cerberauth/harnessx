package harnessx

import "testing"

func FuzzSimHash(f *testing.F) {
	for _, s := range []string{"", "   ", "hello world", `{"a":1,"b":[1,2,3]}`, "<html><body>Not Found</body></html>", "\xff\xfe"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		h := SimHash(body)
		if h != SimHash(body) {
			t.Error("SimHash is not deterministic")
		}
		if padded := SimHash(append(append([]byte(" "), body...), ' ')); padded != h {
			t.Errorf("surrounding whitespace changed hash: %x != %x", padded, h)
		}
	})
}
