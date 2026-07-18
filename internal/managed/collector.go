package managed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	lensconfig "github.com/barrikadelabs/barrikade-lens/internal/config"
	"github.com/barrikadelabs/barrikade-lens/internal/detector"
	"github.com/barrikadelabs/barrikade-lens/internal/hubclient"
	"github.com/barrikadelabs/barrikade-lens/internal/scanner/endpoint"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"github.com/fsnotify/fsnotify"
)

const (
	debounceInterval      = 10 * time.Second
	maxEventDelay         = 60 * time.Second
	processReconciliation = 15 * time.Minute
	fullReconciliation    = 24 * time.Hour
)

type Runner struct {
	ConfigPath string
	Version    string
	Client     *hubclient.Client
}

func (r Runner) Run(ctx context.Context) error {
	cfg, err := lensconfig.Load(r.ConfigPath)
	if err != nil {
		return fmt.Errorf("load managed collector configuration: %w", err)
	}
	pack, err := detector.Builtin()
	if err != nil {
		return err
	}
	if r.Client == nil {
		r.Client = hubclient.New(r.Version)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	lastDigest := ""
	scanAndUpload := func(full bool) error {
		cfg.Sequence++
		if err := lensconfig.Save(r.ConfigPath, cfg); err != nil {
			return err
		}
		profiles := managedProfiles(home)
		snapshot, scanErr := scanProfiles(ctx, cfg.OrganizationID, cfg.SourceID, pack, profiles, r.Version)
		if scanErr != nil {
			return scanErr
		}
		snapshot.Sequence, snapshot.Full = cfg.Sequence, full
		digest, err := snapshot.Digest()
		if err != nil {
			return err
		}
		if digest == lastDigest && !full {
			return nil
		}
		var uploadErr error
		for attempt := 0; attempt < 5; attempt++ {
			if _, uploadErr = r.Client.Upload(ctx, r.ConfigPath, &cfg, snapshot); uploadErr == nil {
				break
			}
			delay := time.Duration(1<<attempt) * time.Second
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		if uploadErr != nil {
			return fmt.Errorf("upload discovery snapshot after retries: %w", uploadErr)
		}
		lastDigest = digest
		return nil
	}
	if err := scanAndUpload(true); err != nil {
		return err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()
	for _, profile := range managedProfiles(home) {
		for _, root := range watchRoots(pack, profile.Home) {
			_ = watcher.Add(root)
		}
	}
	processTicker := time.NewTicker(processReconciliation)
	defer processTicker.Stop()
	fullTimer := time.NewTimer(jitteredFullInterval())
	defer fullTimer.Stop()
	var debounceTimer, deadlineTimer *time.Timer
	var debounce, deadline <-chan time.Time
	resetEvents := func() {
		if deadlineTimer == nil {
			deadlineTimer = time.NewTimer(maxEventDelay)
			deadline = deadlineTimer.C
		}
		if debounceTimer == nil {
			debounceTimer = time.NewTimer(debounceInterval)
		} else {
			if !debounceTimer.Stop() {
				select {
				case <-debounceTimer.C:
				default:
				}
			}
			debounceTimer.Reset(debounceInterval)
		}
		debounce = debounceTimer.C
	}
	clearEvents := func() {
		if debounceTimer != nil {
			debounceTimer.Stop()
		}
		if deadlineTimer != nil {
			deadlineTimer.Stop()
		}
		debounceTimer, deadlineTimer, debounce, deadline = nil, nil, nil, nil
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) != 0 {
				resetEvents()
			}
		case <-debounce:
			clearEvents()
			if err := scanAndUpload(false); err != nil {
				slog.Warn("incremental discovery scan failed; collector will retry", "error", err)
			}
		case <-deadline:
			clearEvents()
			if err := scanAndUpload(false); err != nil {
				slog.Warn("deadline discovery scan failed; collector will retry", "error", err)
			}
		case <-processTicker.C:
			if err := scanAndUpload(false); err != nil {
				slog.Warn("process and listener reconciliation failed; collector will retry", "error", err)
			}
		case <-fullTimer.C:
			if err := scanAndUpload(true); err != nil {
				slog.Warn("full discovery reconciliation failed; collector will retry", "error", err)
			}
			fullTimer.Reset(jitteredFullInterval())
		case watchErr, ok := <-watcher.Errors:
			if ok && watchErr != nil && !errors.Is(watchErr, os.ErrClosed) {
				return watchErr
			}
		}
	}
}

type profile struct {
	Home     string
	Username string
}

func scanProfiles(ctx context.Context, organizationID, sourceID string, pack detector.Pack, profiles []profile, version string) (discovery.Snapshot, error) {
	var combined discovery.Snapshot
	var firstError error
	scanned := 0
	for _, candidate := range profiles {
		snapshot, err := endpoint.Scan(ctx, endpoint.Options{
			OrganizationID: organizationID, SourceID: sourceID, HomeDir: candidate.Home, Username: candidate.Username, Pack: pack,
		})
		if err != nil {
			if firstError == nil {
				firstError = err
			}
			continue
		}
		scanned++
		if combined.SnapshotID == "" {
			combined = snapshot
		} else if err := discovery.MergeSnapshots(&combined, snapshot); err != nil {
			return discovery.Snapshot{}, err
		}
	}
	if scanned == 0 {
		return discovery.Snapshot{}, firstError
	}
	combined.Collector.Mode = "managed"
	combined.Collector.Version = version
	if combined.Scope.Attributes == nil {
		combined.Scope.Attributes = map[string]string{}
	}
	combined.Scope.Attributes["user_profiles_scanned"] = strconv.Itoa(scanned)
	if firstError != nil {
		combined.Coverage.Partial = true
		combined.Errors = append(combined.Errors, discovery.ScanError{DetectorID: "lens.endpoint.profiles", Code: "profile_scan_failed", Message: "One or more local user profiles could not be inspected"})
	}
	combined.Normalize()
	return combined, combined.Validate()
}

func managedProfiles(currentHome string) []profile {
	currentUsername := filepath.Base(filepath.Clean(currentHome))
	systemAccount := false
	if account, err := user.Current(); err == nil {
		currentUsername = account.Username
		normalized := strings.ToLower(account.Username)
		systemAccount = normalized == "root" || normalized == "system" || strings.HasSuffix(normalized, `\system`)
	}
	current := profile{Home: filepath.Clean(currentHome), Username: currentUsername}
	if !systemAccount {
		return []profile{current}
	}
	root := ""
	switch runtime.GOOS {
	case "darwin":
		root = "/Users"
	case "linux":
		root = "/home"
	case "windows":
		drive := os.Getenv("SystemDrive")
		if drive == "" {
			drive = `C:`
		}
		root = filepath.Join(drive+string(filepath.Separator), "Users")
	}
	return profilesUnder(current, root)
}

func profilesUnder(current profile, root string) []profile {
	result := []profile{current}
	seen := map[string]bool{strings.ToLower(filepath.Clean(current.Home)): true}
	entries, err := os.ReadDir(root)
	if err != nil {
		return result
	}
	ignored := map[string]bool{"shared": true, "public": true, "default": true, "default user": true, "all users": true, "lost+found": true}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || ignored[strings.ToLower(name)] || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		home := filepath.Clean(filepath.Join(root, name))
		key := strings.ToLower(home)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, profile{Home: home, Username: name})
	}
	return result
}

