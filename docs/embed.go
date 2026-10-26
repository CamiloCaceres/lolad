// Package docs supplies the canonical agent-facing messaging guide.
package docs

import _ "embed"

// AgentMessaging is also the template for the managed CLAUDE.md section.
//
//go:embed agent-messaging.md
var AgentMessaging string
