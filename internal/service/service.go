package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const serviceName = "barrikade-lens"

type State string

const (
	StateRunning      State = "running"
	StateStopped      State = "stopped"
	StateNotInstalled State = "not-installed"
	StateUnknown      State = "unknown"
)

type Status struct {
	State          State
	Detail         string
	DefinitionPath string
}

func Install(ctx context.Context, executable, configPath string) (Status, error) {
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return Status{}, err
		}
	}
	executable, _ = filepath.Abs(executable)
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		stagedPath := filepath.Join(filepath.Dir(configPath), "bin", managedExecutableName())
		if runtime.GOOS == "windows" {
			if _, err := os.Stat(stagedPath); err == nil {
				executable = stagedPath
				return installWindows(ctx, executable, configPath)
			}
		}
		staged, err := stageExecutable(executable, configPath)
		if err != nil {
			return Status{}, fmt.Errorf("stage managed collector executable: %w", err)
		}
		executable = staged
	}
	switch runtime.GOOS {
	case "darwin":
		return installLaunchAgent(ctx, executable, configPath)
	case "linux":
		return installSystemd(ctx, executable, configPath)
	case "windows":
		return installWindows(ctx, executable, configPath)
	default:
		return Status{}, fmt.Errorf("service installation is unsupported on %s", runtime.GOOS)
	}
}

func stageExecutable(source, configPath string) (string, error) {
	directory := filepath.Join(filepath.Dir(configPath), "bin")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	destination := filepath.Join(directory, managedExecutableName())
	temporary, err := os.CreateTemp(directory, serviceName+"-*")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	input, err := os.Open(source)
	if err != nil {
		temporary.Close()
		return "", err
	}
	_, copyErr := io.Copy(temporary, input)
	closeInputErr := input.Close()
	chmodErr := temporary.Chmod(0o755)
	closeOutputErr := temporary.Close()
	for _, operationErr := range []error{copyErr, closeInputErr, chmodErr, closeOutputErr} {
		if operationErr != nil {
			return "", operationErr
		}
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return "", err
	}
	return destination, nil
}

func GetStatus(ctx context.Context) Status {
	switch runtime.GOOS {
	case "darwin":
		path, domain, _ := launchdTarget()
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return Status{State: StateNotInstalled, DefinitionPath: path}
		}
		output, err := exec.CommandContext(ctx, "launchctl", "print", domain+"/com.barrikade.lens").CombinedOutput()
		if err == nil && launchdRunning(string(output)) {
			return Status{State: StateRunning, DefinitionPath: path}
		}
		return Status{State: StateStopped, Detail: strings.TrimSpace(string(output)), DefinitionPath: path}
	case "linux":
		path, system, _ := systemdTarget()
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return Status{State: StateNotInstalled, DefinitionPath: path}
		}
		arguments := []string{"is-active", serviceName}
		if !system {
			arguments = append([]string{"--user"}, arguments...)
		}
		output, _ := exec.CommandContext(ctx, "systemctl", arguments...).Output()
		if strings.TrimSpace(string(output)) == "active" {
			return Status{State: StateRunning, DefinitionPath: path}
		}
		return Status{State: StateStopped, DefinitionPath: path}
	case "windows":
		output, err := exec.CommandContext(ctx, "sc.exe", "query", "BarrikadeLens").CombinedOutput()
		if err != nil && strings.Contains(string(output), "1060") {
			return Status{State: StateNotInstalled}
		}
		if strings.Contains(string(output), "RUNNING") {
			return Status{State: StateRunning}
		}
		return Status{State: StateStopped, Detail: strings.TrimSpace(string(output))}
	default:
		return Status{State: StateUnknown, Detail: "unsupported platform"}
	}
}

func Uninstall(ctx context.Context) error {
	switch runtime.GOOS {
	case "darwin":
		path, domain, _ := launchdTarget()
		_ = exec.CommandContext(ctx, "launchctl", "bootout", domain, path).Run()
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	case "linux":
		path, system, _ := systemdTarget()
		arguments := []string{"disable", "--now", serviceName}
		if !system {
			arguments = append([]string{"--user"}, arguments...)
		}
		_ = exec.CommandContext(ctx, "systemctl", arguments...).Run()
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		arguments = []string{"daemon-reload"}
		if !system {
			arguments = append([]string{"--user"}, arguments...)
		}
		return exec.CommandContext(ctx, "systemctl", arguments...).Run()
	case "windows":
		_ = exec.CommandContext(ctx, "sc.exe", "stop", "BarrikadeLens").Run()
		return exec.CommandContext(ctx, "sc.exe", "delete", "BarrikadeLens").Run()
	default:
		return fmt.Errorf("service uninstall is unsupported on %s", runtime.GOOS)
	}
}

