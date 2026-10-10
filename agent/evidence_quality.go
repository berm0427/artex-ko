package agent

import "strings"

// A status line and ordinary response headers establish that a request worked,
// not that a vulnerability exists. This is intentionally a narrow guard: a
// custom header or response body is left to the normal evidence review.
func routineHTTPMetadataOnly(quote string) bool {
	lines := strings.Split(strings.ReplaceAll(quote, "\r", ""), "\n")
	seen := false
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "<"))
		if line == "" {
			continue
		}
		seen = true
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "http/") || strings.HasPrefix(lower, "established connection to ") {
			continue
		}
		known := false
		for _, prefix := range []string{"server:", "date:", "content-type:", "content-length:", "cache-control:", "connection:", "vary:", "accept-ranges:"} {
			if strings.HasPrefix(lower, prefix) {
				known = true
				break
			}
		}
		if !known {
			return false
		}
	}
	return seen
}

// A self-described public page and routine response metadata do not establish
// information disclosure. Do not apply this shortcut to a claim that actually
// names unauthorized access or sensitive data; those still require provenance.
func publicPageExposureClaim(vulnClass, summary, quote string) bool {
	class, claim, evidence := strings.ToLower(vulnClass), strings.ToLower(summary), strings.ToLower(quote)
	if !strings.Contains(class, "information_disclosure") && !strings.Contains(class, "정보노출") && !strings.Contains(class, "정보 노출") {
		return false
	}
	if !containsAny(claim, "header", "헤더", "body", "본문", "public", "공개") ||
		containsAny(claim, "unauthorized", "비인가", "민감", "secret", "기밀", "private") {
		return false
	}
	return containsAny(evidence, "공개", "public")
}

// A denial response proves that the attempted access was blocked, even when a
// shell tool itself exited successfully. It must not substantiate a finding
// claiming that the protected file was downloaded.
func deniedAccessEvidence(quote string) bool {
	lower := strings.ToLower(strings.TrimSpace(quote))
	return containsAny(lower,
		"outside lab boundary", "access denied", "permission denied",
		"403 forbidden", "404 not found", "file not found",
	)
}

func containsAny(s string, parts ...string) bool {
	for _, part := range parts {
		if strings.Contains(s, part) {
			return true
		}
	}
	return false
}
