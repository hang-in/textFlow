//go:build !darwin && !windows

package statusitem

import "context"

func PrepareAccessoryApp()        {}
func Install(ctx context.Context) {}