func installLaunchAgent(ctx context.Context, executable, configPath string) (Status, error) {
	path, domain, err := launchdTarget()
	if err != nil {
		return Status{}, err
	}
	if privilegedAccount() {
		if err := removeUserLaunchAgents(ctx); err != nil {
			return Status{}, err
		}
	} else if _, err := os.Stat("/Library/LaunchDaemons/com.barrikade.lens.plist"); err == nil {
		return Status{}, fmt.Errorf("a system-wide Lens collector is already installed; use the administrator installation instead of creating a second user collector")
	} else if !errors.Is(err, os.ErrNotExist) {
		return Status{}, fmt.Errorf("inspect system-wide Lens collector: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Status{}, err
	}
	home := ""
	if privilegedAccount() {
		home = "/var/root"
	} else if value, homeErr := os.UserHomeDir(); homeErr == nil {
		home = value
	}
	definition := launchdDefinition(executable, configPath, home)
	if err := os.WriteFile(path, []byte(definition), 0o644); err != nil {
		return Status{}, err
	}
	_ = exec.CommandContext(ctx, "launchctl", "bootout", domain, path).Run()
	if output, err := exec.CommandContext(ctx, "launchctl", "bootstrap", domain, path).CombinedOutput(); err != nil {
		return Status{}, fmt.Errorf("launchctl bootstrap: %s: %w", strings.TrimSpace(string(output)), err)
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		status := GetStatus(ctx)
		if status.State == StateRunning {
			return status, nil
		}
		select {
		case <-ctx.Done():
			return Status{}, ctx.Err()
		case <-deadline.C:
			return status, fmt.Errorf("macOS collector did not reach the running state: %s", status.Detail)
		case <-ticker.C:
		}
	}
}

func removeUserLaunchAgents(ctx context.Context) error {
	accounts, err := candidateUserAccounts("/Users")
	if err != nil {
		return err
	}
	for _, account := range accounts {
		path, domain := launchdTargetForUser(account.HomeDir, account.Uid)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return fmt.Errorf("inspect conflicting user collector for %s: %w", account.Username, err)
		}
		_ = exec.CommandContext(ctx, "launchctl", "bootout", domain, path).Run()
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove conflicting user collector for %s: %w", account.Username, err)
		}
	}
	return nil
}

func removeUserSystemdUnits(ctx context.Context) error {
	accounts, err := candidateUserAccounts("/home")
	if err != nil {
		return err
	}
	for _, account := range accounts {
		path := filepath.Join(account.HomeDir, ".config", "systemd", "user", serviceName+".service")
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return fmt.Errorf("inspect conflicting user collector for %s: %w", account.Username, err)
		}
		// A user manager may not be reachable from a noninteractive root install.
		// Best-effort stopping plus removing the unit prevents it returning at the
		// next login; the collector run lock prevents overlap in the meantime.
		_ = exec.CommandContext(ctx, "runuser", "-u", account.Username, "--", "systemctl", "--user", "disable", "--now", serviceName).Run()
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove conflicting user collector for %s: %w", account.Username, err)
		}
	}
	return nil
}

func candidateUserAccounts(root string) ([]*user.User, error) {
	accounts := map[string]*user.User{}
	uid := strings.TrimSpace(os.Getenv("SUDO_UID"))
	if uid != "" && uid != "0" {
		if _, err := strconv.ParseUint(uid, 10, 32); err != nil {
			return nil, fmt.Errorf("invalid invoking user id %q", uid)
		}
		account, err := user.LookupId(uid)
		if err != nil {
			return nil, fmt.Errorf("resolve invoking user %s: %w", uid, err)
		}
		accounts[account.Uid] = account
	}
	entries, err := os.ReadDir(root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect local user homes: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		account, lookupErr := user.Lookup(entry.Name())
		if lookupErr != nil || account.Uid == "0" {
			continue
		}
		accounts[account.Uid] = account
	}
	result := make([]*user.User, 0, len(accounts))
	for _, account := range accounts {
		result = append(result, account)
	}
	return result, nil
}

func launchdTargetForUser(home, uid string) (string, string) {
	return filepath.Join(home, "Library", "LaunchAgents", "com.barrikade.lens.plist"), "gui/" + uid
}

