package hooks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"github.com/somebox/cards/internal/config"
)

// Service identity + declaration fingerprint for reconcile-on-reload (P5c).
// Normative table: docs/architecture/RELOAD.md §"Service reconcile-on-reload".
//
// Identity key = extension id.
// Same id + different ServiceDeclFingerprint ⇒ declaration-changed → drain+restart.

// ServiceDeclFingerprint hashes the declaration fields that mean "same service,
// changed how it runs": run (command+args), env (sorted), cwd, restart_policy.
func ServiceDeclFingerprint(e config.Extension) string {
	h := sha256.New()
	for _, a := range e.Run {
		fmt.Fprintf(h, "run:%s\n", a)
	}
	keys := make([]string, 0, len(e.Env))
	for k := range e.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(h, "env:%s=%s\n", k, e.Env[k])
	}
	fmt.Fprintf(h, "cwd:%s\n", e.Cwd)
	fmt.Fprintf(h, "restart_policy:%s\n", e.RestartPolicy)
	return hex.EncodeToString(h.Sum(nil))
}

// Reconcile applies the P5c decision table against a snapshot of extensions
// from a successful reload. Call AFTER reloadableApp.mu is released — never
// from a path that holds that mutex while waiting on supervise work.
//
// Decision table (desired = kind:service && autostart):
//
//	added               → start
//	removed             → drain + stop
//	unchanged           → leave alone
//	declaration-changed → drain + restart
//
// Hook/run declarations stay frozen at construction; only service decls update.
func (s *Supervisor) Reconcile(exts []config.Extension) {
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()

	s.replaceServiceDecls(exts)

	s.mu.Lock()
	ctx := s.svcCtx
	s.mu.Unlock()
	if ctx == nil || ctx.Err() != nil {
		// Not past listener-ready / already shutting down — initial
		// startAutostartServices (or shutdown) owns the set.
		return
	}

	desired := map[string]config.Extension{}
	for _, e := range exts {
		if e.Kind == "service" && e.Autostart {
			desired[e.ID] = e
		}
	}

	s.mu.Lock()
	running := append([]*managedService(nil), s.services...)
	s.mu.Unlock()

	alive := map[string]*managedService{}
	for _, ms := range running {
		alive[ms.ext.ID] = ms
	}

	// removed + declaration-changed → drain+stop
	for id, ms := range alive {
		want, ok := desired[id]
		if !ok {
			s.log(id, "reconcile: removed → stop")
			s.stopOne(ms)
			s.removeManaged(ms)
			delete(alive, id)
			continue
		}
		if ServiceDeclFingerprint(want) != ms.fp {
			s.log(id, "reconcile: declaration-changed → restart")
			s.stopOne(ms)
			s.removeManaged(ms)
			delete(alive, id)
			// restarted below as added
			continue
		}
		// unchanged → leave alone
	}

	// added (and declaration-changed after stop) → start
	for id, want := range desired {
		if _, ok := alive[id]; ok {
			continue
		}
		s.log(id, "reconcile: added → start")
		s.startOne(ctx, want)
	}
}

// replaceServiceDecls swaps kind:service entries from the reload snapshot while
// keeping hook/run decls from construction (frozen).
func (s *Supervisor) replaceServiceDecls(exts []config.Extension) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := make([]config.Extension, 0, len(s.extensions))
	for _, e := range s.extensions {
		if e.Kind != "service" {
			kept = append(kept, e)
		}
	}
	for _, e := range exts {
		if e.Kind == "service" {
			kept = append(kept, e)
		}
	}
	s.extensions = kept
}

func (s *Supervisor) removeManaged(ms *managedService) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.services[:0]
	for _, x := range s.services {
		if x != ms {
			out = append(out, x)
		}
	}
	s.services = out
}

// stopOne drains a single managed service (SIGTERM → grace → SIGKILL) and
// waits for its supervise loop to exit. Does not touch reloadableApp.mu.
func (s *Supervisor) stopOne(ms *managedService) {
	ms.mu.Lock()
	pid := ms.pid
	cancel := ms.cancel
	done := ms.done
	ms.mu.Unlock()

	if pid > 0 {
		s.log(ms.ext.ID, "stopping (SIGTERM)")
		_ = terminateProcessGroup(pid)
	}
	if cancel != nil {
		cancel()
	}
	if done == nil {
		return
	}
	select {
	case <-done:
		return
	case <-time.After(s.drainTimeout):
		ms.mu.Lock()
		pid = ms.pid
		ms.mu.Unlock()
		if pid > 0 {
			s.log(ms.ext.ID, "force kill (SIGKILL)")
			_ = killProcessGroup(pid)
		}
		<-done
	}
}

// startOne launches one supervise loop under parent (the Run-scoped svcCtx).
func (s *Supervisor) startOne(parent context.Context, ext config.Extension) {
	ctx, cancel := context.WithCancel(parent)
	ms := &managedService{
		ext:    ext,
		fp:     ServiceDeclFingerprint(ext),
		cancel: cancel,
		done:   make(chan struct{}),
	}
	s.mu.Lock()
	s.services = append(s.services, ms)
	s.mu.Unlock()
	s.svcWG.Add(1)
	go func() {
		defer s.svcWG.Done()
		defer close(ms.done)
		s.superviseService(ctx, ms)
	}()
}

// ServicePID returns the live child pid for id, or 0 if not running.
func (s *Supervisor) ServicePID(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ms := range s.services {
		if ms.ext.ID == id {
			ms.mu.Lock()
			pid := ms.pid
			ms.mu.Unlock()
			return pid
		}
	}
	return 0
}

// ServiceStarts returns how many times the child for id has successfully
// started (increments across crash-restarts and reconcile restarts).
func (s *Supervisor) ServiceStarts(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ms := range s.services {
		if ms.ext.ID == id {
			return int(ms.starts.Load())
		}
	}
	return 0
}
