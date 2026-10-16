// Package config loads the single YAML configuration file, resolves secrets from the
// environment variables it names, applies defaults in one place and validates the result.
// It also computes which media types each role can read from the declared capabilities.
package config
