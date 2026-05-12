package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	NumChunks             int           `json:"num_chunks"`
	InsecureSkipVerify    bool          `json:"insecure_skip_verify"`
	OutputDir             string        `json:"output_dir"`
	QueueFile             string        `json:"queue_file"`
	CompletedFile         string        `json:"completed_file"`
	MaxRetries            int           `json:"-"`
	DialTimeout           time.Duration `json:"-"`
	KeepAlive             time.Duration `json:"-"`
	MaxIdleConns          int           `json:"-"`
	IdleConnTimeout       time.Duration `json:"-"`
	TLSHandshakeTimeout   time.Duration `json:"-"`
	ResponseHeaderTimeout time.Duration `json:"-"`
	ExpectContinueTimeout time.Duration `json:"-"`
}

func Default() *Config {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}

	downloadsDir := filepath.Join(homeDir, "Downloads", "dlm")
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = "."
	}
	queueFilePath := filepath.Join(configDir, "queue.txt")
	completedFilePath := filepath.Join(configDir, "completed.txt")

	return &Config{
		NumChunks:             8,
		InsecureSkipVerify:    false,
		OutputDir:             downloadsDir,
		QueueFile:             queueFilePath,
		CompletedFile:         completedFilePath,
		MaxRetries:            3,
		DialTimeout:           10 * time.Second,
		KeepAlive:             30 * time.Second,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

func Load(path string) (*Config, error) {
	cfg := Default()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	return cfg, nil
}

func (c *Config) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}
