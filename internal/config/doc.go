// Package config parses koffr.yaml strictly and is the only reader of the environment: every secret reaches the rest of the program through a value it validated at start-up.
package config
