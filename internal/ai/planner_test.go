package ai

import (
	"strings"
	"testing"
)

func TestPlannerSystemPrompt_ProgrammingQuestionsReturnEmptyTasks(t *testing.T) {
	prompt := plannerSystemPrompt()

	checks := []struct {
		name    string
		segment string
	}{
		{
			name:    "programming/coding questions must NOT call a tool",
			segment: "Programming, coding, or algorithm questions you can answer from your own knowledge",
		},
		{
			name:    "Two Sum example explicitly listed as empty-task case",
			segment: "give me C++ code for Two Sum",
		},
		{
			name:    "binary search example explicitly listed as empty-task case",
			segment: "explain binary search",
		},
		{
			name:    "general knowledge questions do not need tools",
			segment: "General knowledge questions about well-established concepts",
		},
		{
			name:    "tools only for info the model cannot know",
			segment: "USE A TOOL ONLY when the answer genuinely requires information the model cannot know",
		},
		{
			name:    "do NOT use web_search merely because topic is niche",
			segment: "Do NOT use a tool merely because a topic is technical, niche, or unfamiliar",
		},
		{
			name:    "current information still maps to tools",
			segment: "Current information — live prices, weather, recent events, breaking news, up-to-date stats",
		},
		{
			name:    "explicit web search request still maps to web_search",
			segment: "explicitly asks to 'search the web'/'search the internet' → web_search",
		},
	}

	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(prompt, c.segment) {
				t.Errorf("plannerSystemPrompt() is missing required rule segment %q", c.segment)
			}
		})
	}
}

func TestPlannerSystemPrompt_WebSearchRuleIsNeedBased(t *testing.T) {
	prompt := plannerSystemPrompt()

	// The old broad rule used "niche" as a trigger for web_search. After the fix,
	// web_search selection must be driven by information need, not topic niche-ness.
	if strings.Contains(prompt, "Niche, real-time, or non-Wikipedia topics → web_search") {
		t.Error("plannerSystemPrompt() still contains the old broad web_search rule that routes niche technical topics to web_search")
	}

	if !strings.Contains(prompt, "web_search") {
		t.Error("plannerSystemPrompt() must keep web_search available for genuinely external/current information")
	}
}

func TestPlannerSystemPrompt_PreservesExistingRules(t *testing.T) {
	prompt := plannerSystemPrompt()

	preserved := []string{
		"Greetings or chit-chat",
		"Simple math",
		"PRONOUN RESOLUTION",
		"NEVER use 'he', 'she', 'it', 'they', 'his', 'her', 'that'",
		"set depends_on",
		"Each task calls EXACTLY ONE tool",
		"make one task per location",
		"MAXIMUM 6 TASKS",
		"NEVER use both search_news AND web_search",
		"Available tools:",
	}

	for _, segment := range preserved {
		if !strings.Contains(prompt, segment) {
			t.Errorf("plannerSystemPrompt() lost existing rule segment %q", segment)
		}
	}
}