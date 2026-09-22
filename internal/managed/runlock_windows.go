//go:build windows

package managed

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

type runLock struct {
	handle windows.Handle
}

func acquireRunLock(configPath string) (*runLock, error) {
	path, err := windows.UTF16PtrFromString(configPath + ".lock")
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_HIDDEN, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, fmt.Errorf("another managed collector is already using this configuration")
		}
		return nil, err
	}
	return &runLock{handle: handle}, nil
}

func (lock *runLock) Close() error {
	if lock == nil || lock.handle == 0 || lock.handle == windows.InvalidHandle {
		return nil
	}
	handle := lock.handle
	lock.handle = 0
	return windows.CloseHandle(handle)
}
