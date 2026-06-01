package sandbox

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Default binary name; main.go can override via config to support absolute
// paths or alternate runtimes (e.g. docker shim).
const defaultPodmanBin = "podman"

// Argv flag literals — centralised here so any reflag (e.g. --memory=)
// changes are a single edit.
const (
	flagWorkspaceMount = "/workspace:Z"     // ":Z" relabels for SELinux; no-op elsewhere.
	flagNousContainerPort       = "9000"
	flagPreviewContainerPort    = "5173"
	flagCodeServerContainerPort = "8443"
)

// PodmanManager implements Manager by shelling out to `podman` via Runner.
type PodmanManager struct {
	Bin    string // path / name of the podman binary (default: "podman")
	Runner Runner
}

// NewPodmanManager builds a PodmanManager. An empty bin defaults to "podman";
// a nil runner defaults to ExecRunner{}.
func NewPodmanManager(bin string, runner Runner) *PodmanManager {
	if bin == "" {
		bin = defaultPodmanBin
	}
	if runner == nil {
		runner = ExecRunner{}
	}
	return &PodmanManager{Bin: bin, Runner: runner}
}

// Ensure starts the container if not already running. Argv mirrors mvp.md §8.
func (pm *PodmanManager) Ensure(ctx context.Context, spec Spec) error {
	running, err := pm.IsRunning(ctx, spec.ContainerName)
	if err != nil {
		return fmt.Errorf("IsRunning: %w", err)
	}
	if running {
		return nil
	}
	args := buildRunArgs(spec)
	if _, err := pm.Runner.Run(ctx, pm.Bin, args...); err != nil {
		return fmt.Errorf("podman run %s: %w", spec.ContainerName, err)
	}
	return nil
}

// buildRunArgs constructs the `podman run …` argument list verbatim per
// mvp.md §8. Pulled out as a separate function so tests can assert on the
// slice directly.
//
// Flags vary by spec:
//   - NousPort > 0  → emit `-p` for nous; 0 skips (main sandbox uses 0).
//   - !NoAutoRemove → emit `--rm` (auto-remove after exit).
//   - All cases also emit `--replace`: if a container with the same name
//     already exists (e.g. stuck in `Created` after an interrupted start,
//     or a stopped corpse the mainsite watchdog wants to recreate), remove
//     it first. Without --replace a corpse blocks `podman run` with
//     "name already in use", and `--rm` alone DOES NOT clean up — it only
//     fires when the container exits naturally. Symptom seen in production:
//     orchestrator crashed mid-create → container in Created state →
//     subsequent EnsureRunning fails forever until manual `podman rm`.
func buildRunArgs(spec Spec) []string {
	args := []string{"run", "-d", "--replace"}
	if !spec.NoAutoRemove {
		args = append(args, "--rm")
	}
	args = append(args,
		"--name", spec.ContainerName,
		"-v", spec.WorktreePath+":"+flagWorkspaceMount,
	)
	// Extra mounts in slice order — caller controls ordering. Emitted before
	// ports/limits so the workspace mount stays adjacent to its kin in
	// `podman inspect` output.
	for _, m := range spec.Mounts {
		args = append(args, "-v", formatMount(m))
	}
	if spec.NousPort > 0 {
		args = append(args, "-p", fmt.Sprintf("127.0.0.1:%d:%s", spec.NousPort, flagNousContainerPort))
	}
	args = append(args,
		"-p", fmt.Sprintf("127.0.0.1:%d:%s", spec.PreviewPort, flagPreviewContainerPort),
	)
	if spec.CodeServerPort > 0 {
		args = append(args, "-p", fmt.Sprintf("127.0.0.1:%d:%s", spec.CodeServerPort, flagCodeServerContainerPort))
	}
	args = append(args,
		"--memory="+spec.MemoryLimit,
		"--cpus="+spec.CPULimit,
	)
	// Env vars in stable iteration order so tests can assert exact argv.
	for _, k := range sortedKeys(spec.Env) {
		args = append(args, "-e", k+"="+spec.Env[k])
	}
	args = append(args, spec.ImageRef)
	return args
}

// formatMount renders a Mount as the `src:dst[:ro]` form expected by `-v`.
func formatMount(m Mount) string {
	if m.ReadOnly {
		return m.Src + ":" + m.Dst + ":ro"
	}
	return m.Src + ":" + m.Dst
}

// IsRunning queries `podman inspect`. Returns (false, nil) when the
// container is absent — we treat a non-zero exit as "absent".
func (pm *PodmanManager) IsRunning(ctx context.Context, containerName string) (bool, error) {
	out, err := pm.Runner.Run(ctx, pm.Bin, "inspect", "-f", "{{.State.Running}}", containerName)
	if err != nil {
		if IsExitError(err) {
			return false, nil
		}
		return false, err
	}
	switch strings.TrimSpace(string(out)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("podman inspect: unexpected output %q", out)
	}
}

// Stop terminates the container. Absent container → success (idempotent).
// Implementation: ask IsRunning first; only call `podman stop` if there's
// actually something to stop. Cleaner than parsing stderr for "no such
// container" and structurally matches the idempotent contract.
func (pm *PodmanManager) Stop(ctx context.Context, containerName string) error {
	running, err := pm.IsRunning(ctx, containerName)
	if err != nil {
		return fmt.Errorf("Stop: IsRunning: %w", err)
	}
	if !running {
		return nil
	}
	_, err = pm.Runner.Run(ctx, pm.Bin, "stop", containerName)
	if err != nil {
		// Race: container may have stopped between IsRunning and stop.
		// Tolerate `*RunError` (process ran & exited); bubble harder
		// failures (binary missing, ctx canceled).
		if IsExitError(err) {
			return nil
		}
		return err
	}
	return nil
}

// Logs returns the last `lines` lines of the container's log output.
func (pm *PodmanManager) Logs(ctx context.Context, containerName string, lines int) ([]string, error) {
	out, err := pm.Runner.Run(ctx, pm.Bin, "logs", "--tail", strconv.Itoa(lines), containerName)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimRight(string(out), "\n")
	if trimmed == "" {
		return nil, nil
	}
	return strings.Split(trimmed, "\n"), nil
}

// sortedKeys returns map keys sorted lexically. Pulled out so the argv is
// deterministic for tests.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
