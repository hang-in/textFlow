//go:build !darwin && !windows

package main

import "github.com/wailsapp/wails/v2/pkg/options"

func applyPlatformOptions(opts *options.App) {}
