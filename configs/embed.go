package configs

import "embed"

//go:embed *.yaml *.toml *.md prompts
var FS embed.FS
