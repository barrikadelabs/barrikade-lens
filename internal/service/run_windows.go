//go:build windows

package service

import (
	"context"
	"fmt"

	"golang.org/x/sys/windows/svc"
)

func Run(ctx context.Context, runner func(context.Context) error) error {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return fmt.Errorf("detect Windows service session: %w", err)
	}
	if !isService {
		return runner(ctx)
	}
	return svc.Run("BarrikadeLens", &windowsService{ctx: ctx, runner: runner})
}

type windowsService struct {
	ctx    context.Context
	runner func(context.Context) error
}

func (service *windowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, statuses chan<- svc.Status) (bool, uint32) {
	statuses <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(service.ctx)
	defer cancel()
	errors := make(chan error, 1)
	go func() { errors <- service.runner(ctx) }()
	statuses <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-errors:
			if err != nil && ctx.Err() == nil {
				return false, 1
			}
			return false, 0
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				statuses <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				statuses <- svc.Status{State: svc.StopPending}
				cancel()
				if err := <-errors; err != nil && err != context.Canceled {
					return false, 1
				}
				return false, 0
			}
		}
	}
}
