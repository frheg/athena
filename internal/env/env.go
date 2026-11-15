// Package env reads configuration from environment variables. Every part of Athena is configured this way only.
package env

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Str returns the variable, or def when it is unset or empty.
func Str(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// Must returns the variable or an error naming it.
func Must(key string) (string, error) {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("%s is not set", key)
}

// Int returns the variable as an int, or def.
func Int(key string, def int) int {
	if v, err := strconv.Atoi(Str(key, "")); err == nil {
		return v
	}
	return def
}

// Dur returns the variable as a duration ("90s", "5m"), or def.
func Dur(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(Str(key, "")); err == nil {
		return v
	}
	return def
}

// Bool is true for 1, true, yes, on.
func Bool(key string, def bool) bool {
	switch strings.ToLower(Str(key, "")) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

// Pairs parses "a=1,b=2" (or "a:1,b:2") into a map. Used for per-caller tokens and server lists.
func Pairs(key string) map[string]string {
	out := map[string]string{}
	for _, p := range strings.Split(Str(key, ""), ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		i := strings.IndexAny(p, "=:")
		if i <= 0 {
			continue
		}
		// a URL value contains ':' itself, so split on the first '=' when there is one
		if j := strings.Index(p, "="); j > 0 {
			i = j
		}
		out[strings.TrimSpace(p[:i])] = strings.TrimSpace(p[i+1:])
	}
	return out
}
