// Package agentdocs embeds the agent-facing documentation in the server binary.
package agentdocs

import _ "embed"

//go:embed COMMANDS.md
var commandsMarkdown string

// CommandsMarkdown returns the complete guide shipped with this server build.
func CommandsMarkdown() string { return commandsMarkdown }
