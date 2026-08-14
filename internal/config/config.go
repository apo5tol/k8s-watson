package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	EnvModel                  = "K8SWTSN_MODEL"
	EnvOllamaURL              = "K8SWTSN_OLLAMA_URL"
	DefaultOllamaURL          = "http://localhost:11434"
	DefaultOllamaTimeout      = 2 * time.Minute
	DefaultKubectlTimeout     = 30 * time.Second
	DefaultMaxToolOutputBytes = 16 * 1024
	DefaultMaxHistoryChars    = 65_536
	DefaultMaxIterations      = 8
)

type Config struct {
	Model              string
	OllamaURL          *url.URL
	OllamaTimeout      time.Duration
	KubectlTimeout     time.Duration
	MaxToolOutputBytes int
	MaxHistoryChars    int
	MaxIterations      int
	DebugLogPath       string
}

type Values struct {
	Model              string
	ModelSet           bool
	OllamaURL          string
	OllamaURLSet       bool
	OllamaTimeout      time.Duration
	KubectlTimeout     time.Duration
	MaxToolOutputBytes int
	MaxHistoryChars    int
	MaxIterations      int
	DebugLog           string
	DebugLogSet        bool
}

func Load(values Values, lookupEnv func(string) (string, bool), now time.Time) (Config, error) {
	values = applyDefaults(values, lookupEnv, now)

	parsedOllamaURL, err := validate(values)
	if err != nil {
		return Config{}, err
	}

	return Config{
		Model:              values.Model,
		OllamaURL:          parsedOllamaURL,
		OllamaTimeout:      values.OllamaTimeout,
		KubectlTimeout:     values.KubectlTimeout,
		MaxToolOutputBytes: values.MaxToolOutputBytes,
		MaxHistoryChars:    values.MaxHistoryChars,
		MaxIterations:      values.MaxIterations,
		DebugLogPath:       values.DebugLog,
	}, nil
}

func applyDefaults(values Values, lookupEnv func(string) (string, bool), now time.Time) Values {
	if !values.ModelSet {
		if envModel, ok := lookupEnv(EnvModel); ok {
			values.Model = envModel
		}
	}

	if !values.OllamaURLSet {
		if envURL, ok := lookupEnv(EnvOllamaURL); ok {
			values.OllamaURL = envURL
		}
	}
	if values.OllamaURL == "" {
		values.OllamaURL = DefaultOllamaURL
	}

	if values.DebugLogSet && values.DebugLog == "" {
		values.DebugLog = fmt.Sprintf("k8s-watson-debug-%s.log", now.Format("06-01-02-15-04-05"))
	}

	return values
}

func validate(values Values) (*url.URL, error) {
	if strings.TrimSpace(values.Model) == "" {
		return nil, fmt.Errorf("model must be specified with --model or %s", EnvModel)
	}

	parsedOllamaURL, err := validateOllamaURL(values.OllamaURL)
	if err != nil {
		return nil, err
	}

	if values.OllamaTimeout <= 0 {
		return nil, errors.New("ollama timeout must be positive")
	}
	if values.KubectlTimeout <= 0 {
		return nil, errors.New("kubectl timeout must be positive")
	}
	if values.MaxToolOutputBytes <= 0 {
		return nil, errors.New("max tool output bytes must be positive")
	}
	if values.MaxHistoryChars <= 0 {
		return nil, errors.New("max history chars must be positive")
	}
	if values.MaxIterations <= 0 {
		return nil, errors.New("max iterations must be positive")
	}

	return parsedOllamaURL, nil
}

func validateOllamaURL(rawURL string) (*url.URL, error) {
	parsedURL, err := url.ParseRequestURI(rawURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return nil, errors.New("ollama URL must be an absolute HTTP or HTTPS URL")
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, errors.New("ollama URL must use HTTP or HTTPS")
	}

	return parsedURL, nil
}
