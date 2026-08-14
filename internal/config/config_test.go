package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	now := time.Date(2026, time.August, 12, 14, 35, 7, 0, time.FixedZone("MSK", 3*60*60))
	config, err := Load(Values{
		Model:              "qwen3",
		ModelSet:           true,
		OllamaURL:          DefaultOllamaURL,
		OllamaTimeout:      DefaultOllamaTimeout,
		KubectlTimeout:     DefaultKubectlTimeout,
		MaxToolOutputBytes: DefaultMaxToolOutputBytes,
		MaxHistoryChars:    DefaultMaxHistoryChars,
		MaxIterations:      DefaultMaxIterations,
	}, func(string) (string, bool) { return "", false }, now)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if config.OllamaURL.String() != DefaultOllamaURL {
		t.Errorf("OllamaURL = %q, want %q", config.OllamaURL, DefaultOllamaURL)
	}
	if config.DebugLogPath != "" {
		t.Errorf("DebugLogPath = %q, want empty", config.DebugLogPath)
	}
	if config.Model != "qwen3" || config.OllamaTimeout != DefaultOllamaTimeout || config.KubectlTimeout != DefaultKubectlTimeout || config.MaxToolOutputBytes != DefaultMaxToolOutputBytes || config.MaxHistoryChars != DefaultMaxHistoryChars || config.MaxIterations != DefaultMaxIterations {
		t.Errorf("Load() = %+v, want all configured default values", config)
	}
}

func TestLoadPrecedenceAndDebugLog(t *testing.T) {
	now := time.Date(2026, time.August, 12, 14, 35, 7, 0, time.UTC)
	tests := []struct {
		name         string
		values       Values
		environment  map[string]string
		wantModel    string
		wantURL      string
		wantDebugLog string
	}{
		{
			name:        "environment values",
			values:      Values{OllamaTimeout: DefaultOllamaTimeout, KubectlTimeout: DefaultKubectlTimeout, MaxToolOutputBytes: DefaultMaxToolOutputBytes, MaxHistoryChars: DefaultMaxHistoryChars, MaxIterations: DefaultMaxIterations},
			environment: map[string]string{EnvModel: "env-model", EnvOllamaURL: "https://ollama.example"},
			wantModel:   "env-model", wantURL: "https://ollama.example", wantDebugLog: "",
		},
		{
			name:        "flags override environment",
			values:      Values{Model: "flag-model", ModelSet: true, OllamaURL: "http://flag.example", OllamaURLSet: true, OllamaTimeout: DefaultOllamaTimeout, KubectlTimeout: DefaultKubectlTimeout, MaxToolOutputBytes: DefaultMaxToolOutputBytes, MaxHistoryChars: DefaultMaxHistoryChars, MaxIterations: DefaultMaxIterations},
			environment: map[string]string{EnvModel: "env-model", EnvOllamaURL: "https://ollama.example"},
			wantModel:   "flag-model", wantURL: "http://flag.example", wantDebugLog: "",
		},
		{
			name:        "empty debug log creates name",
			values:      Values{Model: "model", ModelSet: true, OllamaURL: DefaultOllamaURL, OllamaTimeout: DefaultOllamaTimeout, KubectlTimeout: DefaultKubectlTimeout, MaxToolOutputBytes: DefaultMaxToolOutputBytes, MaxHistoryChars: DefaultMaxHistoryChars, MaxIterations: DefaultMaxIterations, DebugLogSet: true},
			environment: map[string]string{},
			wantModel:   "model", wantURL: DefaultOllamaURL, wantDebugLog: "k8s-watson-debug-26-08-12-14-35-07.log",
		},
		{
			name:        "explicit debug log path is preserved",
			values:      Values{Model: "model", ModelSet: true, OllamaURL: DefaultOllamaURL, OllamaTimeout: DefaultOllamaTimeout, KubectlTimeout: DefaultKubectlTimeout, MaxToolOutputBytes: DefaultMaxToolOutputBytes, MaxHistoryChars: DefaultMaxHistoryChars, MaxIterations: DefaultMaxIterations, DebugLog: "custom.log", DebugLogSet: true},
			environment: map[string]string{},
			wantModel:   "model", wantURL: DefaultOllamaURL, wantDebugLog: "custom.log",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := Load(test.values, func(key string) (string, bool) {
				value, ok := test.environment[key]
				return value, ok
			}, now)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if config.Model != test.wantModel || config.OllamaURL.String() != test.wantURL || config.DebugLogPath != test.wantDebugLog {
				t.Errorf("Load() = model %q, URL %q, debug log %q; want %q, %q, %q", config.Model, config.OllamaURL, config.DebugLogPath, test.wantModel, test.wantURL, test.wantDebugLog)
			}
		})
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	valid := Values{Model: "model", ModelSet: true, OllamaURL: DefaultOllamaURL, OllamaTimeout: DefaultOllamaTimeout, KubectlTimeout: DefaultKubectlTimeout, MaxToolOutputBytes: DefaultMaxToolOutputBytes, MaxHistoryChars: DefaultMaxHistoryChars, MaxIterations: DefaultMaxIterations}
	tests := []struct {
		name string
		edit func(*Values)
	}{
		{"missing model", func(values *Values) { values.Model = "" }},
		{"unsupported URL scheme", func(values *Values) { values.OllamaURL = "ftp://ollama.example" }},
		{"relative URL", func(values *Values) { values.OllamaURL = "/api" }},
		{"zero Ollama timeout", func(values *Values) { values.OllamaTimeout = 0 }},
		{"negative kubectl timeout", func(values *Values) { values.KubectlTimeout = -time.Second }},
		{"zero output limit", func(values *Values) { values.MaxToolOutputBytes = 0 }},
		{"zero history limit", func(values *Values) { values.MaxHistoryChars = 0 }},
		{"zero iteration limit", func(values *Values) { values.MaxIterations = 0 }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := valid
			test.edit(&values)
			_, err := Load(values, func(string) (string, bool) { return "", false }, time.Time{})
			if err == nil {
				t.Fatal("Load() error = nil, want error")
			}
		})
	}
}

func TestLoadReportsMissingModel(t *testing.T) {
	_, err := Load(Values{OllamaURL: DefaultOllamaURL, OllamaTimeout: DefaultOllamaTimeout, KubectlTimeout: DefaultKubectlTimeout, MaxToolOutputBytes: DefaultMaxToolOutputBytes, MaxHistoryChars: DefaultMaxHistoryChars, MaxIterations: DefaultMaxIterations}, func(string) (string, bool) { return "", false }, time.Time{})
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Errorf("Load() error = %v, want model error", err)
	}
}
