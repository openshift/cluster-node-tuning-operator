package assets

import "embed"

var (
	//go:embed manifests/default-cr-tuned.yaml
	DefaultCrTuned []byte

	//go:embed manifests
	Manifests embed.FS
)
