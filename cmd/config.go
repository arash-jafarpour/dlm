package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"dlm/config"
)

func configCmd(ctx *Context, args []string) {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		if len(args) > 1 {
			generateCommandUsage([]string{"config", args[1]})
		} else {
			generateCommandUsage([]string{"config"})
		}
		return
	}

	if len(args) < 1 {
		fmt.Println("config subcommands: show | set | path | reset")
		fmt.Println("Run 'dlm config --help' for more information")
		os.Exit(1)
	}

	switch args[0] {
	case "show":
		showConfig(ctx)
	case "set":
		if len(args) < 3 {
			fmt.Println("usage: dlm config set <key> <value>")
			os.Exit(1)
		}
		setConfig(ctx, args[1], args[2])
	case "reset":
		resetConfig(ctx)
	case "path":
		homeDir, _ := os.UserHomeDir()
		fmt.Println(filepath.Join(homeDir, ".config", "dlm", "config.json"))
	default:
		fmt.Printf("unknown config subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func showConfig(ctx *Context) {
	data, err := json.MarshalIndent(ctx.Config, "", "  ")
	if err != nil {
		fmt.Printf("error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(data))
}

func setConfig(ctx *Context, key, value string) {
	homeDir, _ := os.UserHomeDir()
	configPath := filepath.Join(homeDir, ".config", "dlm", "config.json")

	switch key {
	case "queue_file":
		ctx.Config.QueueFile = value
	case "output_dir":
		ctx.Config.OutputDir = value
	case "num_chunks":
		var chunks int
		fmt.Sscanf(value, "%d", &chunks)
		ctx.Config.NumChunks = chunks
	case "insecure_skip_verify":
		ctx.Config.InsecureSkipVerify = (value == "true")
	default:
		fmt.Printf("unknown config key: %s\n", key)
		os.Exit(1)
	}

	if err := ctx.Config.Save(configPath); err != nil {
		fmt.Printf("error saving config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("config updated: %s = %s\n", key, value)
}

func resetConfig(ctx *Context) {
	homeDir, _ := os.UserHomeDir()
	configPath := filepath.Join(homeDir, ".config", "dlm", "config.json")

	ctx.Config = config.Default()

	if err := ctx.Config.Save(configPath); err != nil {
		fmt.Printf("error saving config: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("config reset to defaults")
}
