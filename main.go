package main

import (
	"os"

	"github.com/arash-jafarpour/dlm/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
