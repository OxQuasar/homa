// portdump is a tiny test-only helper that prints a user's nous_port from
// the orchestrator's SQLite DB. Used by scripts/restart_safety_test.sh.
// Not part of the production surface — lives under cmd/ purely so it can
// import the same internal/store types the orchestrator uses.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/skipper/homa/orchestrator/internal/store"
)

func main() {
	dbPath := flag.String("db", "", "path to homa.db")
	email := flag.String("email", "", "user email")
	flag.Parse()
	if *dbPath == "" || *email == "" {
		fmt.Fprintln(os.Stderr, "portdump: --db and --email are required")
		os.Exit(2)
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer st.Close()
	u, err := st.GetUserByEmail(context.Background(), *email)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(u.NousPort)
}
