package cmd

import (
	"fmt"
	"strings"
)

type CommandInfo struct {
	Name        string
	Description string
	Subcommands map[string]CommandInfo
	Args        []ArgInfo
	Flags       []FlagInfo
}

type ArgInfo struct {
	Name        string
	Required    bool
	Description string
}

type FlagInfo struct {
	Name        string
	Short       string
	Type        string
	Default     string
	Description string
}

var commandTree = CommandInfo{
	Name:        "dlm",
	Description: "Download Manager - A powerful download utility",
	Subcommands: map[string]CommandInfo{
		"queue": {
			Name:        "queue",
			Description: "Manage download queue",
			Subcommands: map[string]CommandInfo{
				"add": {
					Name:        "add",
					Description: "Add a URL to the queue",
					Args: []ArgInfo{
						{Name: "url", Required: true, Description: "URL to download"},
					},
				},
				"list": {
					Name:        "list",
					Description: "List all URLs in the queue",
				},
				"clear": {
					Name:        "clear",
					Description: "Clear all URLs from the queue",
				},
			},
		},
		"download": {
			Name:        "download",
			Description: "Download files",
			Subcommands: map[string]CommandInfo{
				"url": {
					Name:        "url",
					Description: "Download a single URL",
					Args: []ArgInfo{
						{Name: "url", Required: true, Description: "URL to download"},
					},
					Flags: []FlagInfo{
						{
							Name:        "out",
							Short:       "o",
							Type:        "string",
							Default:     "",
							Description: "Output directory",
						},
						{
							Name:        "chunks",
							Short:       "c",
							Type:        "int",
							Default:     "4",
							Description: "Number of parallel chunks",
						},
						{
							Name:        "insecure",
							Short:       "",
							Type:        "bool",
							Default:     "false",
							Description: "Skip TLS verification",
						},
					},
				},
				"queue": {
					Name:        "queue",
					Description: "Download all URLs from the queue",
				},
			},
		},
		"completed": {
			Name:        "completed",
			Description: "Manage completed downloads",
			Subcommands: map[string]CommandInfo{
				"clear": {
					Name:        "clear",
					Description: "Clear completed downloads list",
				},
			},
		},
		"config": {
			Name:        "config",
			Description: "Manage configuration",
			Subcommands: map[string]CommandInfo{
				"show": {
					Name:        "show",
					Description: "Show current configuration",
				},
				"set": {
					Name:        "set",
					Description: "Set configuration value",
					Args: []ArgInfo{
						{
							Name:        "key",
							Required:    true,
							Description: "Configuration key (queue_file, output_dir, num_chunks, insecure_skip_verify)",
						},
						{Name: "value", Required: true, Description: "Configuration value"},
					},
				},
				"path": {
					Name:        "path",
					Description: "Show configuration file path",
				},
				"reset": {
					Name:        "reset",
					Description: "Resets to default configuration",
				},
			},
		},
	},
}

func generateUsage() {
	fmt.Printf("%s - %s\n", commandTree.Name, commandTree.Description)
	fmt.Println("\nUsage:")
	fmt.Println("  dlm <command> [subcommand] [arguments] [flags]")

	fmt.Println("\nCommands:")

	// Find max command length for alignment
	maxLen := 0
	for name := range commandTree.Subcommands {
		if len(name) > maxLen {
			maxLen = len(name)
		}
	}

	for name, cmd := range commandTree.Subcommands {
		fmt.Printf("  %-*s  %s\n", maxLen, name, cmd.Description)
	}

	fmt.Println("\nRun 'dlm <command> --help' for more information on a command.")
}

