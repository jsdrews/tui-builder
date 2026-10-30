// Package things is an offline stand-in for a FastAPI-style REST API:
// an OpenAPI 3.1 document (Spec) and an in-memory server that answers it.
// The OpenAPI generator is designed and tested against the pair.
//
// The server does what the spec says, and two things it doesn't, the
// way real servers drift from their specs:
//
//   - Undocumented filters: `name__icontains` and
//     `description__icontains` on /things, `name__icontains` on /owners.
//   - Refused sorts: ThingOrder lists `status` and `-status`; asking for
//     either answers 400 {"detail": "..."}.
//
// Every route but the spec itself needs `Authorization: Bearer <Token>`.
package things

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Spec is the OpenAPI document, as YAML.
//
//go:embed things.yaml
var Spec []byte

// Token is the bearer token the server accepts.
const Token = "fixture-token"

// Seed sizes: enough things for several pages at the default per_page.
const (
	seedOwners = 5
	seedThings = 120
)

// Statuses are ThingStatus's values, in the spec's order.
var Statuses = []string{"active", "pending", "failed", "archived"}

// RefusedOrders are the ThingOrder values the server answers with a 400.
var RefusedOrders = []string{"status", "-status"}

type Labels struct {
	Team *string `json:"team"`
	Env  *string `json:"env"`
}

type Thing struct {
	ID          int       `json:"id"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Size        int       `json:"size"`
	OwnerID     int       `json:"owner_id"`
	Tags        []string  `json:"tags"`
	Labels      Labels    `json:"labels"`
	Description *string   `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

type Owner struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// Server is the stub. Each New starts from the same seed, so tests can
// rely on ids and counts; writes change only that Server.
type Server struct {
	mu     sync.Mutex
	things []Thing
	owners []Owner
	nextID int
	mux    *http.ServeMux
}

// New returns a seeded Server.
func New() *Server {
	s := &Server{mux: http.NewServeMux()}
	s.seed()
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) seed() {
	names := []string{"Ada", "Brook", "Cyd", "Dana", "Eli"}
	for i := range seedOwners {
		s.owners = append(s.owners, Owner{
			ID: i + 1, Name: names[i], Email: strings.ToLower(names[i]) + "@example.com",
		})
	}
	teams := []string{"core", "edge", "data"}
	envs := []string{"prod", "staging"}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range seedThings {
		id := i + 1
		t := Thing{
			ID:        id,
			Name:      fmt.Sprintf("thing-%03d", id),
			Status:    Statuses[i%len(Statuses)],
			Size:      (i*7919)%100000 + 1,
			OwnerID:   i%seedOwners + 1,
			Tags:      []string{teams[i%len(teams)]},
			CreatedAt: base.Add(time.Duration(i) * time.Hour),
		}
		team, env := teams[i%len(teams)], envs[i%len(envs)]
		t.Labels = Labels{Team: &team}
		if i%3 != 0 {
			t.Labels.Env = &env
		}
		if i%4 != 3 {
			d := fmt.Sprintf("Thing number %d, kept by %s for the %s team. %s",
				id, names[t.OwnerID-1], team, strings.Repeat("Notes. ", i%5))
			t.Description = &d
		}
		s.things = append(s.things, t)
	}
	s.nextID = seedThings + 1
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		w.Write(Spec)
	})
	s.mux.HandleFunc("GET /openapi.json", func(w http.ResponseWriter, _ *http.Request) {
		var doc any
		if err := yaml.Unmarshal(Spec, &doc); err != nil {
			writeJSON(w, http.StatusInternalServerError, detail(err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, doc)
	})
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+Token {
				w.Header().Set("WWW-Authenticate", "Bearer")
				writeJSON(w, http.StatusUnauthorized, detail("Not authenticated"))
				return
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			h(w, r)
		}
	}
	s.mux.HandleFunc("GET /things", auth(s.listThings))
	s.mux.HandleFunc("POST /things", auth(s.createThing))
	s.mux.HandleFunc("GET /things/{thing_id}", auth(s.getThing))
	s.mux.HandleFunc("PATCH /things/{thing_id}", auth(s.updateThing))
	s.mux.HandleFunc("DELETE /things/{thing_id}", auth(s.deleteThing))
	s.mux.HandleFunc("GET /owners", auth(s.listOwners))
	s.mux.HandleFunc("GET /owners/{owner_id}", auth(s.getOwner))
	s.mux.HandleFunc("GET /owners/{owner_id}/things", auth(s.listOwnerThings))
}

