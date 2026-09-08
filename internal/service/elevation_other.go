//go:build !windows

package service

import "context"

// EnsureInstallPrivileges is a no-op on platforms whose managed collector is
// installed in the current user's service domain.
func EnsureInstallPrivileges(context.Context, []string) (bool, error) {
	return false, nil
}
