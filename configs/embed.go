package configs

import "embed"

//go:embed *.yaml *.toml prompts
var FS embed.FS
