package cmd

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/arash-jafarpour/dlm/config"

	"github.com/spf13/cobra"
)

var configKeys = []string{
	"queue_file",
	"completed_file",
	"output_dir",
	"num_chunks",
	"insecure_skip_verify",
}

func newConfigCmd(ctx *Context) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage configuration",
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "show",
			Short: "Show current configuration",
			RunE: func(cmd *cobra.Command, args []string) error {
				return showConfig(ctx)
			},
		},
		&cobra.Command{
			Use:               "set <key> <value>",
			Short:             "Set configuration value",
			Args:              cobra.ExactArgs(2),
			ValidArgsFunction: completeConfigSet,
			RunE: func(cmd *cobra.Command, args []string) error {
				return setConfig(ctx, args[0], args[1])
			},
		},
		&cobra.Command{
			Use:   "path",
			Short: "Show configuration file path",
			RunE: func(cmd *cobra.Command, args []string) error {
				path, err := configPath()
				if err != nil {
					return err
				}
				fmt.Println(path)
				return nil
			},
		},
		&cobra.Command{
			Use:   "reset",
			Short: "Resets to default configuration",
			RunE: func(cmd *cobra.Command, args []string) error {
				return resetConfig(ctx)
			},
		},
	)

	return cmd
}

// completeConfigSet offers config keys for the first argument and, for
// insecure_skip_verify, its boolean values for the second.
func completeConfigSet(
	cmd *cobra.Command,
	args []string,
	toComplete string,
) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return configKeys, cobra.ShellCompDirectiveNoFileComp
	}
	if len(args) == 1 && args[0] == "insecure_skip_verify" {
		return []string{"true", "false"}, cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

func showConfig(ctx *Context) error {
	data, err := json.MarshalIndent(ctx.Config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode config: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

func setConfig(ctx *Context, key, value string) error {
	cfgPath, err := configPath()
	if err != nil {
		return err
	}

	switch key {
	case "queue_file":
		if err := config.ValidateQueueFile(value); err != nil {
			return err
		}
		ctx.Config.QueueFile = value

	case "completed_file":
		if err := config.ValidateCompletedFile(value); err != nil {
			return err
		}
		ctx.Config.CompletedFile = value

	case "output_dir":
		if err := config.ValidateOutputDir(value); err != nil {
			return err
		}
		ctx.Config.OutputDir = value

	case "num_chunks":
		chunks, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("num_chunks must be a valid integer: %w", err)
		}
		if err := config.ValidateNumChunks(chunks); err != nil {
			return err
		}
		ctx.Config.NumChunks = chunks

	case "insecure_skip_verify":
		if err := config.ValidateInsecureSkipVerify(value); err != nil {
			return err
		}
		ctx.Config.InsecureSkipVerify = (value == "true")

	default:
		return fmt.Errorf("unknown config key %q (valid keys: %s)", key, strings.Join(configKeys, ", "))
	}

	if err := ctx.Config.Save(cfgPath); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Printf("config updated: %s = %s\n", key, value)
	return nil
}

func resetConfig(ctx *Context) error {
	cfgPath, err := configPath()
	if err != nil {
		return err
	}

	ctx.Config = config.Default()

	if err := ctx.Config.Save(cfgPath); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Println("config reset to defaults")
	return nil
}
