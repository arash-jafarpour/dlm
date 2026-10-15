package cmd

import (
	"fmt"
	"strconv"

	"github.com/arash-jafarpour/dlm/config"
	tui "github.com/arash-jafarpour/dlm/internal/tui"
)

// settingDef describes one editable config-file value for the board's
// settings page. Validation reuses the config package's Validate*
// functions, so the page and `dlm config set` accept exactly the same
// input.
type settingDef struct {
	Key  string
	Kind tui.SettingKind
	Help string

	Get     func(cfg *config.Config) string
	Set     func(cfg *config.Config, value string) error
	Default func(cfg *config.Config) string
}

var settingDefs = []settingDef{
	{
		Key:  "num_chunks",
		Kind: tui.SettingInt,
		Help: "parallel chunks per file (1-16)",
		Get:  func(c *config.Config) string { return strconv.Itoa(c.NumChunks) },
		Set: func(c *config.Config, value string) error {
			n, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("num_chunks must be a valid integer: %w", err)
			}
			if err := config.ValidateNumChunks(n); err != nil {
				return err
			}
			c.NumChunks = n
			return nil
		},
		Default: func(c *config.Config) string { return strconv.Itoa(config.Default().NumChunks) },
	},
	{
		Key:  "queue_jobs",
		Kind: tui.SettingInt,
		Help: "queue downloads at once (1-8)",
		Get:  func(c *config.Config) string { return strconv.Itoa(c.QueueJobs) },
		Set: func(c *config.Config, value string) error {
			n, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("queue_jobs must be a valid integer: %w", err)
			}
			if err := config.ValidateQueueJobs(n); err != nil {
				return err
			}
			c.QueueJobs = n
			return nil
		},
		Default: func(c *config.Config) string { return strconv.Itoa(config.Default().QueueJobs) },
	},
	{
		Key:  "insecure_skip_verify",
		Kind: tui.SettingBool,
		Help: "skip TLS certificate verification",
		Get:  func(c *config.Config) string { return strconv.FormatBool(c.InsecureSkipVerify) },
		Set: func(c *config.Config, value string) error {
			if err := config.ValidateInsecureSkipVerify(value); err != nil {
				return err
			}
			c.InsecureSkipVerify = value == "true"
			return nil
		},
		Default: func(c *config.Config) string {
			return strconv.FormatBool(config.Default().InsecureSkipVerify)
		},
	},
	{
		Key:  "output_dir",
		Kind: tui.SettingText,
		Help: "where files are saved",
		Get:  func(c *config.Config) string { return c.OutputDir },
		Set: func(c *config.Config, value string) error {
			if err := config.ValidateOutputDir(value); err != nil {
				return err
			}
			c.OutputDir = value
			return nil
		},
		Default: func(c *config.Config) string { return config.Default().OutputDir },
	},
	{
		Key:  "queue_file",
		Kind: tui.SettingText,
		Help: "path to the queue file",
		Get:  func(c *config.Config) string { return c.QueueFile },
		Set: func(c *config.Config, value string) error {
			if err := config.ValidateQueueFile(value); err != nil {
				return err
			}
			c.QueueFile = value
			return nil
		},
		Default: func(c *config.Config) string { return config.Default().QueueFile },
	},
	{
		Key:  "completed_file",
		Kind: tui.SettingText,
		Help: "path to the completed history",
		Get:  func(c *config.Config) string { return c.CompletedFile },
		Set: func(c *config.Config, value string) error {
			if err := config.ValidateCompletedFile(value); err != nil {
				return err
			}
			c.CompletedFile = value
			return nil
		},
		Default: func(c *config.Config) string { return config.Default().CompletedFile },
	},
}

func settingByKey(key string) *settingDef {
	for i := range settingDefs {
		if settingDefs[i].Key == key {
			return &settingDefs[i]
		}
	}
	return nil
}

// settingsRows renders the current config as board rows, in a stable order
// (settingDefs order).
func settingsRows(cfg *config.Config) []tui.Setting {
	rows := make([]tui.Setting, 0, len(settingDefs))
	for i := range settingDefs {
		d := &settingDefs[i]
		rows = append(rows, tui.Setting{
			Key:   d.Key,
			Value: d.Get(cfg),
			Kind:  d.Kind,
			Help:  d.Help,
		})
	}
	return rows
}

// applySetting validates and applies one key=value to cfg (no disk write:
// the caller saves once, after a successful change).
func applySetting(cfg *config.Config, key, value string) error {
	d := settingByKey(key)
	if d == nil {
		return fmt.Errorf("unknown config key %q", key)
	}
	return d.Set(cfg, value)
}
