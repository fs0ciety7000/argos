package deploy

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The guard script must refuse anything but its three actions, before ever
// calling docker (exit 64 = usage error).
func TestGuardScriptRejects(t *testing.T) {
	cases := [][]string{
		{},
		{"rm", "x"},
		{"exec", "web", "sh"},
		{"list", "--all"},
		{"stats", "now"},
		{"restart"},
		{"restart", "a", "b"},
		{"restart", "-f"},
		{"restart", "--time=0"},
		{"restart", "a;id"},
		{"restart", "$(id)"},
		{"restart", "`id`"},
		{"restart", "../x"},
		{"restart", ".x"},
		{"restart", "a b"},
		{"restart", "a\nb"},
		{"restart", strings.Repeat("a", 129)},
		{"logs"},
		{"logs", "web"},
		{"logs", "web", "0"},
		{"logs", "web", "1001"},
		{"logs", "web", "-5"},
		{"logs", "web", "010"},
		{"logs", "web", "200", "--since=1h"},
		{"logs", "-f", "200"},
		{"logs", "a;id", "200"},
		{"logs", "web", "1e3"},
	}
	for _, protected := range []string{"coolify", "coolify-proxy", "coolify-db", "coolify-redis", "coolify-realtime", "coolify-sentinel"} {
		err := exec.Command("bash", "./argos-docker", "restart", protected).Run()
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 65 {
			t.Errorf("%s: want exit 65 (protected), got %v", protected, err)
		}
	}
	for _, args := range cases {
		err := exec.Command("bash", append([]string{"./argos-docker"}, args...)...).Run()
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 64 {
			t.Errorf("%q: want exit 64, got %v", args, err)
		}
	}
}

func TestGuardScriptAcceptsValidLogs(t *testing.T) {
	for _, n := range []string{"1", "200", "1000"} {
		err := exec.Command("bash", "./argos-docker", "logs", "web", n).Run()
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 64 {
			t.Errorf("logs web %s rejected as usage error", n)
		}
	}
}

func TestGuardScriptAcceptsValidNames(t *testing.T) {
	for _, name := range []string{"web", "coolify-app", "app_1.web-2", "mgrx66slhilnqavtdqs9a9fn-012345"} {
		err := exec.Command("bash", "./argos-docker", "restart", name).Run()
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 64 {
			t.Errorf("%q rejected as usage error", name)
		}
	}
}

// guardWithFakeDocker copies the script with PATH pointing at a fake
// docker, so the logs lifecycle can be exercised without a daemon.
func guardWithFakeDocker(t *testing.T) (script, dir string) {
	t.Helper()
	dir = t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := `#!/bin/bash
case "$1" in
  inspect) echo "/web 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" ;;
  logs) if [ -n "${FAKE_SHORT:-}" ]; then echo line1; exit 0; fi
        while true; do echo tick; sleep 0.1; done ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("argos-docker")
	if err != nil {
		t.Fatal(err)
	}
	s := regexp.MustCompile(`(?m)^export PATH=.*$`).ReplaceAllString(string(src), "export PATH="+bin+":/usr/bin:/bin")
	s = strings.ReplaceAll(s, "/run/argos-docker", filepath.Join(dir, "run"))
	script = filepath.Join(dir, "guard")
	if err := os.WriteFile(script, []byte(s), 0o755); err != nil {
		t.Fatal(err)
	}
	return script, dir
}

func fakeDockerAlive(dir string) bool {
	out, _ := exec.Command("pgrep", "-f", filepath.Join(dir, "bin", "docker")).Output()
	return len(bytes.TrimSpace(out)) > 0
}

func TestLogsStopsWhenStdinCloses(t *testing.T) {
	script, dir := guardWithFakeDocker(t)
	cmd := exec.Command("bash", script, "logs", "web", "200")
	stdin, _ := cmd.StdinPipe()
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	stdin.Close() // what the SSH channel closing does
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("script kept following after stdin EOF")
	}
	if !strings.Contains(out.String(), "tick") {
		t.Fatalf("no log output: %q", out.String())
	}
	time.Sleep(200 * time.Millisecond)
	if fakeDockerAlive(dir) {
		t.Fatal("docker logs survived the stream")
	}
}

func TestLogsEndsWhenDockerLogsEnds(t *testing.T) {
	script, _ := guardWithFakeDocker(t)
	cmd := exec.Command("bash", script, "logs", "web", "200")
	cmd.Env = append(os.Environ(), "FAKE_SHORT=1")
	stdin, _ := cmd.StdinPipe() // stays open
	defer stdin.Close()
	start := time.Now()
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != "line1" || time.Since(start) > 2*time.Second {
		t.Fatalf("out=%q err=%v after %v", out, err, time.Since(start))
	}
}

func TestLogsProtectedAndSlots(t *testing.T) {
	script, dir := guardWithFakeDocker(t)
	err := exec.Command("bash", script, "logs", "coolify-db", "200").Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 65 {
		t.Fatalf("protected logs: %v", err)
	}
	type stream struct {
		cmd *exec.Cmd
		in  io.WriteCloser
	}
	var running []stream
	for range 6 {
		c := exec.Command("bash", script, "logs", "web", "200")
		in, _ := c.StdinPipe()
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		running = append(running, stream{c, in})
	}
	time.Sleep(400 * time.Millisecond)
	err = exec.Command("bash", script, "logs", "web", "200").Run()
	if !errors.As(err, &ee) || ee.ExitCode() != 66 {
		t.Fatalf("7th stream: want exit 66, got %v", err)
	}
	for _, r := range running { // close stdin like the SSH channel would
		_ = r.in.Close()
		_ = r.cmd.Wait()
	}
	time.Sleep(200 * time.Millisecond)
	if fakeDockerAlive(dir) {
		t.Fatal("followers survived their streams")
	}
}
