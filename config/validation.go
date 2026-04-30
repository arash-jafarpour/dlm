package config

import "fmt"

func (c *Config) Validate() error {
	if err := ValidateNumChunks(c.NumChunks); err != nil {
		return err
	}
	if err := ValidateMaxRetries(c.MaxRetries); err != nil {
		return err
	}
	if err := ValidateQueueFile(c.QueueFile); err != nil {
		return err
	}
	if err := ValidateCompletedFile(c.CompletedFile); err != nil {
		return err
	}
	if err := ValidateOutputDir(c.OutputDir); err != nil {
		return err
	}
	if c.DialTimeout < 0 {
		return fmt.Errorf("dial_timeout cannot be negative")
	}
	return nil
}

// Validation functions for individual fields
func ValidateNumChunks(chunks int) error {
	if chunks < 1 {
		return fmt.Errorf("num_chunks must be at least 1")
	}
	if chunks > 16 {
		return fmt.Errorf("num_chunks cannot exceed 16")
	}
	return nil
}

func ValidateMaxRetries(retries int) error {
	if retries < 0 {
		return fmt.Errorf("max_retries cannot be negative")
	}
	if retries > 10 {
		return fmt.Errorf("max_retries cannot exceed 10")
	}
	return nil
}

func ValidateQueueFile(path string) error {
	if path == "" {
		return fmt.Errorf("queue_file cannot be empty")
	}
	return nil
}

func ValidateCompletedFile(path string) error {
	if path == "" {
		return fmt.Errorf("completed_file cannot be empty")
	}
	return nil
}

func ValidateOutputDir(path string) error {
	if path == "" {
		return fmt.Errorf("output_dir cannot be empty")
	}
	return nil
}

func ValidateInsecureSkipVerify(value string) error {
	if value != "true" && value != "false" {
		return fmt.Errorf("insecure_skip_verify must be 'true' or 'false'")
	}
	return nil
}
