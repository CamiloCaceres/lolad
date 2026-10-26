package harness

import "github.com/CamiloCaceres/lolad/internal/core"

// Models offers convenient choices; the harness still accepts arbitrary model IDs.
func Models(h core.Harness) []core.ModelOption {
	switch h {
	case core.HarnessClaude:
		return []core.ModelOption{
			{ID: "claude-opus-5-5", Label: "Opus 5.5"},
			{ID: "claude-opus-5-5[1m]", Label: "Opus 5.5 (1M context)"},
			{ID: "claude-sonnet-5-5", Label: "Sonnet 5.5"},
			{ID: "claude-sonnet-5-5[1m]", Label: "Sonnet 5.5 (1M context)"},
			{ID: "claude-haiku-5-5", Label: "Haiku 5.5"},
			{ID: "claude-fable-5-1", Label: "Fable 5.1", Note: "Accepted by installed Claude; account safeguards may restrict requests"},
			{ID: "opus", Label: "Opus (latest)"}, {ID: "sonnet", Label: "Sonnet (latest)"}, {ID: "haiku", Label: "Haiku (latest)"},
		}
	case core.HarnessCodewhale:
		return []core.ModelOption{{ID: "deepseek-flash", Label: "DeepSeek Flash", Note: "Installed Codewhale cached catalog"}, {ID: "deepseek-v4-pro", Label: "DeepSeek V4 Pro", Note: "Installed Codewhale cached catalog"}}
	default:
		return []core.ModelOption{}
	}
}
