//go:build darwin

package endpoint

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

const darwinProcessTimeLayout = "Mon Jan _2 15:04:05 2006"

type observedProcess struct {
	pid, parentPID, uid int
	started             time.Time
	command             string
}

func collectRuntimeIdentityObservations(ctx context.Context, options Options) []discovery.RuntimeIdentityObservation {
	productNames := map[string]map[string]bool{}
	for _, signature := range options.Pack.Runtimes {
		if signature.ID != "codex" {
			continue
		}
		productNames[signature.ID] = map[string]bool{}
		for _, name := range signature.Processes {
			productNames[signature.ID][strings.ToLower(name)] = true
		}
	}
	if len(productNames) == 0 {
		return nil
	}
	processes, err := darwinProcesses(ctx)
	if err != nil {
		return nil
	}
	observations := []discovery.RuntimeIdentityObservation{}
	for _, process := range processes {
		path := resolveProcessExecutable(ctx, process)
		name := strings.ToLower(filepath.Base(path))
		if name == "" {
			name = strings.ToLower(filepath.Base(process.command))
		}
		for productID, names := range productNames {
			if !names[name] {
				continue
			}
			observation, observationErr := buildRuntimeIdentityObservation(ctx, options, productID, process, path, processes)
			if observationErr == nil {
				observations = append(observations, observation)
			}
		}
	}
	return observations
}

func darwinProcesses(ctx context.Context) (map[int]observedProcess, error) {
	output, err := exec.CommandContext(ctx, "/bin/ps", "-axo", "pid=,ppid=,uid=,lstart=,comm=").Output()
	if err != nil {
		return nil, err
	}
	result := map[int]observedProcess{}
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 9 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		parentPID, parentErr := strconv.Atoi(fields[1])
		uid, uidErr := strconv.Atoi(fields[2])
		started, startErr := time.ParseInLocation(darwinProcessTimeLayout, strings.Join(fields[3:8], " "), time.Local)
		if pidErr != nil || parentErr != nil || uidErr != nil || startErr != nil {
			continue
		}
		result[pid] = observedProcess{pid: pid, parentPID: parentPID, uid: uid, started: started.UTC(), command: strings.Join(fields[8:], " ")}
	}
	return result, scanner.Err()
}

func resolveProcessExecutable(ctx context.Context, process observedProcess) string {
	if filepath.IsAbs(process.command) {
		if resolved, err := filepath.EvalSymlinks(process.command); err == nil {
			return resolved
		}
	}
	output, err := exec.CommandContext(ctx, "/usr/sbin/lsof", "-a", "-p", strconv.Itoa(process.pid), "-d", "txt", "-Fn").Output()
	if err == nil {
		for _, line := range strings.Split(string(output), "\n") {
			if strings.HasPrefix(line, "n/") {
				if resolved, resolveErr := filepath.EvalSymlinks(strings.TrimPrefix(line, "n")); resolveErr == nil {
					return resolved
				}
			}
		}
	}
	return process.command
}

func buildRuntimeIdentityObservation(ctx context.Context, options Options, productID string, process observedProcess, path string, processes map[int]observedProcess) (discovery.RuntimeIdentityObservation, error) {
	if !filepath.IsAbs(path) {
		return discovery.RuntimeIdentityObservation{}, fmt.Errorf("runtime executable path is unresolved")
	}
	executableDigest, err := fileSHA256(path)
	if err != nil {
		return discovery.RuntimeIdentityObservation{}, err
	}
	observedAt := time.Now().UTC().Format(time.RFC3339Nano)
	startTime := process.started.Format(time.RFC3339Nano)
	observationID := discovery.StableID(options.OrganizationID, discovery.KindRuntime, fmt.Sprintf("%s:%s:%d:%s", options.TargetID, productID, process.pid, startTime))
	ancestry := []discovery.ProcessAncestor{}
	current := process
	visited := map[int]bool{}
	for depth := 0; depth < 64 && current.pid > 0 && !visited[current.pid]; depth++ {
		visited[current.pid] = true
		ancestorPath := resolveProcessExecutable(ctx, current)
		ancestry = append(ancestry, discovery.ProcessAncestor{
			PID: current.pid, StartTime: current.started.Format(time.RFC3339Nano),
			ExecutablePathDigest: discovery.HashLocator(options.OrganizationID, ancestorPath),
		})
		parent, ok := processes[current.parentPID]
		if !ok {
			break
		}
		current = parent
	}
	signing := inspectMacOSSigning(ctx, path)
	version := inspectMacOSVersion(ctx, path)
	observation := discovery.RuntimeIdentityObservation{
		SchemaVersion: discovery.RuntimeIdentityObservationVersion, ObservationID: observationID,
		TargetID: options.TargetID, ProductID: productID, Platform: "darwin", ObservedAt: observedAt,
		PID: process.pid, StartTime: startTime, ParentPID: process.parentPID, Ancestry: ancestry,
		EffectiveUID: process.uid, Version: version, ExecutablePathDigest: discovery.HashLocator(options.OrganizationID, path),
		ExecutableSHA256: executableDigest, MacOSSigning: signing,
		Evidence: []discovery.RuntimeIdentityFact{
			{Fact: "process", Method: "ps", ObservedAt: observedAt},
			{Fact: "ancestry", Method: "ps", ObservedAt: observedAt},
			{Fact: "executable", Method: "lsof_and_sha256", ObservedAt: observedAt},
			{Fact: "macos_signing", Method: "codesign", ObservedAt: observedAt},
		},
	}
	return observation, observation.Validate()
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(digest.Sum(nil)), nil
}

func inspectMacOSSigning(ctx context.Context, path string) discovery.MacOSSigningFacts {
	verified := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--strict", "--verbose=2", path).Run() == nil
	output, _ := exec.CommandContext(ctx, "/usr/bin/codesign", "-d", "--verbose=4", "-r-", path).CombinedOutput()
	facts := discovery.MacOSSigningFacts{SignatureVerified: verified, SigningMetadataEvidenceDigest: discovery.ContentHash(output)}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "TeamIdentifier="):
			facts.PublisherTeamID = strings.TrimPrefix(line, "TeamIdentifier=")
		case strings.HasPrefix(line, "Identifier="):
			facts.SigningIdentifier = strings.TrimPrefix(line, "Identifier=")
		case strings.HasPrefix(line, "CDHash="):
			facts.CDHash = strings.TrimPrefix(line, "CDHash=")
		case strings.HasPrefix(line, "designated =>"):
			facts.DesignatedRequirementDigest = discovery.ContentHash([]byte(strings.TrimSpace(strings.TrimPrefix(line, "designated =>"))))
		case strings.HasPrefix(line, "flags=") && strings.Contains(strings.ToLower(line), "runtime"):
			facts.HardenedRuntime = true
		}
	}
	return facts
}

func inspectMacOSVersion(ctx context.Context, path string) string {
	output, err := exec.CommandContext(ctx, "/usr/bin/mdls", "-raw", "-name", "kMDItemVersion", path).Output()
	if err != nil {
		return "unknown"
	}
	version := strings.Trim(strings.TrimSpace(string(output)), `"`)
	if version == "" || version == "(null)" || len(version) > 128 {
		return "unknown"
	}
	return version
}
