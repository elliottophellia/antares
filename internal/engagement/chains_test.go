package engagement

import "testing"

func TestDetectAccountTakeover(t *testing.T) {
	chains := DetectChains([]string{"IDOR on /api/users (CWE-639)", "Authentication bypass via password reset"})
	if len(chains) == 0 || chains[0].Name != "Account takeover" {
		t.Fatalf("expected account takeover chain, got %+v", chains)
	}
}

func TestDetectSSRFCloudCreds(t *testing.T) {
	chains := DetectChains([]string{"SSRF in webhook (CWE-918)", "reaches 169.254.169.254 metadata endpoint"})
	found := false
	for _, c := range chains {
		if c.Name == "Cloud credential theft via SSRF" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected SSRF cloud chain, got %+v", chains)
	}
}

func TestNoChainFromSingleClass(t *testing.T) {
	// One IDOR alone is not a chain.
	if chains := DetectChains([]string{"IDOR on /api/users"}); len(chains) != 0 {
		t.Fatalf("a single weakness should not form a chain, got %+v", chains)
	}
}
