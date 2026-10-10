package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"dlm/config"
	"dlm/downloader"

	"github.com/spf13/cobra"
)

type Context struct {
	Config     *config.Config
	Downloader *downloader.Downloader
}

// errNoCommand is returned when dlm is invoked without a subcommand. It lets
// Execute exit with a non-zero status without printing an extra error message.
var errNoCommand = errors.New("no command specified")

// appState holds process-wide state that is initialized lazily in the root
// command's PersistentPreRunE, after flags have been parsed.
type appState struct {
	ctx  *Context
	lock *LockFile
}

func Execute() error {
	state := &appState{ctx: &Context{}}

	root := newRootCmd(state)
	err := root.Execute()

	if state.lock != nil {
		state.lock.Release()
	}

	if err != nil {
		if errors.Is(err, errNoCommand) {
			return err
		}
		logError(err)
		return err
	}

	return nil
}

func newRootCmd(state *appState) *cobra.Command {
	root := &cobra.Command{
		Use:           "dlm",
		Short:         "Download Manager - A powerful download utility",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = cmd.Help()
			return errNoCommand
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Shell completion, help, and version must never contend for the
			// download lock or require a valid config.
			if cmd.Name() == "help" || cmd.Name() == "version" || isShellCompletionCmd(cmd) {
				return nil
			}

			lock, err := acquireLock()
			if err != nil {
				return err
			}
			state.lock = lock

			cfgPath, err := configPath()
			if err != nil {
				return err
			}

			cfg, err := config.Load(cfgPath)
			if err != nil {
				return fmt.Errorf("error loading config: %w", err)
			}

			if err := cfg.Validate(); err != nil {
				return fmt.Errorf("invalid config: %w", err)
			}

			state.ctx.Config = cfg
			state.ctx.Downloader = downloader.New(cfg)

			return nil
		},
	}

	root.AddCommand(
		newQueueCmd(state.ctx),
		newDownloadCmd(state.ctx),
		newCompletedCmd(state.ctx),
		newConfigCmd(state.ctx),
		newVersionCmd(),
	)

	return root
}

// isShellCompletionCmd reports whether cmd belongs to cobra's shell-completion
// command tree (e.g. "dlm completion bash" or the hidden "__complete" command).
func isShellCompletionCmd(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case "completion", "__complete", "__completeNoDesc":
			return true
		}
	}
	return false
}

// noFileCompletion disables filesystem completion for commands whose arguments
// are not paths (e.g. URLs, config keys).
func noFileCompletion(
	cmd *cobra.Command,
	args []string,
	toComplete string,
) ([]string, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// configPath returns the path to the dlm configuration file, matching the
// location used by config.Load.
func configPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get config directory: %w", err)
	}
	return filepath.Join(configDir, "dlm", "config.json"), nil
}
