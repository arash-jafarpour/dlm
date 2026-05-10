package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"dlm/config"
	"dlm/downloader"
)

type Context struct {
	Config     *config.Config
	Downloader *downloader.Downloader
}

func Execute() {
	lock, err := acquireLock()
	if err != nil {
		fmt.Printf("error: %v\n", err)
		os.Exit(1)
	}
	defer lock.Release()

	homeDir, _ := os.UserHomeDir()
	configPath := filepath.Join(homeDir, ".config", "dlm", "config.json")

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Printf("error loading config: %v\n", err)
		os.Exit(1)
	}

	if err := cfg.Validate(); err != nil {
		fmt.Printf("invalid config: %v\n", err)
		os.Exit(1)
	}

	dl := downloader.New(cfg)
	ctx := &Context{
		Config:     cfg,
		Downloader: dl,
	}

	if len(os.Args) < 2 {
		generateUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "queue":
		queueCmd(ctx, os.Args[2:])
	case "download":
		downloadCmd(ctx, os.Args[2:])
	case "completed":
		completedCmd(ctx, os.Args[2:])
	case "config":
		configCmd(ctx, os.Args[2:])
	default:
		fmt.Printf("unknown command: %s\n", os.Args[1])
		generateUsage()
		os.Exit(1)
	}
}
