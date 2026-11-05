// Command argos runs the Argos control center: HTTP API, WebSocket stream
// and the embedded web UI.
//
// Usage:
//
//	argos                  start the server
//	argos hash-password    read a password on stdin, print its argon2id hash
//	argos totp-init        print a new TOTP secret and its otpauth:// URI
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fs0ciety7000/argos/internal/auth"
	"github.com/fs0ciety7000/argos/internal/server"
)

func main() {
	if len(os.Args) > 1 {
		if err := runCommand(os.Args[1], os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "argos:", err)
			os.Exit(2)
		}
		return
	}
	if err := serve(); err != nil {
		fmt.Fprintln(os.Stderr, "argos:", err)
		os.Exit(1)
	}
}

func runCommand(name string, in io.Reader, out io.Writer) error {
	switch name {
	case "hash-password":
		line, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read password: %w", err)
		}
		password := strings.TrimRight(line, "\r\n")
		if len(password) < 12 {
			return errors.New("password must be at least 12 characters")
		}
		hash, err := auth.HashPassword(password, auth.DefaultArgon2Params)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, hash)
		return err
	case "totp-init":
		secret, err := auth.GenerateTOTPSecret()
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "ARGOS_TOTP_SECRET=%s\n%s\n", secret, auth.TOTPURI(secret, "admin"))
		return err
	default:
		return fmt.Errorf("unknown command %q (want hash-password or totp-init)", name)
	}
}

func serve() error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	addr := os.Getenv("ARGOS_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	authSvc, err := auth.New(auth.Config{
		PasswordHash: os.Getenv("ARGOS_ADMIN_PASSWORD_HASH"),
		TOTPSecret:   os.Getenv("ARGOS_TOTP_SECRET"),
	}, logger)
	if err != nil {
		return fmt.Errorf("auth config: %w", err)
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           server.New(logger, authSvc),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", addr, "auth", authSvc.Enabled())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
