package checkdef

const (
	testCheckID          = "alg_none"
	testCheckName        = "Algorithm None"
	testCheckDescription = "Tests if the server accepts tokens with the algorithm set to 'none'."
	testCheckLink        = "https://example.com/vulnerabilities/jwt-alg-none"
	testCheckTag         = "algorithm"
	testCheckDep1        = "baseline"
	testCheckDep2        = "no_verification"
)

func wantTestCheckDef() CheckDef {
	return CheckDef{
		ID:          testCheckID,
		Name:        testCheckName,
		Description: testCheckDescription,
		Link:        testCheckLink,
		Tags:        []string{testCheckTag},
		DependsOn:   []string{testCheckDep1, testCheckDep2},
	}
}
