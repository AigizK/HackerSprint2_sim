// Package agentdocs embeds the agent-facing documentation in the server binary.
package agentdocs

import _ "embed"

//go:embed COMMANDS.md
var commandsMarkdown string

//go:embed openapi.yaml
var openAPIYAML string

// CommandsMarkdown returns the complete guide shipped with this server build.
func CommandsMarkdown() string { return commandsMarkdown }

// OpenAPIYAML returns the complete API contract shipped with this server build.
func OpenAPIYAML() string { return openAPIYAML }
