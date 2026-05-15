package config

import (
	"testing"
)

// TestExpandAnthropicAPIKey — matrix of common cases for the startup-time
// secret resolver. Names matches the captain's spec.
func TestExpandAnthropicAPIKey(t *testing.T) {
	t.Setenv("HOMA_TEST_KEY", "sk-real-12345")

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"dollar-var-set", "$HOMA_TEST_KEY", "sk-real-12345"},
		{"brace-var-set", "${HOMA_TEST_KEY}", "sk-real-12345"},
		{"missing-var", "$HOMA_UNDEFINED_VAR_XYZ", ""},
		{"literal-key", "sk-literal-key", "sk-literal-key"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExpandSecret(tc.raw)
			if got != tc.want {
				t.Errorf("ExpandSecret(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}
