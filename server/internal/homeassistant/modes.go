package homeassistant

import (
	"context"
	"fmt"
	"time"

	"github.com/garybowers/cineva/server/internal/store"
)

// Limits on a mode, so one cannot tie a runner up for long.
const (
	MaxModeSteps    = 20
	MaxWaitSecs     = 30
	MaxModeWaitSecs = 60
)

// stepAction is a mode's action step as a control request.
func stepAction(st store.HAModeStep) Action {
	return Action{Service: st.Service, BrightnessPct: st.BrightnessPct, Volume: st.Volume, Muted: st.Muted, Source: st.Source}
}

// ValidateSteps checks a mode's steps against the rules: allowed services
// and fields per domain, wait limits, and at most one play step, which
// needs a device. Whether the device and title exist is the caller's to
// check. known, when it has the entity, supplies its inputs for a
// select_source; a mode can be built while Home Assistant is away, so an
// entity it does not know is let through.
func ValidateSteps(steps []store.HAModeStep, known func(string) (Entity, bool)) error {
	if len(steps) == 0 {
		return invalid("a mode needs at least one step")
	}
	if len(steps) > MaxModeSteps {
		return invalid("a mode has at most %d steps", MaxModeSteps)
	}
	wait, plays := 0, 0
	for i, st := range steps {
		n := i + 1
		switch st.Type {
		case "action":
			if st.EntityID == "" {
				return invalid("step %d: choose an entity", n)
			}
			if domainOf(st.EntityID) == "camera" {
				return invalid("step %d: a camera cannot be worked by a mode", n)
			}
			var sources []string
			if e, ok := known(st.EntityID); ok {
				sources = e.Sources
			}
			if _, err := serviceData(st.EntityID, stepAction(st), sources); err != nil {
				return fmt.Errorf("step %d: %w", n, err)
			}
			if st.Seconds != 0 || st.DeviceID != "" || st.ItemID != "" {
				return invalid("step %d: an action takes no seconds, device or title", n)
			}
		case "wait":
			if st.Seconds < 1 || st.Seconds > MaxWaitSecs {
				return invalid("step %d: wait between 1 and %d seconds", n, MaxWaitSecs)
			}
			wait += st.Seconds
		case "play":
			if st.DeviceID == "" {
				return invalid("step %d: choose the device to play on", n)
			}
			plays++
		default:
			return invalid("step %d: unknown kind of step %q", n, st.Type)
		}
	}
	if wait > MaxModeWaitSecs {
		return invalid("a mode waits at most %d seconds in all", MaxModeWaitSecs)
	}
	if plays > 1 {
		return invalid("a mode plays one thing; it has %d play steps", plays)
	}
	return nil
}

// PlayStep is a mode's play step, if it has one.
func PlayStep(m store.HAMode) (store.HAModeStep, bool) {
	for _, st := range m.Steps {
		if st.Type == "play" {
			return st, true
		}
	}
	return store.HAModeStep{}, false
}

// NeedsTitle reports whether a mode plays the title it is started from.
func NeedsTitle(m store.HAMode) bool {
	st, ok := PlayStep(m)
	return ok && st.ItemID == ""
}

// Preflight checks a mode can run now: Home Assistant is connected when it
// has actions, and every entity they name exists there.
func (s *Service) Preflight(m store.HAMode) error {
	for i, st := range m.Steps {
		if st.Type != "action" {
			continue
		}
		if _, _, err := s.live(); err != nil {
			return err
		}
		if _, ok := s.Entity(st.EntityID); !ok {
			return invalid("step %d: Home Assistant has no %s", i+1, st.EntityID)
		}
	}
	return nil
}

// Progress is a mode run's state, reported as each step starts and when
// it ends.
type Progress struct {
	RunID    string `json:"runId"`
	ModeID   string `json:"modeId"`
	ModeName string `json:"modeName"`
	Status   string `json:"status"` // "running" | "done" | "failed"
	Step     int    `json:"step"`   // 1-based; the one running or the one that failed
	Steps    int    `json:"steps"`
	Error    string `json:"error,omitempty"`
	// PlayedOn is the device the title was sent to, by id and name, once
	// the play step has run; empty when that device was already playing it.
	PlayedOn     string `json:"playedOn,omitempty"`
	PlayedOnName string `json:"playedOnName,omitempty"`
}

// Player plays a mode's title on its play step's device, and says which
// device it was sent to; nothing when that device was already playing it.
type Player func(ctx context.Context, st store.HAModeStep, itemID string) (deviceID, deviceName string, err error)

// Run runs a mode's steps in order, stopping at the first that fails;
// steps already done are not undone. itemID is the title it was started
// from. Call it in a goroutine: it waits as the mode says.
func (s *Service) Run(ctx context.Context, runID string, m store.HAMode, itemID string, play Player, report func(Progress)) {
	p := Progress{RunID: runID, ModeID: m.ID, ModeName: m.Name, Status: "running", Steps: len(m.Steps)}
	for i, st := range m.Steps {
		p.Step = i + 1
		report(p)
		var err error
		switch st.Type {
		case "action":
			err = s.Do(ctx, st.EntityID, stepAction(st))
		case "wait":
			select {
			case <-ctx.Done():
				err = ctx.Err()
			case <-time.After(time.Duration(st.Seconds) * time.Second):
			}
		case "play":
			id := st.ItemID
			if id == "" {
				id = itemID
			}
			p.PlayedOn, p.PlayedOnName, err = play(ctx, st, id)
		}
		if err != nil {
			p.Status, p.Error = "failed", err.Error()
			logger.Warn("mode step failed", "mode", m.Name, "step", p.Step, "err", err)
			report(p)
			return
		}
	}
	p.Status = "done"
	logger.Info("mode ran", "mode", m.Name, "steps", len(m.Steps))
	report(p)
}