func generateCommandUsage(args []string) {
	if len(args) == 0 {
		generateUsage()
		return
	}

	cmd, exists := commandTree.Subcommands[args[0]]
	if !exists {
		fmt.Printf("Unknown command: %s\n", args[0])
		generateUsage()
		return
	}

	// If no subcommand specified
	if len(args) == 1 {
		printCommandHelp(cmd, args[0])
		return
	}

	// Check for subcommand
	subcmd, exists := cmd.Subcommands[args[1]]
	if !exists {
		fmt.Printf("Unknown subcommand: %s %s\n", args[0], args[1])
		printCommandHelp(cmd, args[0])
		return
	}

	printSubcommandHelp(subcmd, args[0], args[1])
}

func printCommandHelp(cmd CommandInfo, parentName string) {
	fmt.Printf("Usage: dlm %s", parentName)
	if len(cmd.Subcommands) > 0 {
		fmt.Printf(" <subcommand>")
	}
	fmt.Println()

	if cmd.Description != "" {
		fmt.Printf("\n%s\n", cmd.Description)
	}

	if len(cmd.Subcommands) > 0 {
		fmt.Println("\nAvailable Commands:")

		maxLen := 0
		for name := range cmd.Subcommands {
			if len(name) > maxLen {
				maxLen = len(name)
			}
		}

		for name, sub := range cmd.Subcommands {
			// Build argument string if any
			argsStr := ""
			if len(sub.Args) > 0 {
				args := make([]string, len(sub.Args))
				for i, arg := range sub.Args {
					if arg.Required {
						args[i] = fmt.Sprintf("<%s>", arg.Name)
					} else {
						args[i] = fmt.Sprintf("[%s]", arg.Name)
					}
				}
				argsStr = " " + strings.Join(args, " ")
			}

			fmt.Printf("  %s%s", name, argsStr)

			// Align descriptions
			padding := maxLen + len(argsStr) + 2
			if padding < 20 {
				padding = 20
			}
			fmt.Printf("%*s%s\n", padding-len(name)-len(argsStr), "", sub.Description)
		}
	}

	fmt.Println("\nFlags:")
	fmt.Println("  -h, --help    Show help for this command")
}

func printSubcommandHelp(cmd CommandInfo, parentName, subcmdName string) {
	// Build usage string
	usage := fmt.Sprintf("Usage: dlm %s %s", parentName, subcmdName)

	// Add arguments
	for _, arg := range cmd.Args {
		if arg.Required {
			usage += fmt.Sprintf(" <%s>", arg.Name)
		} else {
			usage += fmt.Sprintf(" [%s]", arg.Name)
		}
	}
	fmt.Println(usage)

	if cmd.Description != "" {
		fmt.Printf("\n%s\n", cmd.Description)
	}

	// Show argument descriptions
	if len(cmd.Args) > 0 {
		fmt.Println("\nArguments:")
		maxLen := 0
		for _, arg := range cmd.Args {
			if len(arg.Name) > maxLen {
				maxLen = len(arg.Name)
			}
		}

		for _, arg := range cmd.Args {
			required := ""
			if arg.Required {
				required = " (required)"
			}
			fmt.Printf("  %-*s  %s%s\n", maxLen, arg.Name, arg.Description, required)
		}
	}

	// Show flags
	if len(cmd.Flags) > 0 {
		fmt.Println("\nFlags:")
		maxLen := 0
		for _, flag := range cmd.Flags {
			flagStr := fmt.Sprintf("--%s", flag.Name)
			if flag.Short != "" {
				flagStr = fmt.Sprintf("-%s, --%s", flag.Short, flag.Name)
			}
			if len(flagStr) > maxLen {
				maxLen = len(flagStr)
			}
		}

		for _, flag := range cmd.Flags {
			flagStr := fmt.Sprintf("--%s", flag.Name)
			if flag.Short != "" {
				flagStr = fmt.Sprintf("-%s, --%s", flag.Short, flag.Name)
			}
			fmt.Printf("  %-*s  %s", maxLen, flagStr, flag.Description)
			if flag.Default != "" {
				fmt.Printf(" (default: %s)", flag.Default)
			}
			fmt.Println()
		}
	}

	fmt.Println("\n  -h, --help    Show help for this command")
}
