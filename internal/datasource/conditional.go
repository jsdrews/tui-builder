package datasource

// Conditional GET: a source that asks the same question again sends the
// ETag / Last-Modified it got last time, and a 304 means its last answer
// still stands. GitHub doesn't count 304s against the rate limit, which
// makes a short refresh: affordable.

import (
	"net/http"
	"sync"
)

// validators remembers, per exact request (URL and body), what the last
// 2xx answer said about itself and the value it produced.
type validators struct {
	mu sync.Mutex
	m  map[string]validated
}

type validated struct {
	etag, lastModified string
	value              any
}

// headers are the conditional headers for a request, if its last answer
// gave any.
func (v *validators) headers(key string) http.Header {
	v.mu.Lock()
	defer v.mu.Unlock()
	e, ok := v.m[key]
	if !ok {
		return nil
	}
	h := http.Header{}
	if e.etag != "" {
		h.Set("If-None-Match", e.etag)
	}
	if e.lastModified != "" {
		h.Set("If-Modified-Since", e.lastModified)
	}
	return h
}

// remember stores a 2xx answer's validators and the value it produced.
// An answer with neither validator is forgotten.
func (v *validators) remember(key string, r *response, value any) {
	e := validated{etag: r.header.Get("ETag"), lastModified: r.header.Get("Last-Modified"), value: value}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.m == nil {
		v.m = map[string]validated{}
	}
	if e.etag == "" && e.lastModified == "" {
		delete(v.m, key)
		return
	}
	v.m[key] = e
}

// last is the value the remembered answer produced, for a 304.
func (v *validators) last(key string) (any, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	e, ok := v.m[key]
	return e.value, ok
}
