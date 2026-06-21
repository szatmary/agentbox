package auth

import "os"

// OSEnv reads from the process environment.
type OSEnv struct{}

// Getenv implements [Env].
func (OSEnv) Getenv(key string) string { return os.Getenv(key) }

// MapEnv is an in-memory [Env] for tests.
type MapEnv map[string]string

// Getenv implements [Env].
func (m MapEnv) Getenv(key string) string { return m[key] }
