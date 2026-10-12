package screen

import (
	"regexp"
	"strings"
	"time"

	"github.com/jsdrews/tuilib/pkg/resume"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// resumeMsg asks to reconnect a resumable stream that ended.
type resumeMsg struct{ source string }

// Waits between automatic reconnects: the first is quick, and they back
// off while the stream keeps ending with nothing new (a finished pod's
// logs), so a dead stream isn't hammered.
const (
	resumeFirstWait = time.Second
	resumeMaxWait   = 30 * time.Second
)

// resumeState is a `resume:` stream's bookkeeping: tuilib's Tracker over
// the timestamps the config's regex reads off each line.
type resumeState struct {
	tracker resume.Tracker
	stamp   *regexp.Regexp
	wait    time.Duration
}

func newResumeState(src *cfg.Source) *resumeState {
	if src == nil || src.Resume == nil {
		return nil
	}
	return &resumeState{
		tracker: resume.New(resume.Options{Granularity: src.Resume.GranularityOrDefault()}),
		stamp:   regexp.MustCompile(src.Resume.Timestamp), // validated at load
	}
}

// skip reports whether line repeats one already shown, and otherwise
// records its timestamp. Lines without one are always shown.
func (r *resumeState) skip(line string) bool {
	ts, ok := r.timestamp(line)
	if !ok {
		return false
	}
	if r.tracker.Drop(ts) {
		return true
	}
	r.tracker.Observe(ts)
	r.wait = 0 // something new: the next drop reconnects quickly again
	return false
}

func (r *resumeState) timestamp(line string) (time.Time, bool) {
	m := r.stamp.FindStringSubmatch(line)
	if len(m) < 2 {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339, strings.TrimSpace(m[1]))
	return ts, err == nil
}

// resumeFrom starts a reconnect and returns ${resume.since}: the time to
// ask from, RFC 3339 in UTC, or "" when no line was ever seen (start from
// the tail, as on the first connect).
func (r *resumeState) resumeFrom(now time.Time) string {
	since, _, ok := r.tracker.Resume(now)
	if !ok {
		return ""
	}
	return since.UTC().Format(time.RFC3339)
}

// nextWait is how long to wait before the next automatic reconnect.
func (r *resumeState) nextWait() time.Duration {
	if r.wait == 0 {
		r.wait = resumeFirstWait
	} else if r.wait *= 2; r.wait > resumeMaxWait {
		r.wait = resumeMaxWait
	}
	return r.wait
}
