package agent

import "testing"

func TestTrivialProtocolPromptOnly(t *testing.T) {
	for _, quote := range []string{
		"Your input is 'A",
		"[2] Overwrite the return address",
		"Input: ",
		"Choice: ",
		"Your input is 'A\n'\n[2] Next input\nInput: ",
	} {
		if !trivialProtocolPromptOnly(quote) {
			t.Errorf("accepted ordinary prompt/echo: %q", quote)
		}
	}
	for _, quote := range []string{
		"Your input is 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\n'\nraw-leak: 0x12345678",
		"HTTP/1.1 500 Internal Server Error\nstack trace with unexpected sensitive content",
	} {
		if trivialProtocolPromptOnly(quote) {
			t.Errorf("rejected potentially substantive evidence: %q", quote)
		}
	}
}
