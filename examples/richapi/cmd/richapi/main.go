package main

import (
	_ "embed"
	"fmt"
	"os"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/lathe"
	"github.com/lathe-cli/lathe/pkg/runtime"

	"example/richapi/internal/generated"
)

//go:embed cli.yaml
var manifestBytes []byte

func main() {
	m, err := config.Load(manifestBytes)
	if err != nil {
		panic(err)
	}
	config.Bind(m)

	root := lathe.NewApp(m)
	if err := generated.MountModules(root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(runtime.Execute(root))
}
