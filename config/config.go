package config

import (
	"errors"
	"time"
)

// Config holds every tunable value in the application.
// Nothing else should have magic numbers or hard-coded paths.
type Config struct {
	// Files
	QueueFile     string
	CompletedFile string
	OutputDir     string

	// URL
	SingleURL string

	// Download behavior
	NumChunks  int
	MaxRetries int

	// HTTP timeouts
	DialTimeout           time.Duration
	KeepAlive             time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	IdleConnTimeout       time.Duration
	ExpectContinueTimeout time.Duration
	MaxIdleConns          int

	// TLS
	InsecureSkipVerify bool
}

// Default returns a Config populated with sensible defaults.
func Default() *Config {
	return &Config{
		QueueFile:     "queue.txt",
		CompletedFile: "completed.txt",
		OutputDir:     "./downloads",

		SingleURL: "",

		NumChunks:  8,
		MaxRetries: 2,

		DialTimeout:           10 * time.Second,
		KeepAlive:             30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		MaxIdleConns:          50,

		InsecureSkipVerify: false,
	}
}

// Validate checks that the Config has no obviously wrong values.
// Call this once after building the config (e.g. after flag parsing).
func (c *Config) Validate() error {
	if c.OutputDir == "" {
		return errors.New("output directory cannot be empty")
	}
	if c.QueueFile == "" {
		return errors.New("queue file path cannot be empty")
	}
	if c.NumChunks < 1 {
		return errors.New("NumChunks must be at least 1")
	}
	if c.MaxRetries < 0 {
		return errors.New("MaxRetries cannot be negative")
	}
	return nil
}
