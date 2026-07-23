// Package rootcause embeds the project's two core assets — the Knowledge
// base and the Rule definitions — into the compiled binary, so an installed
// CLI works standalone without needing a clone of this repository.
package rootcause

import "embed"

//go:embed knowledge
var KnowledgeFS embed.FS

//go:embed rules
var RulesFS embed.FS