func (s *Server) listThings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pg, ok := pageOf(w, q)
	if !ok {
		return
	}
	order := q.Get("order_by")
	if order != "" && !slices.Contains(thingOrders, order) {
		writeInvalid(w, "order_by", "Input should be "+quoteList(thingOrders), "enum")
		return
	}
	if slices.Contains(RefusedOrders, order) {
		writeJSON(w, http.StatusBadRequest, detail("ordering by status is not supported"))
		return
	}
	status := q.Get("status")
	if status != "" && !slices.Contains(Statuses, status) {
		writeInvalid(w, "status", "Input should be "+quoteList(Statuses), "enum")
		return
	}
	owner, ok := optionalInt(w, q, "owner_id")
	if !ok {
		return
	}
	name := strings.ToLower(q.Get("name__icontains"))
	desc := strings.ToLower(q.Get("description__icontains"))

	var out []Thing
	for _, t := range s.things {
		switch {
		case status != "" && t.Status != status,
			owner != 0 && t.OwnerID != owner,
			name != "" && !strings.Contains(strings.ToLower(t.Name), name),
			desc != "" && (t.Description == nil || !strings.Contains(strings.ToLower(*t.Description), desc)):
			continue
		}
		out = append(out, t)
	}
	sortThings(out, order)
	writePage(w, out, pg)
}

func (s *Server) listOwnerThings(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "owner_id")
	if !ok {
		return
	}
	if s.owner(id) == nil {
		writeJSON(w, http.StatusNotFound, detail("Owner not found"))
		return
	}
	pg, ok := pageOf(w, r.URL.Query())
	if !ok {
		return
	}
	var out []Thing
	for _, t := range s.things {
		if t.OwnerID == id {
			out = append(out, t)
		}
	}
	writePage(w, out, pg)
}

func (s *Server) getThing(w http.ResponseWriter, r *http.Request) {
	if i, ok := s.thingIndex(w, r); ok {
		writeJSON(w, http.StatusOK, s.things[i])
	}
}

