package tui

import (
	"errors"
	"log"
	"sync"
	"time"

	"github.com/radix29/gossms/internal/config"
)

// app_saves.go writes config.json and tracked_queries.json off the UI
// goroutine (T58). Both saves take a cross-instance file lock that waits up to
// two seconds for another gossms, read a key file, and fsync — all of it
// stalled every keystroke and redraw while it ran on the UI goroutine.

// errConfigSavePanicked is what a config save's callers hear when Run
// panicked; the panic itself is reported by safegoRepair.
var errConfigSavePanicked = errors.New("config save failed: internal error (see the log)")

// savesInFlight counts the save goroutines of every App. FlushSaves waits on
// it for the tracked-query saves; tests wait on it before their scratch config
// directory is removed under a save still writing there.
var savesInFlight sync.WaitGroup

// appSaves is the App's background-save bookkeeping. UI goroutine only, but
// for ran/err, read only after ran is closed.
type appSaves struct {
	// job is the config save in flight, nil when none is. Config.EndSave
	// drops the ops a job carried by position, so jobs never overlap: a save
	// asked for meanwhile waits in waiting, and the in-flight one's finish
	// starts it with a fresh snapshot covering every change since.
	job *config.SaveJob
	// ran is closed once job's Run has returned, err holding its result,
	// for FlushSaves after the event loop is gone.
	ran chan struct{}
	err error
	// waiting is the callbacks of saves asked for while job was out.
	waiting []func(error)
}

// saveConfig writes a.cfg in the background and then calls done, which may be
// nil, on the UI goroutine with the result. The change it saves must already
// be in a.cfg: the file gets a snapshot taken now, or — while another save is
// out — when that one finishes.
func (a *App) saveConfig(done func(error)) {
	if a.cfg == nil {
		return
	}
	a.saves.waiting = append(a.saves.waiting, done)
	if a.saves.job == nil {
		a.startConfigSave()
	}
}

// startConfigSave runs one config save for every caller waiting.
func (a *App) startConfigSave() {
	s := &a.saves
	job, dones, ran := a.cfg.BeginSave(), s.waiting, make(chan struct{})
	s.job, s.ran, s.err, s.waiting = job, ran, nil, nil
	finish := func(err error) {
		if s.job != job {
			return
		}
		a.cfg.EndSave(job)
		s.job = nil
		for _, done := range dones {
			if done != nil {
				done(err)
			}
		}
		if len(s.waiting) > 0 {
			a.startConfigSave()
		}
	}
	// The repair releases job, or no config save would run again this
	// session.
	savesInFlight.Add(1)
	a.safegoRepair("save config", func() { finish(errConfigSavePanicked) }, func() {
		defer savesInFlight.Done()
		defer close(ran)
		err := job.Run()
		s.err = err
		a.postAndWake(func() { finish(err) })
	})
}

// saveTracked writes the tracked-query set in the background and then calls
// done on the UI goroutine with the result. TrackedQueries serializes its own
// saves, and one in progress does not block its readers.
func (a *App) saveTracked(done func(error)) {
	savesInFlight.Add(1)
	a.safego("save tracked queries", func() {
		defer savesInFlight.Done()
		err := config.Tracked().Save()
		a.postAndWake(func() { done(err) })
	})
}

// FlushSaves finishes the saves still owed once Run has returned, so quitting
// straight after OK in Options, say, does not lose the change: it waits up to
// wait for the config save in flight, adopts it, writes any save asked for
// after it synchronously, and waits for tracked-query saves. Their callbacks
// do not run — there is no UI left to report to — so failures go to the log.
//
// It runs on main's goroutine with the event loop gone, which makes that
// goroutine the only one touching a.cfg. Not after a panic on the UI
// goroutine: a.cfg may be half-updated then.
func (a *App) FlushSaves(wait time.Duration) {
	if a == nil || a.cfg == nil {
		return
	}
	s := &a.saves
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	if s.job != nil {
		select {
		case <-s.ran:
			if s.err != nil {
				log.Printf("save config: %v", s.err)
			}
			a.cfg.EndSave(s.job)
			s.job = nil
		case <-deadline.C:
			log.Printf("save config: still running after %v at exit; later changes not saved", wait)
			return
		}
	}
	if len(s.waiting) > 0 {
		s.waiting = nil
		if err := a.cfg.Save(); err != nil {
			log.Printf("save config: %v", err)
		}
	}
	tracked := make(chan struct{})
	go func() {
		savesInFlight.Wait()
		close(tracked)
	}()
	select {
	case <-tracked:
	case <-deadline.C:
		log.Printf("save tracked queries: still running after %v at exit", wait)
	}
}
