package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLoadEmptyAppliesAllDefaults — read a config.json with `{}` and
// assert every defaulted field lands at its expected value. This is the
// integration test for applyDefaults; catches regressions where a new
// field is added without a corresponding default (or a default is
// silently changed).
//
// Reading from a real file (not constructing a struct directly) so the
// JSON tag wiring is exercised too — an accidentally renamed JSON key
// would surface as a "still got zero" failure in the matching field.
func TestLoadEmptyAppliesAllDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write empty config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Identifiers (paths / binary names / image refs).
	checkString(t, "ListenAddr", cfg.ListenAddr, defaultListenAddr)
	checkString(t, "DataDir", cfg.DataDir, defaultDataDir)
	checkString(t, "BranchesDir", cfg.BranchesDir, defaultBranchesDir)
	checkString(t, "SiteTemplateDir", cfg.SiteTemplateDir, defaultSiteTemplateDir)
	checkString(t, "ImageRef", cfg.ImageRef, defaultImageRef)
	checkString(t, "PodmanBin", cfg.PodmanBin, defaultPodmanBin)
	checkString(t, "TailscaleBin", cfg.TailscaleBin, defaultTailscaleBin)
	checkString(t, "GitBin", cfg.GitBin, defaultGitBin)
	checkString(t, "ContainerMemory", cfg.ContainerMemory, defaultContainerMemory)
	checkString(t, "ContainerCPUs", cfg.ContainerCPUs, defaultContainerCPUs)

	// Readiness probe.
	checkInt(t, "ReadinessTimeoutSec", cfg.ReadinessTimeoutSec, defaultReadinessTimeoutSec)
	checkInt(t, "ReadinessIntervalMS", cfg.ReadinessIntervalMS, defaultReadinessIntervalMS)

	// Lifecycle defaults — the values backing the compact-then-stop story.
	// Changing these affects user-visible behavior; the test pins the
	// numbers so a silent regression in applyDefaults gets caught.
	checkInt(t, "IdleAfterMinutes", cfg.IdleAfterMinutes, defaultIdleAfterMinutes)
	checkInt(t, "GCIntervalSeconds", cfg.GCIntervalSeconds, defaultGCIntervalSeconds)
	checkInt(t, "IdleWarningSeconds", cfg.IdleWarningSeconds, defaultIdleWarningSeconds)
	checkInt(t, "CompactTimeoutSeconds", cfg.CompactTimeoutSeconds, defaultCompactTimeoutSeconds)
	checkInt64(t, "CompactMinTokens", cfg.CompactMinTokens, defaultCompactMinTokens)

	// User configs path defaults to <DataDir>/configs/, and the template
	// path is shipped relative to sandbox/.
	wantConfigs := filepath.Join(defaultDataDir, "configs")
	checkString(t, "UserConfigsDir", cfg.UserConfigsDir, wantConfigs)
	checkString(t, "NousConfigTemplate", cfg.NousConfigTemplate, defaultNousConfigTemplate)

	// Mainsite port — separate from user pool.
	checkInt(t, "MainSiteHostPort", cfg.MainSiteHostPort, defaultMainSiteHostPort)

	// CookieSecure is a pointer-bool so applyDefaults can distinguish
	// "unset" from "set to false". An empty config means production-style
	// secure cookies on by default.
	if cfg.CookieSecure == nil {
		t.Error("CookieSecure: got nil (default not applied)")
	} else if !*cfg.CookieSecure {
		t.Error("CookieSecure default: got false, want true")
	}

	// MainSiteEnabled is also a pointer-bool. Default is `cfg.UsePodman` —
	// which is false in an all-zero config, so MainSiteEnabled defaults to false.
	if cfg.MainSiteEnabled == nil {
		t.Error("MainSiteEnabled: got nil (default not applied)")
	} else if *cfg.MainSiteEnabled != false {
		t.Errorf("MainSiteEnabled default with UsePodman=false: got true, want false")
	}
}

// TestMainSiteEnabledTracksUsePodman — when use_podman is true, the
// MainSiteEnabled default flips to true. Verifies the cross-field default.
func TestMainSiteEnabledTracksUsePodman(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"use_podman": true}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MainSiteEnabled == nil || !*cfg.MainSiteEnabled {
		t.Errorf("MainSiteEnabled with UsePodman=true: got %v, want true",
			cfg.MainSiteEnabled)
	}
}

// TestExplicitValuesNotOverridden — values set in JSON aren't clobbered
// by the defaults. Spot-checks the three flavors of override: string,
// int, and pointer-bool.
func TestExplicitValuesNotOverridden(t *testing.T) {
	const body = `{
		"listen_addr": ":9999",
		"idle_after_minutes": 7,
		"compact_min_tokens": 12345,
		"cookie_secure": false,
		"main_site_enabled": false
	}`
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddr != ":9999" {
		t.Errorf("ListenAddr: got %q, want :9999", cfg.ListenAddr)
	}
	if cfg.IdleAfterMinutes != 7 {
		t.Errorf("IdleAfterMinutes: got %d, want 7", cfg.IdleAfterMinutes)
	}
	if cfg.CompactMinTokens != 12345 {
		t.Errorf("CompactMinTokens: got %d, want 12345", cfg.CompactMinTokens)
	}
	if cfg.CookieSecure == nil || *cfg.CookieSecure != false {
		t.Errorf("CookieSecure: got %v, want explicit false", cfg.CookieSecure)
	}
	if cfg.MainSiteEnabled == nil || *cfg.MainSiteEnabled != false {
		t.Errorf("MainSiteEnabled: got %v, want explicit false", cfg.MainSiteEnabled)
	}
}

// TestLifecycleDurationsAreSensible — sanity that the defaults are
// internally coherent: warning window < idle threshold, compact timeout
// can fit inside the warning-to-stop interval, etc. Catches a future
// "I bumped one default but forgot the other" regression.
func TestLifecycleDurationsAreSensible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte("{}"), 0o644)
	cfg, _ := Load(path)

	idle := time.Duration(cfg.IdleAfterMinutes) * time.Minute
	warn := time.Duration(cfg.IdleWarningSeconds) * time.Second
	compactTimeout := time.Duration(cfg.CompactTimeoutSeconds) * time.Second

	if warn >= idle {
		t.Errorf("warning window (%v) >= idle threshold (%v); warning would never fire", warn, idle)
	}
	if compactTimeout >= idle {
		t.Errorf("compact timeout (%v) >= idle threshold (%v); compact could outlast next tick", compactTimeout, idle)
	}
}

// helpers ----------------------------------------------------------------

func checkString(t *testing.T, name, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %q, want %q", name, got, want)
	}
}
func checkInt(t *testing.T, name string, got, want int) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %d, want %d", name, got, want)
	}
}
func checkInt64(t *testing.T, name string, got, want int64) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %d, want %d", name, got, want)
	}
}
