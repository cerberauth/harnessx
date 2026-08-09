package checkdef

const (
	testCheckID          = "alg_none"
	testCheckName        = "Algorithm None"
	testCheckDescription = "Tests if the server accepts tokens with the algorithm set to 'none'."
	testCheckLink        = "https://example.com/vulnerabilities/jwt-alg-none"
	testCheckTag         = "algorithm"
	testCheckDep1        = "baseline"
	testCheckDep2        = "no_verification"
	testCheckCVSSVector  = "CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:N/SC:N/SI:N/SA:N"
	testCheckCVSSScore   = 9.3
	testCheckCWEID       = "CWE-345"
	testCheckCAPECID     = "CAPEC-31"
	testCheckOWASP       = "API2:2023"
	testCheckExtraKey    = "custom_field"
	testCheckExtraValue  = "custom_value"
)

func wantTestCheckDef() CheckDef {
	return CheckDef{
		ID:          testCheckID,
		Name:        testCheckName,
		Description: testCheckDescription,
		Link:        testCheckLink,
		Tags:        []string{testCheckTag},
		DependsOn:   []string{testCheckDep1, testCheckDep2},
		CVSSVector:  testCheckCVSSVector,
		CVSSScore:   testCheckCVSSScore,
		CWEID:       testCheckCWEID,
		CAPECID:     testCheckCAPECID,
		OWASP:       testCheckOWASP,
		Extra:       map[string]any{testCheckExtraKey: testCheckExtraValue},
	}
}
