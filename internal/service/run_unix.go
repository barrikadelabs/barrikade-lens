//go:build !windows

package service

import "context"

func Run(ctx context.Context, runner func(context.Context) error) error {
	return runner(ctx)
}
