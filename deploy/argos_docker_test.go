package deploy

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
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
	}
	for _, args := range cases {
		err := exec.Command("bash", append([]string{"./argos-docker"}, args...)...).Run()
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 64 {
			t.Errorf("%q: want exit 64, got %v", args, err)
		}
	}
}

func TestGuardScriptAcceptsValidNames(t *testing.T) {
	for _, name := range []string{"web", "coolify-proxy", "app_1.web-2", "mgrx66slhilnqavtdqs9a9fn-012345"} {
		err := exec.Command("bash", "./argos-docker", "restart", name).Run()
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 64 {
			t.Errorf("%q rejected as usage error", name)
		}
	}
}
