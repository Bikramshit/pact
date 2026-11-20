// Command mock runs one demo target. ROLE selects deploy, secrets, iam or sink.
package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/foxcolab/pact/internal/mocktargets"
)

func main() {
	role := os.Getenv("ROLE")
	switch role {
	case "deploy", "secrets", "iam", "sink", "fakegemini":
	default:
		log.Fatalf("ROLE must be deploy, secrets, iam, sink or fakegemini (got %q)", role)
	}
	addr := os.Getenv("LISTEN")
	if addr == "" {
		addr = ":8000"
	}
	srv := &http.Server{Addr: addr, Handler: mocktargets.NewHandler(role, os.Getenv("TARGET_TOKEN")), ReadHeaderTimeout: 5 * time.Second}
	log.Printf("mock target %q listening on %s", role, addr)
	log.Fatal(srv.ListenAndServe())
}
