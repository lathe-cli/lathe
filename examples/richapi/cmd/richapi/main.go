package main

import (
	_ "embed"
	"os"

	"github.com/lathe-cli/lathe/pkg/lathe"

	"example/richapi/internal/generated"
)

//go:embed cli.yaml
var manifestBytes []byte

func main() {
	os.Exit(lathe.Run(lathe.RunOptions{
		Manifest: manifestBytes,
		Mount:    generated.MountModules,
	}))
}
