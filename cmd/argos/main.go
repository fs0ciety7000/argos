// Command argos runs the Argos control center: HTTP API, WebSocket stream
// and the embedded web UI.
//
// Usage:
//
//	argos                  start the server
//	argos hash-password    read a password on stdin, print its argon2id hash
//	argos totp-init        print a new TOTP secret, its otpauth:// URI and a QR code
//	argos totp-code        read a TOTP secret, print the code expected right now
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
	"github.com/fs0ciety7000/argos/internal/cfaccess"
	"github.com/fs0ciety7000/argos/internal/server"
	"golang.org/x/term"
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
		password, err := readPassword(in)
		if err != nil {
			return err
		}
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
		uri := auth.TOTPURI(secret, "admin")
		fmt.Fprintln(out, "Scanne ce QR code avec ton application d'authentification :")
		fmt.Fprintln(out)
		if err := printQR(out, uri); err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "\nURI : %s\n\nValeur à mettre dans Coolify (ARGOS_TOTP_SECRET) :\n%s\n", uri, secret)
		return err
	case "totp-code":
		secret, err := readSecret(in, "Secret TOTP : ")
		if err != nil {
			return err
		}
		code, left, err := auth.CurrentCode(secret, time.Now())
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "Code attendu maintenant : %s (encore %d s)\n", code, int(left.Seconds()))
		return err
	default:
		return fmt.Errorf("unknown command %q (want hash-password, totp-init or totp-code)", name)
	}
}

// readPassword prompts twice with echo off when stdin is a terminal, and
// reads a single line otherwise (piped input, scripts, tests).
func readPassword(in io.Reader) (string, error) {
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fd := int(f.Fd())
		fmt.Fprint(os.Stderr, "Mot de passe : ")
		first, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		fmt.Fprint(os.Stderr, "Confirmation : ")
		second, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		if string(first) != string(second) {
			return "", errors.New("the two passwords do not match")
		}
		return string(first), nil
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read password: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// readSecret reads one value with echo off on a terminal, or one line otherwise.
func readSecret(in io.Reader, prompt string) (string, error) {
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(os.Stderr, prompt)
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", fmt.Errorf("read secret: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read secret: %w", err)
	}
	return strings.TrimSpace(line), nil
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

	edge, err := accessMiddleware(logger)
	if err != nil {
		return fmt.Errorf("cloudflare access config: %w", err)
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           server.New(logger, authSvc, edge),
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

// accessMiddleware enables Cloudflare Access JWT verification when both
// ARGOS_CF_ACCESS_TEAM_DOMAIN and ARGOS_CF_ACCESS_AUD are set. /health stays
// open for the container health check.
func accessMiddleware(logger *slog.Logger) (server.Middleware, error) {
	team, aud := os.Getenv("ARGOS_CF_ACCESS_TEAM_DOMAIN"), os.Getenv("ARGOS_CF_ACCESS_AUD")
	switch {
	case team == "" && aud == "":
		logger.Warn("cloudflare access verification disabled: requests reaching the origin directly are not filtered")
		return nil, nil
	case team == "" || aud == "":
		return nil, errors.New("set both ARGOS_CF_ACCESS_TEAM_DOMAIN and ARGOS_CF_ACCESS_AUD, or neither")
	}
	v, err := cfaccess.New(team, aud)
	if err != nil {
		return nil, err
	}
	logger.Info("cloudflare access verification enabled", "team", team)
	return v.Middleware(logger, "/health"), nil
}
