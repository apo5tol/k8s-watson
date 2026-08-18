package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"k8s-watson/internal/config"
	"k8s-watson/internal/tui"
)

func TestExecuteExitCodes(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		want       int
		wantStderr string
	}{
		{name: "help", args: []string{"--help"}, want: 0},
		{name: "version", args: []string{"--version"}, want: 0},
		{name: "valid configuration", args: []string{"--model", "qwen3"}, want: 0},
		{name: "missing model", want: 1, wantStderr: "model must be specified with --model or K8SWTSN_MODEL\n"},
		{name: "invalid configuration", args: []string{"--model", "qwen3", "--ollama-url", "ftp://example.com"}, want: 1},
		{name: "flag parsing error", args: []string{"--unknown"}, want: 2, wantStderr: "unknown flag: --unknown\n"},
		{name: "positional argument", args: []string{"unexpected"}, want: 2, wantStderr: "unknown command \"unexpected\"\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := executeWithApplicationRunner(
				test.args,
				&stdout,
				&stderr,
				func(tui.Engine) error { return nil },
				func(values config.Values, flags *pflag.FlagSet, runner tuiRunner) error {
					return runApplicationWithKubectlLookup(
						values,
						flags,
						runner,
						func(string) (string, error) { return "kubectl", nil },
					)
				},
			); got != test.want {
				t.Errorf("Execute(%v) = %d, want %d; stderr = %q", test.args, got, test.want, stderr.String())
			}
			if test.wantStderr != "" && stderr.String() != test.wantStderr {
				t.Errorf("stderr = %q, want %q", stderr.String(), test.wantStderr)
			}
		})
	}
}

func TestExecuteHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := Execute([]string{"--help"}, &stdout, &stderr); got != 0 {
		t.Fatalf("Execute() = %d, want 0", got)
	}
	if !strings.Contains(stdout.String(), "--model") {
		t.Errorf("stdout = %q, want help with model flag", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestExecuteShowsVersionWithoutModel(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := Execute([]string{"--version"}, &stdout, &stderr); got != 0 {
		t.Fatalf("Execute() = %d, want 0", got)
	}
	if !strings.Contains(stdout.String(), version) {
		t.Errorf("stdout = %q, want version", stdout.String())
	}
}
