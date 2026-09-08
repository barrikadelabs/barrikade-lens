//go:build windows

package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows"
)

// EnsureInstallPrivileges relaunches the exact CLI invocation once through the
// Windows UAC prompt. Enrollment therefore remains one copy/paste operation and
// the one-time credential is not consumed before service installation can work.
func EnsureInstallPrivileges(ctx context.Context, args []string) (bool, error) {
	if windows.GetCurrentProcessToken().IsElevated() {
		return false, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("prepare administrator relaunch: %w", err)
	}
	commandLine := strings.Join(quoteWindowsArgs(args), " ")
	script := "$process = Start-Process -FilePath " + quotePowerShellLiteral(executable) +
		" -ArgumentList " + quotePowerShellLiteral(commandLine) +
		" -Verb RunAs -Wait -PassThru; exit $process.ExitCode"
	encoded := encodePowerShell(script)
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", encoded)
	if err := command.Run(); err != nil {
		return false, fmt.Errorf("the elevated Windows collector setup did not complete (approve the administrator prompt and review the elevated window): %w", err)
	}
	return true, nil
}
