// Shared extension-supervisor construction for `cards serve --run-extensions`
// (supported home) and standalone `cards run-extensions`. Both entry points
// must call newExtensionSupervisor — do not construct hooks.Supervisor inline.
// See docs/architecture/lifecycle-schema.md.
package main

import (
	"fmt"
	"net"

	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/hooks"
)

// extensionSupervisorOpts is the single wiring surface for the bimodal
// supervisor (hooks + services).
type extensionSupervisorOpts struct {
	getSvc       hooks.ServiceFunc
	ws           *core.Workspace
	exts         []config.Extension
	workspaceDir string
	cardsURL     string
	ready        <-chan struct{} // nil → start services immediately
}

// newExtensionSupervisor builds the supervisor used by both run-extensions
// entry points. ready gates kind:service autostart (listener-ready); pass nil
// for standalone mode where the API is already up elsewhere.
func newExtensionSupervisor(opts extensionSupervisorOpts) *hooks.Supervisor {
	if opts.getSvc == nil {
		panic("newExtensionSupervisor: getSvc is required")
	}
	sup := hooks.New(opts.getSvc, opts.ws, opts.exts, opts.workspaceDir, opts.cardsURL)
	if opts.ready != nil {
		sup.SetReady(opts.ready)
	}
	return sup
}

// cardsURLForChildren returns the loopback base URL handed to extension
// children via CARDS_URL. Non-loopback bind addresses still advertise
// 127.0.0.1 so local supervised processes dial the same machine.
func cardsURLForChildren(host string, port int) string {
	h := host
	switch h {
	case "", "0.0.0.0", "::", "[::]":
		h = "127.0.0.1"
	default:
		if ip := net.ParseIP(h); ip != nil && (ip.IsUnspecified() || !ip.IsLoopback()) {
			// Still prefer loopback for in-process children when the listen
			// address is a specific non-loopback NIC — children co-locate.
			if ip.IsUnspecified() {
				h = "127.0.0.1"
			}
		}
	}
	return fmt.Sprintf("http://%s:%d/v1", h, port)
}

// countHooks returns the number of hook-kind extensions declared.
func countHooks(exts []config.Extension) int {
	n := 0
	for _, e := range exts {
		if e.Kind == "hook" {
			n++
		}
	}
	return n
}

// countAutostartServices returns kind:service with Autostart true.
func countAutostartServices(exts []config.Extension) int {
	n := 0
	for _, e := range exts {
		if e.Kind == "service" && e.Autostart {
			n++
		}
	}
	return n
}