func watchRoots(pack detector.Pack, home string) []string {
	seen := map[string]bool{}
	roots := []string{}
	add := func(path string) {
		path = expand(path, home)
		if path == "" {
			return
		}
		for {
			info, err := os.Stat(path)
			if err == nil && info.IsDir() {
				if !seen[path] {
					seen[path] = true
					roots = append(roots, path)
				}
				return
			}
			parent := filepath.Dir(path)
			if parent == path || path == home {
				return
			}
			path = parent
		}
	}
	for _, runtime := range pack.Runtimes {
		for _, path := range runtime.Paths {
			add(path)
		}
		for _, root := range runtime.SkillRoots {
			add(root)
		}
		for _, config := range runtime.Configs {
			add(filepath.Dir(expand(config.Path, home)))
		}
	}
	for _, cache := range pack.ModelCaches {
		for _, path := range cache.Paths {
			add(path)
		}
	}
	return roots
}

func expand(path, home string) string {
	if path == "~" {
		return home
	}
	if len(path) > 2 && (path[:2] == "~/" || path[:2] == `~\`) {
		return filepath.Join(home, path[2:])
	}
	path = strings.ReplaceAll(path, "$HOME", home)
	path = strings.ReplaceAll(path, "${HOME}", home)
	path = strings.ReplaceAll(path, "%USERPROFILE%", home)
	for _, variable := range []string{"APPDATA", "LOCALAPPDATA"} {
		placeholder := "%" + variable + "%"
		if strings.Contains(strings.ToUpper(path), placeholder) {
			value := os.Getenv(variable)
			if value == "" {
				return ""
			}
			path = strings.ReplaceAll(path, placeholder, value)
		}
	}
	return filepath.Clean(path)
}
func jitteredFullInterval() time.Duration {
	return fullReconciliation + time.Duration(rand.Int64N(int64(2*time.Hour))) - time.Hour
}