func launchdDefinition(executable, configPath, home string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>com.barrikade.lens</string>
<key>ProgramArguments</key><array><string>%s</string><string>service</string><string>run</string><string>--config</string><string>%s</string></array>
<key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
<key>ProcessType</key><string>Background</string>
<key>EnvironmentVariables</key><dict><key>HOME</key><string>%s</string></dict>
<key>StandardOutPath</key><string>%s</string><key>StandardErrorPath</key><string>%s</string>
</dict></plist>`, xmlEscape(executable), xmlEscape(configPath), xmlEscape(home), xmlEscape(filepath.Join(filepath.Dir(configPath), "collector.log")), xmlEscape(filepath.Join(filepath.Dir(configPath), "collector.log")))
}

func installSystemd(ctx context.Context, executable, configPath string) (Status, error) {
	path, system, err := systemdTarget()
	if err != nil {
		return Status{}, err
	}
	if system {
		if err := removeUserSystemdUnits(ctx); err != nil {
			return Status{}, err
		}
	} else if _, err := os.Stat("/etc/systemd/system/" + serviceName + ".service"); err == nil {
		return Status{}, fmt.Errorf("a system-wide Lens collector is already installed; use the administrator installation instead of creating a second user collector")
	} else if !errors.Is(err, os.ErrNotExist) {
		return Status{}, fmt.Errorf("inspect system-wide Lens collector: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Status{}, err
	}
	definition := fmt.Sprintf(`[Unit]
Description=Barrikade Lens managed discovery collector
After=network-online.target

[Service]
ExecStart=%s service run --config %s
Restart=on-failure
RestartSec=10
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=default.target
`, systemdQuote(executable), systemdQuote(configPath))
	if err := os.WriteFile(path, []byte(definition), 0o644); err != nil {
		return Status{}, err
	}
	arguments := []string{"daemon-reload"}
	if !system {
		arguments = append([]string{"--user"}, arguments...)
	}
	if output, err := exec.CommandContext(ctx, "systemctl", arguments...).CombinedOutput(); err != nil {
		return Status{}, fmt.Errorf("systemctl daemon-reload: %s: %w", strings.TrimSpace(string(output)), err)
	}
	arguments = []string{"enable", "--now", serviceName}
	if !system {
		arguments = append([]string{"--user"}, arguments...)
	}
	if output, err := exec.CommandContext(ctx, "systemctl", arguments...).CombinedOutput(); err != nil {
		return Status{}, fmt.Errorf("systemctl enable: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return GetStatus(ctx), nil
}

func installWindows(ctx context.Context, executable, configPath string) (Status, error) {
	binPath := fmt.Sprintf(`"%s" service run --config "%s"`, executable, configPath)
	status := GetStatus(ctx)
	action := "create"
	arguments := []string{"create", "BarrikadeLens", "start=", "auto", "binPath=", binPath, "DisplayName=", "Barrikade Lens"}
	if status.State != StateNotInstalled {
		action = "update"
		if status.State == StateRunning {
			_ = exec.CommandContext(ctx, "sc.exe", "stop", "BarrikadeLens").Run()
		}
		arguments = []string{"config", "BarrikadeLens", "start=", "auto", "binPath=", binPath, "DisplayName=", "Barrikade Lens"}
	}
	output, err := exec.CommandContext(ctx, "sc.exe", arguments...).CombinedOutput()
	if err != nil {
		return Status{}, fmt.Errorf("%s Windows service: %s: %w", action, strings.TrimSpace(string(output)), err)
	}
	output, err = exec.CommandContext(ctx, "sc.exe", "start", "BarrikadeLens").CombinedOutput()
	if err != nil {
		return Status{}, fmt.Errorf("start Windows service: %s: %w", strings.TrimSpace(string(output)), err)
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		status = GetStatus(ctx)
		if status.State == StateRunning {
			return status, nil
		}
		select {
		case <-ctx.Done():
			return Status{}, ctx.Err()
		case <-deadline.C:
			return status, fmt.Errorf("Windows service did not reach the running state: %s", status.Detail)
		case <-ticker.C:
		}
	}
}

func managedExecutableName() string {
	if runtime.GOOS == "windows" {
		return serviceName + ".exe"
	}
	return serviceName
}

func launchdRunning(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "state = running" {
			return true
		}
	}
	return false
}

func launchdTarget() (string, string, error) {
	if privilegedAccount() {
		return "/Library/LaunchDaemons/com.barrikade.lens.plist", "system", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	path, domain := launchdTargetForUser(home, strconv.Itoa(os.Getuid()))
	return path, domain, nil
}
func systemdTarget() (string, bool, error) {
	if privilegedAccount() {
		return "/etc/systemd/system/" + serviceName + ".service", true, nil
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return "", false, err
	}
	return filepath.Join(root, "systemd", "user", serviceName+".service"), false, nil
}
func privilegedAccount() bool {
	account, err := user.Current()
	if err != nil {
		return false
	}
	name := strings.ToLower(account.Username)
	return account.Uid == "0" || name == "system" || strings.HasSuffix(name, `\system`)
}
func systemdQuote(value string) string { return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"` }
func xmlEscape(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return replacer.Replace(value)
}
