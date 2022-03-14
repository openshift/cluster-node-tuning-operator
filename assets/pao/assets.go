package assets

import (
	"embed"
)

var (
	// Configs contains all files that placed under the configs directory
	//go:embed configs
	Configs embed.FS

	// Scripts contains all files that placed under the scripts directory
	//go:embed scripts
	Scripts embed.FS

	// Tuned contains all files that placed under the tuned directory
	//go:embed tuned
	Tuned embed.FS
)
