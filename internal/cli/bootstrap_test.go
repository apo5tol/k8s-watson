package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"k8s-watson/internal/config"
	"k8s-watson/internal/tui"
)

func TestValuesFromFlags(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		values        config.Values
		wantModelSet  bool
		wantURLSet    bool
		wantDebugSet  bool
		wantDebugPath string
	}{
		{name: "unchanged flags remain unset", values: config.Values{Model: "model", OllamaURL: config.DefaultOllamaURL}},
		{name: "specified model and URL flags are marked", args: []string{"--model=qwen3", "--ollama-url=http://ollama.example"}, values: config.Values{Model: "model", OllamaURL: config.DefaultOllamaURL}, wantModelSet: true, wantURLSet: true},
		{name: "bare debug log flag is marked and cleared for auto path", args: []string{"--debug-log"}, values: config.Values{Model: "model", OllamaURL: config.DefaultOllamaURL}, wantDebugSet: true},
		{name: "explicit debug log path is preserved", args: []string{"--debug-log=custom.log"}, values: config.Values{Model: "model", OllamaURL: config.DefaultOllamaURL}, wantDebugSet: true, wantDebugPath: "custom.log"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := test.values
			flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
			flags.StringVar(&values.Model, "model", values.Model, "")
			flags.StringVar(&values.OllamaURL, "ollama-url", values.OllamaURL, "")
			flags.StringVar(&values.DebugLog, "debug-log", values.DebugLog, "")
			flags.Lookup("debug-log").NoOptDefVal = autoDebugLogFlagValue
			if err := flags.Parse(test.args); err != nil {
				t.Fatalf("Parse() error = %v", err)
			}

			got := valuesFromFlags(values, flags)
			if got.ModelSet != test.wantModelSet || got.OllamaURLSet != test.wantURLSet || got.DebugLogSet != test.wantDebugSet || got.DebugLog != test.wantDebugPath {
				t.Errorf("valuesFromFlags() = %+v, want model set %t, URL set %t, debug set %t, debug path %q", got, test.wantModelSet, test.wantURLSet, test.wantDebugSet, test.wantDebugPath)
			}
		})
	}
}

func TestRunApplicationWrapsDiagnosticsInitializationError(t *testing.T) {
	values := testConfigValues()
	values.DebugLog = filepath.Join(t.TempDir(), "missing", "diagnostics.log")
	values.DebugLogSet = true

	err := runApplication(
		values,
		changedFlags(t, values.Model, values.DebugLog),
		func(tui.Client, int) error { return nil },
	)
	if err == nil || !strings.Contains(err.Error(), "initialize diagnostics") {
		t.Errorf("runApplication() error = %v, want diagnostics initialization error", err)
	}
}

func TestRunApplicationLogsLifecycle(t *testing.T) {
	values := testConfigValues()
	values.DebugLog = filepath.Join(t.TempDir(), "diagnostics.log")
	values.DebugLogSet = true
	runCalled := false

	if err := runApplication(
		values,
		changedFlags(t, values.Model, values.DebugLog),
		func(client tui.Client, maxHistoryChars int) error {
			runCalled = true
			if client == nil {
				t.Error("TUI client = nil, want Ollama client")
			}
			if maxHistoryChars != config.DefaultMaxHistoryChars {
				t.Errorf("history limit = %d, want %d", maxHistoryChars, config.DefaultMaxHistoryChars)
			}
			return nil
		},
	); err != nil {
		t.Fatalf("runApplication() error = %v", err)
	}
	if !runCalled {
		t.Fatal("TUI runner was not called")
	}
	contents, err := os.ReadFile(values.DebugLog)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(contents), "application_started") || !strings.Contains(string(contents), "application_stopped") {
		t.Errorf("diagnostics log = %q, want lifecycle events", contents)
	}
}

func testConfigValues() config.Values {
	return config.Values{Model: "qwen3", ModelSet: true, OllamaURL: config.DefaultOllamaURL, OllamaTimeout: config.DefaultOllamaTimeout, KubectlTimeout: config.DefaultKubectlTimeout, MaxToolOutputBytes: config.DefaultMaxToolOutputBytes, MaxHistoryChars: config.DefaultMaxHistoryChars, MaxIterations: config.DefaultMaxIterations}
}

func changedFlags(t *testing.T, model, debugLog string) *pflag.FlagSet {
	t.Helper()
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("model", "", "")
	flags.String("ollama-url", "", "")
	flags.String("debug-log", "", "")
	if err := flags.Set("model", model); err != nil {
		t.Fatalf("Set model flag error = %v", err)
	}
	if err := flags.Set("debug-log", debugLog); err != nil {
		t.Fatalf("Set debug log flag error = %v", err)
	}

	return flags
}
