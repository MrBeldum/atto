package server

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"atto/config"
)

// RunStdio implements "atto app-server": JSON-RPC over stdin/stdout.
func RunStdio(version string) error {
	if err := config.Ensure(); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return New(version, cwd).ServeStdio(ctx, os.Stdin, os.Stdout)
}

// RunHTTP implements "atto serve": the protocol over HTTP + SSE plus the
// web client.
func RunHTTP(version string, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:7878", "address to listen on")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := config.Ensure(); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	token, err := LoadOrCreateToken()
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: New(version, cwd).HTTPHandler(token), ReadHeaderTimeout: 10 * time.Second}

	addr := ln.Addr().String()
	fmt.Fprintf(out, "atto %s serving %s\n", version, cwd)
	fmt.Fprintf(out, "  web:    http://%s/#token=%s\n", addr, token)
	fmt.Fprintf(out, "  rpc:    POST http://%s/rpc   events: GET http://%s/events  (Authorization: Bearer <token>)\n", addr, addr)
	fmt.Fprintf(out, "  token:  %s\n", TokenPath())
	if !IsLoopback(addr) {
		fmt.Fprintln(out, "  warning: listening beyond this machine without TLS; prefer a private network such as Tailscale.")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		sh, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sh)
	}()
	if err := srv.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	return nil
}
