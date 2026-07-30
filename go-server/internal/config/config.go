package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	DatabaseURL         string
	Port                string
	MaxBodyBytes        int64
	MaxNodes            int
	MaxDepth            int
	ReadTimeout         time.Duration
	WriteTimeout        time.Duration
	IdleTimeout         time.Duration
	OperationTimeout    time.Duration
	DatabaseLockTimeout time.Duration
	ShutdownTimeout     time.Duration
}

type lookupFunc func(string) (string, bool)

func Load() (Config, error) {
	return load(os.LookupEnv)
}

func load(lookup lookupFunc) (Config, error) {
	databaseURL, ok := lookup("DATABASE_URL")
	if !ok || databaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}

	config := Config{
		DatabaseURL:         databaseURL,
		Port:                valueOrDefault(lookup, "PORT", "8080"),
		MaxBodyBytes:        10 << 20,
		MaxNodes:            100_000,
		MaxDepth:            1_000,
		ReadTimeout:         15 * time.Second,
		WriteTimeout:        30 * time.Second,
		IdleTimeout:         60 * time.Second,
		OperationTimeout:    20 * time.Second,
		DatabaseLockTimeout: 5 * time.Second,
		ShutdownTimeout:     30 * time.Second,
	}

	var err error
	if config.MaxBodyBytes, err = int64Value(lookup, "MAX_BODY_BYTES", config.MaxBodyBytes); err != nil {
		return Config{}, err
	}
	if config.MaxNodes, err = intValue(lookup, "HIERARCHY_MAX_NODES", config.MaxNodes); err != nil {
		return Config{}, err
	}
	if config.MaxDepth, err = intValue(lookup, "HIERARCHY_MAX_DEPTH", config.MaxDepth); err != nil {
		return Config{}, err
	}
	if config.ReadTimeout, err = durationValue(lookup, "HTTP_READ_TIMEOUT", config.ReadTimeout); err != nil {
		return Config{}, err
	}
	if config.WriteTimeout, err = durationValue(lookup, "HTTP_WRITE_TIMEOUT", config.WriteTimeout); err != nil {
		return Config{}, err
	}
	if config.IdleTimeout, err = durationValue(lookup, "HTTP_IDLE_TIMEOUT", config.IdleTimeout); err != nil {
		return Config{}, err
	}
	if config.OperationTimeout, err = durationValue(lookup, "OPERATION_TIMEOUT", config.OperationTimeout); err != nil {
		return Config{}, err
	}
	if config.DatabaseLockTimeout, err = durationValue(lookup, "DATABASE_LOCK_TIMEOUT", config.DatabaseLockTimeout); err != nil {
		return Config{}, err
	}
	if config.ShutdownTimeout, err = durationValue(lookup, "SHUTDOWN_TIMEOUT", config.ShutdownTimeout); err != nil {
		return Config{}, err
	}
	return config, nil
}

func valueOrDefault(lookup lookupFunc, key, fallback string) string {
	if value, ok := lookup(key); ok && value != "" {
		return value
	}
	return fallback
}

func intValue(lookup lookupFunc, key string, fallback int) (int, error) {
	value, ok := lookup(key)
	if !ok || value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return parsed, nil
}

func int64Value(lookup lookupFunc, key string, fallback int64) (int64, error) {
	value, ok := lookup(key)
	if !ok || value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return parsed, nil
}

func durationValue(lookup lookupFunc, key string, fallback time.Duration) (time.Duration, error) {
	value, ok := lookup(key)
	if !ok || value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return parsed, nil
}
