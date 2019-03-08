// Command conntest is a simple tool to test if a server (TCP/Unix socket) can accept a given number
// of simultaneous connections.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

var verbose bool

func usage() {
	fmt.Fprint(os.Stderr, `USAGE: conntest [OPTIONS] [ADDR]

ADDR may be any IPv4, IPv6 address:port pair, or a network::addr, such
as tcp::127.0.0.1:80 or unix::path/to/unix.sock.

If any connection fails, conntest exits with status 1.

OPTIONS:
-v       Verbose connection output.
-n CONNS Number of connections to open per ADDR. (default: 1)
-t TTL   Connection timeout. (default: 1s)
-T TTL   Execution timeout. (default: 0 [none])
`)
}

func main() {
	log.SetFlags(0)

	flag.CommandLine.Usage = usage
	flag.BoolVar(&verbose, "v", false, "verbose")
	numConns := flag.Int("n", 1, "simultaneous connections")
	timeout := flag.Duration("t", time.Second, "connection timeout")
	totalTimeout := flag.Duration("T", 0, "process timeout")
	flag.Parse()

	var (
		ctx    context.Context
		cancel context.CancelFunc
	)
	if *totalTimeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), *totalTimeout)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}
	defer cancel()

	conns := flag.Args()
	var wg sync.WaitGroup
	wg.Add(len(conns))

	for _, conn := range conns {
		conn := conn
		go func() {
			defer wg.Done()
			if err := makeConns(ctx, conn, *numConns, *timeout); err != nil {
				log.Print(err)
				cancel()
			}
		}()
	}

	wg.Wait()

	select {
	case <-ctx.Done():
		os.Exit(1)
	default:
	}
}

func parseConn(conn string) (network, addr string) {
	network, addr = "tcp", conn
	if i := strings.Index(conn, "::"); i != -1 {
		network, addr = conn[:i], conn[i+2:]
		if network == "" {
			network = "tcp"
		}
	}
	return network, addr
}

var dialer net.Dialer

func makeConns(ctx context.Context, conn string, numConns int, timeout time.Duration) error {
	network, addr := parseConn(conn)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	start := make(chan struct{})
	errs := make(chan error, 1)
	var wg sync.WaitGroup
	var down sync.WaitGroup
	defer down.Wait()

	finish := func(c net.Conn) {
		<-ctx.Done()
		c.Close()
		down.Done()
	}

	connect := func(ctx context.Context, id int) {
		defer wg.Done()
		<-start

		dialctx, done := context.WithTimeout(ctx, timeout)
		defer done()

		c, err := dialer.DialContext(dialctx, network, addr)
		if err != nil {
			err = fmt.Errorf("%s: Connection %d failed: %v", conn, id, err)
			select {
			case errs <- err:
			case <-ctx.Done():
				if verbose {
					log.Print(err)
				}
			}
			cancel()
			down.Done()
			return
		}
		if verbose {
			log.Printf("%s: Connection %d up", conn, id)
		}
		go finish(c)
	}

	for i := 0; i < numConns; i++ {
		wg.Add(1)
		down.Add(1)
		go connect(ctx, i+1)
	}

	close(start)
	wg.Wait()
	cancel()

	select {
	case err := <-errs:
		return err
	default:
		return nil
	}
}
