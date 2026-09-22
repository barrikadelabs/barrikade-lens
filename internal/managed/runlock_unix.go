//go:build !windows

package managed

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

type runLock struct {
	file *os.File
}

func acquireRunLock(configPath string) (*runLock, error) {
	file, err := os.OpenFile(configPath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, fmt.Errorf("another managed collector is already using this configuration")
		}
		return nil, err
	}
	return &runLock{file: file}, nil
}

func (lock *runLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	file := lock.file
	lock.file = nil
	unlockErr := unix.Flock(int(file.Fd()), unix.LOCK_UN)
	closeErr := file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
