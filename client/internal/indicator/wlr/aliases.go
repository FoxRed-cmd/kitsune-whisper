//go:build linux

//go:generate go run github.com/neurlang/wayland/cmd/wayland-scanner -i wlr-layer-shell-unstable-v1.xml

package wlr

import (
	"github.com/neurlang/wayland/wl"
	"github.com/neurlang/wayland/xdg"
)

// The generated binding is written against the core types in the same package;
// these aliases keep it unchanged while living in its own package.
type (
	BaseProxy = wl.BaseProxy
	Context   = wl.Context
	Event     = wl.Event
	Output    = wl.Output
	Proxy     = wl.Proxy
	Surface   = wl.Surface
	XdgPopup  = xdg.Popup
)
