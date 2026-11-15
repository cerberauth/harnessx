// Package cvss computes CVSS v4.0 scores from vector strings, following the
// FIRST CVSS v4.0 calculator (https://www.first.org/cvss/calculator/4.0).
//
// Copyright FIRST, Red Hat, and contributors
// SPDX-License-Identifier: BSD-2-Clause
package cvss

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

const Prefix = "CVSS:4.0"

var ErrInvalidVector = errors.New("invalid CVSS v4.0 vector")

var baseMetrics = []string{"AV", "AC", "AT", "PR", "UI", "VC", "VI", "VA", "SC", "SI", "SA"}

const (
	impactValues           = "HLN"
	requirementValues      = "XHML"
	modifiedImpactValues   = "XHLN"
	modifiedSubImpactValue = "XSHLN"
)

var allowedValues = map[string]string{
	"AV": "NALP", "AC": "LH", "AT": "NP", "PR": "NLH", "UI": "NPA",
	"VC": impactValues, "VI": impactValues, "VA": impactValues, "SC": impactValues, "SI": impactValues, "SA": impactValues,
	"E":  "XAPU",
	"CR": requirementValues, "IR": requirementValues, "AR": requirementValues,
	"MAV": "XNALP", "MAC": "XLH", "MAT": "XNP", "MPR": "XNLH", "MUI": "XNPA",
	"MVC": modifiedImpactValues, "MVI": modifiedImpactValues, "MVA": modifiedImpactValues, "MSC": modifiedImpactValues,
	"MSI": modifiedSubImpactValue, "MSA": modifiedSubImpactValue,
	"S": "XNP", "AU": "XNY", "R": "XAUI", "V": "XDC", "RE": "XLMH", "U": "X",
}

type Vector map[string]string

func Parse(vector string) (Vector, error) {
	parts := strings.Split(strings.TrimSpace(vector), "/")
	if len(parts) < 2 || parts[0] != Prefix {
		return nil, fmt.Errorf("%w: must start with %s", ErrInvalidVector, Prefix)
	}
	v := Vector{}
	for _, part := range parts[1:] {
		metric, value, ok := strings.Cut(part, ":")
		if !ok || value == "" {
			return nil, fmt.Errorf("%w: malformed metric %q", ErrInvalidVector, part)
		}
		allowed, known := allowedValues[metric]
		if !known {
			return nil, fmt.Errorf("%w: unknown metric %q", ErrInvalidVector, metric)
		}
		if _, dup := v[metric]; dup {
			return nil, fmt.Errorf("%w: duplicate metric %q", ErrInvalidVector, metric)
		}
		if metric == "U" {
			allowed = ""
		}
		if metric != "U" && (len(value) != 1 || !strings.Contains(allowed, value)) {
			return nil, fmt.Errorf("%w: invalid value %q for %s", ErrInvalidVector, value, metric)
		}
		v[metric] = value
	}
	for _, metric := range baseMetrics {
		if _, ok := v[metric]; !ok {
			return nil, fmt.Errorf("%w: missing base metric %s", ErrInvalidVector, metric)
		}
	}
	return v, nil
}

func (v Vector) m(metric string) string {
	selected, ok := v[metric]
	if !ok {
		selected = "X"
	}
	switch {
	case metric == "E" && selected == "X":
		return "A"
	case (metric == "CR" || metric == "IR" || metric == "AR") && selected == "X":
		return "H"
	}
	if modified, ok := v["M"+metric]; ok && modified != "X" {
		return modified
	}
	return selected
}

func (v Vector) macroVector() string {
	eq1 := "2"
	switch {
	case v.m("AV") == "N" && v.m("PR") == "N" && v.m("UI") == "N":
		eq1 = "0"
	case (v.m("AV") == "N" || v.m("PR") == "N" || v.m("UI") == "N") && v.m("AV") != "P":
		eq1 = "1"
	}

	eq2 := "1"
	if v.m("AC") == "L" && v.m("AT") == "N" {
		eq2 = "0"
	}

	eq3 := "2"
	switch {
	case v.m("VC") == "H" && v.m("VI") == "H":
		eq3 = "0"
	case v.m("VC") == "H" || v.m("VI") == "H" || v.m("VA") == "H":
		eq3 = "1"
	}

	eq4 := "2"
	switch {
	case v.m("MSI") == "S" || v.m("MSA") == "S":
		eq4 = "0"
	case v.m("SC") == "H" || v.m("SI") == "H" || v.m("SA") == "H":
		eq4 = "1"
	}

	eq5 := "2"
	switch v.m("E") {
	case "A":
		eq5 = "0"
	case "P":
		eq5 = "1"
	}

	eq6 := "1"
	if (v.m("CR") == "H" && v.m("VC") == "H") || (v.m("IR") == "H" && v.m("VI") == "H") || (v.m("AR") == "H" && v.m("VA") == "H") {
		eq6 = "0"
	}

	return eq1 + eq2 + eq3 + eq4 + eq5 + eq6
}

