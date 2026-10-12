package config

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ResumeSince is the token a resumable stream's request reads: the time
// to ask for lines since, RFC 3339 in UTC. Empty on the first connect.
const ResumeSince = "${resume.since}"

// ResumeSpec is a stream's `resume:` block.
//
//	resume:
//	  timestamp: '^(\S+) '   # each line's timestamp: the first capture group
//	  granularity: 1s        # what the source's "since" honours; default 1s
type ResumeSpec struct {
	// Timestamp is a regular expression whose first capture group is
	// the line's timestamp (RFC 3339, with or without fractional
	// seconds). Lines it doesn't match are shown but not tracked.
	Timestamp string `yaml:"timestamp"`
	// Granularity is the resolution the source's "since" parameter
	// honours — a second for the Kubernetes API. Default 1s.
	Granularity string `yaml:"granularity,omitempty"`
}

// GranularityOrDefault is the granularity as a duration.
func (r *ResumeSpec) GranularityOrDefault() time.Duration {
	if d, err := time.ParseDuration(r.Granularity); err == nil && d > 0 {
		return d
	}
	return time.Second
}

// validateResume checks `resume:`, and that ${resume.since} is only read
// by a source that declares it.
func (s *Source) validateResume(path string) error {
	reads := false
	for _, t := range s.requestTemplates() {
		if strings.Contains(t, ResumeSince) {
			reads = true
		}
	}
	r := s.Resume
	if r == nil {
		if reads {
			return fmt.Errorf("%s: reads %s but declares no `resume:` — nothing tracks where the stream got to", path, ResumeSince)
		}
		return nil
	}
	streaming := s.Type == "websocket" || ((s.Type == "exec" || s.Type == "http") && s.Follow)
	if !streaming {
		return fmt.Errorf("%s.resume: only applies to a stream (websocket, or exec / http with `follow: true`)", path)
	}
	re, err := regexp.Compile(r.Timestamp)
	switch {
	case r.Timestamp == "":
		return fmt.Errorf("%s.resume.timestamp: required — a regex whose first group is each line's timestamp", path)
	case err != nil:
		return fmt.Errorf("%s.resume.timestamp: %v", path, err)
	case re.NumSubexp() < 1:
		return fmt.Errorf("%s.resume.timestamp: needs a capture group around the timestamp, e.g. '^(\\S+) '", path)
	}
	if r.Granularity != "" {
		if d, err := time.ParseDuration(r.Granularity); err != nil || d <= 0 {
			return fmt.Errorf("%s.resume.granularity: %q is not a positive duration", path, r.Granularity)
		}
	}
	return nil
}

// requestTemplates is every templated string a source sends.
func (s *Source) requestTemplates() []string {
	out := append([]string{s.URL, s.Body}, s.Command...)
	out = append(out, s.InitialMessages...)
	for _, v := range s.Headers {
		out = append(out, v)
	}
	for _, v := range s.Env {
		out = append(out, v)
	}
	return out
}
