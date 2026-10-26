package fake

import (
	"fmt"
	"time"
)

// A realistic noisy harness home exercises the settings workspace without
// touching an actual agent directory or introducing real credentials.
func seedSettingsWorkspace(c *agentCfg, at time.Time) {
	c.put("AGENTS.md", "# Working agreement\n\nKeep durable decisions in brain/ and memory/.\n", at)
	c.put(".claude/settings.local.json", "{\n  \"permissions\": {\"allow\": []}\n}\n", at)
	c.put("brain/people/milo.md", "# Milo\n\nPrefers short updates and clear decisions.\n", at)
	c.put("memory/daily/2026-10-25.md", "# Daily notes\n\nReviewed the inbox and upcoming calendar.\n", at)
	c.put("tools/notes/README.md", "# Local tools\n\nInstructions for maintaining the agent's tools.\n", at)
	for i := 1; i <= 24; i++ {
		c.put(fmt.Sprintf(".codewhale-home/builtin-plugins/snapshots/computer-use-3898%d/skills/computer-use/references/browser.md", i), "# Bundled browser reference\n\nIllustrative generated file.\n", at)
	}
	c.put(".cache/session/state.json", "{}\n", at)
	c.put("backups/2026-10-24/lolabot.yaml", "# Illustrative backup\n", at)
}