var levels = map[string]map[string]float64{
	"AV": {"N": 0.0, "A": 0.1, "L": 0.2, "P": 0.3},
	"PR": {"N": 0.0, "L": 0.1, "H": 0.2},
	"UI": {"N": 0.0, "P": 0.1, "A": 0.2},
	"AC": {"L": 0.0, "H": 0.1},
	"AT": {"N": 0.0, "P": 0.1},
	"VC": {"H": 0.0, "L": 0.1, "N": 0.2},
	"VI": {"H": 0.0, "L": 0.1, "N": 0.2},
	"VA": {"H": 0.0, "L": 0.1, "N": 0.2},
	"SC": {"H": 0.1, "L": 0.2, "N": 0.3},
	"SI": {"S": 0.0, "H": 0.1, "L": 0.2, "N": 0.3},
	"SA": {"S": 0.0, "H": 0.1, "L": 0.2, "N": 0.3},
	"CR": {"H": 0.0, "M": 0.1, "L": 0.2},
	"IR": {"H": 0.0, "M": 0.1, "L": 0.2},
	"AR": {"H": 0.0, "M": 0.1, "L": 0.2},
}

var maxComposed = map[string]map[byte][]string{
	"eq1": {
		'0': {"AV:N/PR:N/UI:N/"},
		'1': {"AV:A/PR:N/UI:N/", "AV:N/PR:L/UI:N/", "AV:N/PR:N/UI:P/"},
		'2': {"AV:P/PR:N/UI:N/", "AV:A/PR:L/UI:P/"},
	},
	"eq2": {
		'0': {"AC:L/AT:N/"},
		'1': {"AC:H/AT:N/", "AC:L/AT:P/"},
	},
	"eq4": {
		'0': {"SC:H/SI:S/SA:S/"},
		'1': {"SC:H/SI:H/SA:H/"},
		'2': {"SC:L/SI:L/SA:L/"},
	},
	"eq5": {
		'0': {"E:A/"},
		'1': {"E:P/"},
		'2': {"E:U/"},
	},
}

var maxComposedEQ3 = map[byte]map[byte][]string{
	'0': {
		'0': {"VC:H/VI:H/VA:H/CR:H/IR:H/AR:H/"},
		'1': {"VC:H/VI:H/VA:L/CR:M/IR:M/AR:H/", "VC:H/VI:H/VA:H/CR:M/IR:M/AR:M/"},
	},
	'1': {
		'0': {"VC:L/VI:H/VA:H/CR:H/IR:H/AR:H/", "VC:H/VI:L/VA:H/CR:H/IR:H/AR:H/"},
		'1': {"VC:L/VI:H/VA:L/CR:H/IR:M/AR:H/", "VC:L/VI:H/VA:H/CR:H/IR:M/AR:M/", "VC:H/VI:L/VA:H/CR:M/IR:H/AR:M/", "VC:H/VI:L/VA:L/CR:M/IR:H/AR:H/", "VC:L/VI:L/VA:H/CR:H/IR:H/AR:M/"},
	},
	'2': {
		'1': {"VC:L/VI:L/VA:L/CR:H/IR:H/AR:H/"},
	},
}

var maxSeverityEQ1 = map[int]float64{0: 1, 1: 4, 2: 5}
var maxSeverityEQ2 = map[int]float64{0: 1, 1: 2}
var maxSeverityEQ3EQ6 = map[int]map[int]float64{0: {0: 7, 1: 6}, 1: {0: 8, 1: 8}, 2: {1: 10}}
var maxSeverityEQ4 = map[int]float64{0: 6, 1: 5, 2: 4}

func extractValueMetric(metric, vector string) string {
	extracted := vector[strings.Index(vector, metric)+len(metric)+1:]
	if i := strings.Index(extracted, "/"); i > 0 {
		return extracted[:i]
	}
	return extracted
}

func lookupScore(macro string) float64 {
	if score, ok := lookup[macro]; ok {
		return score
	}
	return math.NaN()
}

func macroString(eqs ...int) string {
	var b strings.Builder
	for _, eq := range eqs {
		fmt.Fprint(&b, eq)
	}
	return b.String()
}

var distanceMetrics = []string{"AV", "PR", "UI", "AC", "AT", "VC", "VI", "VA", "SC", "SI", "SA", "CR", "IR", "AR"}

