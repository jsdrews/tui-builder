// Package gen turns an OpenAPI 3.0 / 3.1 document into a tui-builder
// config: `tui-builder gen <spec>`.
package gen

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
)

// DefaultTimeout bounds fetching a spec (and each file or URL it
// references) from a URL.
const DefaultTimeout = 30 * time.Second

// Input is where the spec comes from.
type Input struct {
	// Spec is a file path, an http(s):// URL, or "-" for Stdin.
	Spec  string
	Stdin io.Reader
	// Headers are sent with every URL fetch (--header), for specs behind
	// auth. They never reach the config.
	Headers http.Header
	Timeout time.Duration
}

// spec is a loaded document and where it came from.
type spec struct {
	doc *openapi3.T
	// url is the spec's URL when it was fetched from one; relative
	// server URLs resolve against it.
	url *url.URL
	// name is what the header comment calls it: the path or URL as given.
	name   string
	client *http.Client
	in     Input
	// ov is the --overrides file, or nil; hinted are the config paths
	// its gen: hints set, which the fragment mustn't set too.
	ov     *overrides
	hinted [][]string
	order  []string // gen.order, resolved to "METHOD /path"
	all    []op     // every operation in the spec, for the header
}

// hint is an operation's gen: hints (empty without an overrides file),
// marked as applied.
func (s *spec) hint(o op) *opHints {
	h := s.ov.hint(o)
	h.used = true
	return h
}

// hinted records a config path a hint set.
func (s *spec) set(path ...string) { s.hinted = append(s.hinted, path) }

// load reads and resolves the spec. It doesn't validate it: strict
// validation rejects real-world specs that load and map fine.
func load(ctx context.Context, in Input) (*spec, error) {
	if in.Timeout <= 0 {
		in.Timeout = DefaultTimeout
	}
	s := &spec{name: in.Spec, client: &http.Client{Timeout: in.Timeout}, in: in}
	loader := openapi3.NewLoader()
	loader.Context = ctx
	loader.IncludeOrigin = true
	loader.IsExternalRefsAllowed = true
	loader.ReadFromURIFunc = func(_ *openapi3.Loader, u *url.URL) ([]byte, error) {
		if u.Scheme == "http" || u.Scheme == "https" {
			return s.fetch(ctx, u.String())
		}
		path := u.Path
		if u.Scheme == "file" || u.Scheme == "" {
			return os.ReadFile(filepath.FromSlash(path))
		}
		return nil, fmt.Errorf("can't read %s: only files and http(s) URLs", u)
	}
	var doc *openapi3.T
	var err error
	switch {
	case in.Spec == "-":
		if in.Stdin == nil {
			return nil, fmt.Errorf("no stdin to read the spec from")
		}
		b, rerr := io.ReadAll(in.Stdin)
		if rerr != nil {
			return nil, rerr
		}
		s.name = "stdin"
		doc, err = loader.LoadFromData(b)
	case strings.HasPrefix(in.Spec, "http://") || strings.HasPrefix(in.Spec, "https://"):
		u, perr := url.Parse(in.Spec)
		if perr != nil {
			return nil, perr
		}
		s.url = u
		doc, err = loader.LoadFromURI(u)
	default:
		if _, serr := os.Stat(in.Spec); serr != nil {
			return nil, serr
		}
		doc, err = loader.LoadFromFile(in.Spec)
	}
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", s.name, err)
	}
	if doc.Paths == nil || doc.Paths.Len() == 0 {
		return nil, fmt.Errorf("%s has no paths: is it an OpenAPI 3 document?", s.name)
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.") {
		return nil, fmt.Errorf("%s is OpenAPI %q; gen reads 3.0 and 3.1 (convert Swagger 2.0 first)", s.name, doc.OpenAPI)
	}
	s.doc = doc
	return s, nil
}

// fetch GETs a URL with --header and the timeout.
func (s *spec) fetch(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	for k, vs := range s.in.Headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}
