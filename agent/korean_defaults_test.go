package agent

import (
	"encoding/json"
	"regexp"
	"testing"
)

func TestDefaultAgentPromptsHaveNoChineseCharacters(t *testing.T) {
	han := regexp.MustCompile(`\p{Han}`)
	for key, prompt := range BuiltinPromptSeeds() {
		if han.MatchString(prompt) {
			t.Errorf("%s default prompt still contains Chinese characters", key)
		}
	}
	for key, prompt := range map[string]string{
		"reporter":  ReporterDefaultPrompt,
		"retester":  RetesterDefaultPrompt,
		"assistant": DefaultAssistantPrompt,
	} {
		if han.MatchString(prompt) {
			t.Errorf("%s default prompt still contains Chinese characters", key)
		}
	}
}

func TestBuiltinToolDescriptionsHaveNoChineseCharacters(t *testing.T) {
	han := regexp.MustCompile(`\p{Han}`)
	for _, seed := range BuiltinToolSeeds() {
		schema, err := json.Marshal(seed.Schema)
		if err != nil {
			t.Fatalf("%s schema: %v", seed.Key, err)
		}
		if han.MatchString(seed.Desc) || han.Match(schema) {
			t.Errorf("%s still has Chinese in its description or schema", seed.Key)
		}
	}
}