func (v Vector) Score() float64 {
	noImpact := true
	for _, metric := range []string{"VC", "VI", "VA", "SC", "SI", "SA"} {
		if v.m(metric) != "N" {
			noImpact = false
			break
		}
	}
	if noImpact {
		return 0
	}

	macro := v.macroVector()
	value := lookup[macro]

	eq1 := int(macro[0] - '0')
	eq2 := int(macro[1] - '0')
	eq3 := int(macro[2] - '0')
	eq4 := int(macro[3] - '0')
	eq5 := int(macro[4] - '0')
	eq6 := int(macro[5] - '0')

	scoreEQ1NextLower := lookupScore(macroString(eq1+1, eq2, eq3, eq4, eq5, eq6))
	scoreEQ2NextLower := lookupScore(macroString(eq1, eq2+1, eq3, eq4, eq5, eq6))
	var scoreEQ3EQ6NextLower float64
	switch {
	case eq3 == 1 && eq6 == 1, eq3 == 0 && eq6 == 1:
		scoreEQ3EQ6NextLower = lookupScore(macroString(eq1, eq2, eq3+1, eq4, eq5, eq6))
	case eq3 == 1 && eq6 == 0:
		scoreEQ3EQ6NextLower = lookupScore(macroString(eq1, eq2, eq3, eq4, eq5, eq6+1))
	case eq3 == 0 && eq6 == 0:
		left := lookupScore(macroString(eq1, eq2, eq3, eq4, eq5, eq6+1))
		right := lookupScore(macroString(eq1, eq2, eq3+1, eq4, eq5, eq6))
		if left > right {
			scoreEQ3EQ6NextLower = left
		} else {
			scoreEQ3EQ6NextLower = right
		}
	default:
		scoreEQ3EQ6NextLower = lookupScore(macroString(eq1, eq2, eq3+1, eq4, eq5, eq6+1))
	}
	scoreEQ4NextLower := lookupScore(macroString(eq1, eq2, eq3, eq4+1, eq5, eq6))
	scoreEQ5NextLower := lookupScore(macroString(eq1, eq2, eq3, eq4, eq5+1, eq6))

	var maxVectors []string
	for _, eq1Max := range maxComposed["eq1"][macro[0]] {
		for _, eq2Max := range maxComposed["eq2"][macro[1]] {
			for _, eq3EQ6Max := range maxComposedEQ3[macro[2]][macro[5]] {
				for _, eq4Max := range maxComposed["eq4"][macro[3]] {
					for _, eq5Max := range maxComposed["eq5"][macro[4]] {
						maxVectors = append(maxVectors, eq1Max+eq2Max+eq3EQ6Max+eq4Max+eq5Max)
					}
				}
			}
		}
	}

	distance := map[string]float64{}
	for _, maxVector := range maxVectors {
		negative := false
		for _, metric := range distanceMetrics {
			distance[metric] = levels[metric][v.m(metric)] - levels[metric][extractValueMetric(metric, maxVector)]
			if distance[metric] < 0 {
				negative = true
			}
		}
		if !negative {
			break
		}
	}

	currentEQ1 := distance["AV"] + distance["PR"] + distance["UI"]
	currentEQ2 := distance["AC"] + distance["AT"]
	currentEQ3EQ6 := distance["VC"] + distance["VI"] + distance["VA"] + distance["CR"] + distance["IR"] + distance["AR"]
	currentEQ4 := distance["SC"] + distance["SI"] + distance["SA"]

	const step = 0.1
	availableEQ1 := value - scoreEQ1NextLower
	availableEQ2 := value - scoreEQ2NextLower
	availableEQ3EQ6 := value - scoreEQ3EQ6NextLower
	availableEQ4 := value - scoreEQ4NextLower
	availableEQ5 := value - scoreEQ5NextLower

	nExistingLower := 0
	var normalizedEQ1, normalizedEQ2, normalizedEQ3EQ6, normalizedEQ4, normalizedEQ5 float64

	maxSeverityEQ1Value := maxSeverityEQ1[eq1] * step
	maxSeverityEQ2Value := maxSeverityEQ2[eq2] * step
	maxSeverityEQ3EQ6Value := maxSeverityEQ3EQ6[eq3][eq6] * step
	maxSeverityEQ4Value := maxSeverityEQ4[eq4] * step

	if !math.IsNaN(availableEQ1) {
		nExistingLower++
		normalizedEQ1 = availableEQ1 * (currentEQ1 / maxSeverityEQ1Value)
	}
	if !math.IsNaN(availableEQ2) {
		nExistingLower++
		normalizedEQ2 = availableEQ2 * (currentEQ2 / maxSeverityEQ2Value)
	}
	if !math.IsNaN(availableEQ3EQ6) {
		nExistingLower++
		normalizedEQ3EQ6 = availableEQ3EQ6 * (currentEQ3EQ6 / maxSeverityEQ3EQ6Value)
	}
	if !math.IsNaN(availableEQ4) {
		nExistingLower++
		normalizedEQ4 = availableEQ4 * (currentEQ4 / maxSeverityEQ4Value)
	}
	if !math.IsNaN(availableEQ5) {
		nExistingLower++
		normalizedEQ5 = availableEQ5 * 0
	}

	meanDistance := 0.0
	if nExistingLower > 0 {
		meanDistance = (normalizedEQ1 + normalizedEQ2 + normalizedEQ3EQ6 + normalizedEQ4 + normalizedEQ5) / float64(nExistingLower)
	}

	value -= meanDistance
	value = math.Max(0, math.Min(10, value))
	return math.Floor(value*10+0.5) / 10
}

func Score(vector string) (float64, error) {
	v, err := Parse(vector)
	if err != nil {
		return 0, err
	}
	return v.Score(), nil
}

func Severity(score float64) string {
	switch {
	case score == 0:
		return "None"
	case score < 4:
		return "Low"
	case score < 7:
		return "Medium"
	case score < 9:
		return "High"
	default:
		return "Critical"
	}
}
