// Command txqueue is a small CLI for the txqueue durable job-queue API, built
// on the github.com/cognis-digital/txqueue-sdk/txqueue client.
//
// Usage:
//
//	txqueue [global flags] <command> [args]
//
// Commands:
//
//	enqueue <message>   enqueue a message (optionally with -key for idempotency)
//	dequeue             lease and print the next message (does not ack)
//	ack <id>            acknowledge a message
//	nack <id>           negatively acknowledge a message
//	stats               print queue statistics
//
// Global flags:
//
//	-addr     queue base URL (default http://localhost:8080, or $TXQUEUE_ADDR)
//	-timeout  per-attempt timeout (default 30s)
//	-retries  transient-error retries (default 3)
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cognis-digital/txqueue-sdk/txqueue"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("txqueue", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	defaultAddr := os.Getenv("TXQUEUE_ADDR")
	if defaultAddr == "" {
		defaultAddr = "http://localhost:8080"
	}
	addr := fs.String("addr", defaultAddr, "queue base URL (env TXQUEUE_ADDR)")
	timeout := fs.Duration("timeout", 30*time.Second, "per-attempt HTTP timeout")
	retries := fs.Int("retries", 3, "number of retries on transient errors")
	visibility := fs.Int("visibility", 30, "dequeue lease seconds")
	idemKey := fs.String("key", "", "idempotency key for enqueue")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, usage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return 2
	}
	cmd, cmdArgs := rest[0], rest[1:]

	client, err := txqueue.New(*addr,
		txqueue.WithTimeout(*timeout),
		txqueue.WithMaxRetries(*retries),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}

	ctx := context.Background()
	switch cmd {
	case "enqueue":
		if len(cmdArgs) != 1 {
			fmt.Fprintln(os.Stderr, "usage: txqueue enqueue <message>")
			return 2
		}
		res, err := client.Enqueue(ctx, cmdArgs[0], *idemKey)
		if err != nil {
			return fail(err)
		}
		return emit(res)

	case "dequeue":
		msg, err := client.Dequeue(ctx, *visibility)
		if err != nil {
			if errors.Is(err, txqueue.ErrEmptyQueue) {
				fmt.Println("(queue empty)")
				return 0
			}
			return fail(err)
		}
		return emit(msg)

	case "ack":
		if len(cmdArgs) != 1 {
			fmt.Fprintln(os.Stderr, "usage: txqueue ack <id>")
			return 2
		}
		if err := client.Ack(ctx, cmdArgs[0]); err != nil {
			return fail(err)
		}
		fmt.Println("ok")
		return 0

	case "nack":
		if len(cmdArgs) != 1 {
			fmt.Fprintln(os.Stderr, "usage: txqueue nack <id>")
			return 2
		}
		if err := client.Nack(ctx, cmdArgs[0]); err != nil {
			return fail(err)
		}
		fmt.Println("ok")
		return 0

	case "stats":
		st, err := client.Stats(ctx)
		if err != nil {
			return fail(err)
		}
		return emit(st)

	case "help", "-h", "--help":
		fs.Usage()
		return 0

	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		fs.Usage()
		return 2
	}
}

func emit(v any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "error:", err)
	return 1
}

const usage = `txqueue - CLI for the txqueue durable job-queue API

Usage:
  txqueue [flags] <command> [args]

Commands:
  enqueue <message>   enqueue a message (use -key for idempotency)
  dequeue             lease and print the next message (does not ack)
  ack <id>            acknowledge a message
  nack <id>           negatively acknowledge a message
  stats               print queue statistics

Flags:`
