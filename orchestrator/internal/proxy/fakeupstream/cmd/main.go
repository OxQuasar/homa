// Standalone fake-upstream binary. See ../upstream.go for protocol details.
//
// Used by ~/homa/scripts/e2e_phase5.sh to stand up a fake sandbox WS on a
// fixed port the orchestrator's StubProvisioner will allocate to the first
// signup.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os/signal"
	"syscall"

	"github.com/skipper/homa/orchestrator/internal/proxy/fakeupstream"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "TCP listen address (\":0\" → OS-assigned)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	addrCh := make(chan net.Addr, 1)
	errCh := make(chan error, 1)
	go func() { errCh <- fakeupstream.ListenAndServe(ctx, *addr, addrCh) }()

	select {
	case got := <-addrCh:
		fmt.Println("listening", got.String())
	case err := <-errCh:
		log.Fatalf("listen: %v", err)
	}
	if err := <-errCh; err != nil {
		log.Fatalf("serve: %v", err)
	}
}
