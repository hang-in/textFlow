//go:build darwin

package main

import (
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

func applyPlatformOptions(opts *options.App) {
	opts.Mac = &mac.Options{
		About: &mac.AboutInfo{
			Title:   "DKST Text Flow",
			Message: "Text expansion utility by DINKI'ssTyle.",
		},
		DisableZoom: true,
	}
}
