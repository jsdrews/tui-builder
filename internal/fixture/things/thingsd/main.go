// Command thingsd serves the things fixture, for running a generated
// config against it by hand:
//
//	go run ./internal/fixture/things/thingsd -addr localhost:8765
//
// The spec is at /openapi.yaml and /openapi.json; every other route
// wants `Authorization: Bearer fixture-token`.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/jsdrews/tui-builder/internal/fixture/things"
)

func main() {
	addr := flag.String("addr", "localhost:8765", "address to listen on")
	flag.Parse()
	log.Printf("things fixture on http://%s (token %s)", *addr, things.Token)
	log.Fatal(http.ListenAndServe(*addr, things.New()))
}
