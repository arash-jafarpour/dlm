package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/arash-jafarpour/dlm/config"
	"github.com/arash-jafarpour/dlm/downloader"

	"github.com/spf13/cobra"
)

type Context struct {
	Config     *config.Config
	Downloader DownloadRunner
	// Quiet mirrors the --quiet flag: command-level informational prints
	// (status lines, confirmations) are suppressed while errors stay.
	Quiet bool
}

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
		// The lock file close/remove is best-effort housekeeping: the flock
		// is already gone, so a failed os.Remove must not mask the result.
		_ = state.lock.Release()
	}

	if err != nil {
		logError(err)
		return err
	}

	return nil
}

func newRootCmd(state *appState) *cobra.Command {
	var inline bool

	root := &cobra.Command{
		Use:           "dlm",
		Short:         "Download Manager - A powerful download utility",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		// Bare `dlm` is the product: the interactive board on a terminal
		// (plain snapshot when piped or the queue is empty).
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUI(state.ctx, inline)
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Shell completion, help, and version must never contend for the
			// download lock or require a valid config.
			if cmd.Name() == "help" || cmd.Name() == "version" || cmd.Name() == "update" ||
				isShellCompletionCmd(cmd) {
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

			quiet, err := cmd.Flags().GetBool("quiet")
			if err != nil {
				return err
			}
			noProgress, err := cmd.Flags().GetBool("no-progress")
			if err != nil {
				return err
			}
			state.ctx.Quiet = quiet
			state.ctx.Downloader = downloader.New(
				cfg,
				&cliReporter{quiet: quiet, noProgress: noProgress},
			)

			return nil
		},
	}

	root.AddCommand(
		newQueueCmd(state.ctx),
		newDownloadCmd(state.ctx),
		newCompletedCmd(state.ctx),
		newConfigCmd(state.ctx),
		newVersionCmd(),
		newUpdateCmd(),
	)

	root.Flags().BoolVar(&inline, "inline", false,
		"draw the interactive board inline instead of taking over the screen")

	// Output control flags work on every command, which is why they are
	// persistent; they only affect download/listing output, never errors.
	root.PersistentFlags().BoolP("quiet", "q", false, "suppress all non-essential output (errors still show)")
	root.PersistentFlags().Bool("no-progress", false, "disable the progress bar but keep status messages")

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
