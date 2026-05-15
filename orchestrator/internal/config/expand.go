package config

import "os"

// ExpandSecret expands $VAR / ${VAR} references in raw using os.ExpandEnv.
// Unknown variables expand to "". Plain literals (no `$`) are returned
// unchanged.
//
// Used at orchestrator startup to resolve secrets like
//   "anthropic_api_key": "$ANTHROPIC_API_KEY"
// from config.json without baking the secret into the file.
func ExpandSecret(raw string) string {
	if raw == "" {
		return ""
	}
	return os.ExpandEnv(raw)
}
