package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// LoadDotEnv reads a simple KEY=VALUE .env file and sets each variable in
// the process environment, without overwriting variables that are already
// set (so real environment variables / systemd EnvironmentFile entries
// always take precedence over the .env file).
//
// Supported syntax per line:
//
//	KEY=value
//	KEY="quoted value with spaces or # not treated as comment"
//	KEY='single quoted value'
//	# full-line comments
//	blank lines are ignored
//
// If path does not exist, this is a no-op (not an error) — the app is
// expected to work from real environment variables alone in production.
func LoadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("config: open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := splitKeyValue(line)
		if !ok {
			return fmt.Errorf("config: %s:%d: invalid line (expected KEY=VALUE): %q", path, lineNum, line)
		}

		if _, alreadySet := os.LookupEnv(key); alreadySet {
			continue // real env vars win over .env file values
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("config: set %s: %w", key, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("config: read %s: %w", path, err)
	}
	return nil
}

// splitKeyValue parses a single "KEY=VALUE" line, stripping matching
// surrounding quotes from the value if present.
func splitKeyValue(line string) (key, value string, ok bool) {
	idx := strings.IndexByte(line, '=')
	if idx <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	value = strings.TrimSpace(line[idx+1:])

	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'') {
			value = value[1 : len(value)-1]
		}
	}
	return key, value, true
}
