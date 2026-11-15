package cvss_test

import (
	"bufio"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/cerberauth/harnessx/cvss"
)

func TestScore_MatchesFIRSTCalculator(t *testing.T) {
	f, err := os.Open("testdata/first_calculator.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	n := 0
	for scanner.Scan() {
		vector, expected, ok := strings.Cut(scanner.Text(), "\t")
		if !ok {
			t.Fatalf("malformed line %q", scanner.Text())
		}
		want, err := strconv.ParseFloat(expected, 64)
		if err != nil {
			t.Fatal(err)
		}

		got, err := cvss.Score(vector)

		if err != nil {
			t.Fatalf("%s: %v", vector, err)
		}
		if got != want {
			t.Errorf("%s: got %v, want %v", vector, got, want)
		}
		n++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if n < 3000 {
		t.Fatalf("only %d reference vectors", n)
	}
}

func TestScore(t *testing.T) {
	testCases := []struct {
		vector string
		score  float64
	}{
		{"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N", 9.3},
		{"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:N/VI:N/VA:N/SC:N/SI:N/SA:N", 0},
		{"CVSS:4.0/AV:N/AC:L/AT:N/PR:L/UI:N/VC:H/VI:H/VA:N/SC:N/SI:N/SA:N", 8.6},
		{"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:L/VI:L/VA:N/SC:N/SI:N/SA:N", 6.9},
	}
	for _, tc := range testCases {
		t.Run(tc.vector, func(t *testing.T) {
			got, err := cvss.Score(tc.vector)

			if err != nil {
				t.Fatal(err)
			}
			if got != tc.score {
				t.Errorf("got %v, want %v", got, tc.score)
			}
		})
	}
}

func TestParse_Invalid(t *testing.T) {
	for _, vector := range []string{
		"",
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H",
		"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N",
		"CVSS:4.0/AV:X/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N",
		"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N/ZZ:A",
		"CVSS:4.0/AV:N/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N",
	} {
		if _, err := cvss.Score(vector); !errors.Is(err, cvss.ErrInvalidVector) {
			t.Errorf("%q: got %v, want ErrInvalidVector", vector, err)
		}
	}
}

func TestSeverity(t *testing.T) {
	for score, want := range map[float64]string{0: "None", 2.1: "Low", 5.3: "Medium", 8.7: "High", 9.3: "Critical"} {
		if got := cvss.Severity(score); got != want {
			t.Errorf("Severity(%v) = %q, want %q", score, got, want)
		}
	}
}