func (s *Server) createThing(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        *string  `json:"name"`
		OwnerID     *int     `json:"owner_id"`
		Status      *string  `json:"status"`
		Size        *int     `json:"size"`
		Tags        []string `json:"tags"`
		Description *string  `json:"description"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	switch {
	case in.Name == nil:
		writeInvalidBody(w, "name", "Field required", "missing")
		return
	case in.OwnerID == nil:
		writeInvalidBody(w, "owner_id", "Field required", "missing")
		return
	case s.owner(*in.OwnerID) == nil:
		writeInvalidBody(w, "owner_id", "Owner not found", "value_error")
		return
	}
	t := Thing{
		ID: s.nextID, Name: *in.Name, Status: "pending", OwnerID: *in.OwnerID,
		Tags: in.Tags, Description: in.Description,
		CreatedAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(s.nextID) * time.Minute),
	}
	if in.Status != nil {
		if !slices.Contains(Statuses, *in.Status) {
			writeInvalidBody(w, "status", "Input should be "+quoteList(Statuses), "enum")
			return
		}
		t.Status = *in.Status
	}
	if in.Size != nil {
		t.Size = *in.Size
	}
	if t.Tags == nil {
		t.Tags = []string{}
	}
	s.nextID++
	s.things = append(s.things, t)
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) updateThing(w http.ResponseWriter, r *http.Request) {
	i, ok := s.thingIndex(w, r)
	if !ok {
		return
	}
	// Raw fields, to tell an absent key from a null: a null description
	// clears it, any other null leaves the field alone.
	var in map[string]json.RawMessage
	if !decodeBody(w, r, &in) {
		return
	}
	t := s.things[i]
	for key, raw := range in {
		null := string(raw) == "null"
		var err error
		switch key {
		case "name":
			if !null {
				err = json.Unmarshal(raw, &t.Name)
			}
		case "status":
			if !null {
				var v string
				if err = json.Unmarshal(raw, &v); err == nil && !slices.Contains(Statuses, v) {
					writeInvalidBody(w, "status", "Input should be "+quoteList(Statuses), "enum")
					return
				}
				t.Status = v
			}
		case "size":
			if !null {
				err = json.Unmarshal(raw, &t.Size)
			}
		case "description":
			if null {
				t.Description = nil
			} else {
				var v string
				err = json.Unmarshal(raw, &v)
				t.Description = &v
			}
		}
		if err != nil {
			writeInvalidBody(w, key, "Input has the wrong type", "type_error")
			return
		}
	}
	s.things[i] = t
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) deleteThing(w http.ResponseWriter, r *http.Request) {
	if i, ok := s.thingIndex(w, r); ok {
		s.things = slices.Delete(s.things, i, i+1)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) listOwners(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pg, ok := pageOf(w, q)
	if !ok {
		return
	}
	order := q.Get("order_by")
	if order != "" && order != "name" && order != "-name" {
		writeInvalid(w, "order_by", "Input should be 'name' or '-name'", "enum")
		return
	}
	name := strings.ToLower(q.Get("name__icontains"))
	var out []Owner
	for _, o := range s.owners {
		if name == "" || strings.Contains(strings.ToLower(o.Name), name) {
			out = append(out, o)
		}
	}
	if order != "" {
		slices.SortStableFunc(out, func(a, b Owner) int { return strings.Compare(a.Name, b.Name) })
		if order == "-name" {
			slices.Reverse(out)
		}
	}
	writePage(w, out, pg)
}

func (s *Server) getOwner(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "owner_id")
	if !ok {
		return
	}
	if o := s.owner(id); o != nil {
		writeJSON(w, http.StatusOK, o)
		return
	}
	writeJSON(w, http.StatusNotFound, detail("Owner not found"))
}

func (s *Server) owner(id int) *Owner {
	for i := range s.owners {
		if s.owners[i].ID == id {
			return &s.owners[i]
		}
	}
	return nil
}

func (s *Server) thingIndex(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, ok := pathInt(w, r, "thing_id")
	if !ok {
		return 0, false
	}
	for i, t := range s.things {
		if t.ID == id {
			return i, true
		}
	}
	writeJSON(w, http.StatusNotFound, detail("Thing not found"))
	return 0, false
}

// thingOrders is ThingOrder's enum, refused values included.
var thingOrders = []string{"name", "-name", "created_at", "-created_at", "size", "-size", "status", "-status"}

func sortThings(ts []Thing, order string) {
	key := strings.TrimPrefix(order, "-")
	cmp := map[string]func(a, b Thing) int{
		"name":       func(a, b Thing) int { return strings.Compare(a.Name, b.Name) },
		"created_at": func(a, b Thing) int { return a.CreatedAt.Compare(b.CreatedAt) },
		"size":       func(a, b Thing) int { return a.Size - b.Size },
	}[key]
	if cmp == nil {
		return
	}
	slices.SortStableFunc(ts, cmp)
	if strings.HasPrefix(order, "-") {
		slices.Reverse(ts)
	}
}

type page struct{ n, per int }

// pageOf reads page and per_page with the spec's defaults and bounds,
// answering 422 the way FastAPI does when one is out of range.
func pageOf(w http.ResponseWriter, q map[string][]string) (page, bool) {
	pg := page{n: 1, per: 50}
	for _, f := range []struct {
		name     string
		dst      *int
		min, max int
	}{
		{"page", &pg.n, 1, 0},
		{"per_page", &pg.per, 1, 100},
	} {
		v := first(q, f.name)
		if v == "" {
			continue
		}
		n, err := strconv.Atoi(v)
		switch {
		case err != nil:
			writeInvalid(w, f.name, "Input should be a valid integer", "int_parsing")
			return pg, false
		case n < f.min:
			writeInvalid(w, f.name, fmt.Sprintf("Input should be greater than or equal to %d", f.min), "greater_than_equal")
			return pg, false
		case f.max > 0 && n > f.max:
			writeInvalid(w, f.name, fmt.Sprintf("Input should be less than or equal to %d", f.max), "less_than_equal")
			return pg, false
		}
		*f.dst = n
	}
	return pg, true
}

func writePage[T any](w http.ResponseWriter, all []T, pg page) {
	start := min((pg.n-1)*pg.per, len(all))
	end := min(start+pg.per, len(all))
	entries := all[start:end]
	if entries == nil {
		entries = []T{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total": len(all), "page": pg.n, "per_page": pg.per, "entries": entries,
	})
}

func optionalInt(w http.ResponseWriter, q map[string][]string, name string) (int, bool) {
	v := first(q, name)
	if v == "" {
		return 0, true
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		writeInvalid(w, name, "Input should be a valid integer", "int_parsing")
		return 0, false
	}
	return n, true
}

func pathInt(w http.ResponseWriter, r *http.Request, name string) (int, bool) {
	n, err := strconv.Atoi(r.PathValue(name))
	if err != nil {
		writeValidation(w, []any{"path", name}, "Input should be a valid integer", "int_parsing")
		return 0, false
	}
	return n, true
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeValidation(w, []any{"body"}, "JSON decode error", "json_invalid")
		return false
	}
	return true
}

func first(q map[string][]string, name string) string {
	if v := q[name]; len(v) > 0 {
		return v[0]
	}
	return ""
}

func quoteList(vs []string) string {
	q := make([]string, len(vs))
	for i, v := range vs {
		q[i] = "'" + v + "'"
	}
	return strings.Join(q[:len(q)-1], ", ") + " or " + q[len(q)-1]
}

func detail(msg string) map[string]string { return map[string]string{"detail": msg} }

func writeInvalid(w http.ResponseWriter, param, msg, typ string) {
	writeValidation(w, []any{"query", param}, msg, typ)
}

func writeInvalidBody(w http.ResponseWriter, field, msg, typ string) {
	writeValidation(w, []any{"body", field}, msg, typ)
}

// writeValidation answers 422 in FastAPI's HTTPValidationError shape.
func writeValidation(w http.ResponseWriter, loc []any, msg, typ string) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"detail": []map[string]any{{"loc": loc, "msg": msg, "type": typ}},
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
