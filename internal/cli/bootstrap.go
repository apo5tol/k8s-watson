package cli

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/pflag"

	"k8s-watson/internal/chat"
	"k8s-watson/internal/config"
	"k8s-watson/internal/diagnostics"
	"k8s-watson/internal/models/ollama"
	"k8s-watson/internal/tools"
	"k8s-watson/internal/tools/kubectl"
)

func runApplication(values config.Values, flags *pflag.FlagSet, runTUI tuiRunner) (returnErr error) {
	return runApplicationWithKubectlLookup(values, flags, runTUI, exec.LookPath)
}

func runApplicationWithKubectlLookup(
	values config.Values,
	flags *pflag.FlagSet,
	runTUI tuiRunner,
	lookupKubectl func(string) (string, error),
) (returnErr error) {
	values = valuesFromFlags(values, flags)

	loadedConfig, err := config.Load(values, os.LookupEnv, time.Now())
	if err != nil {
		return err
	}

	logger, closeLogger, err := diagnostics.New(loadedConfig.DebugLogPath)
	if err != nil {
		return fmt.Errorf("initialize diagnostics: %w", err)
	}
	logger.Info("application started", "event", "application_started", "model", loadedConfig.Model)
	defer func() {
		logger.Info("application stopped", "event", "application_stopped", "model", loadedConfig.Model)
		if err := closeLogger(); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("close diagnostics: %w", err)
		}
	}()
	kubectlPath, err := lookupKubectl("kubectl")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return errors.New("kubectl was not found in PATH")
		}
		return fmt.Errorf("find kubectl: %w", err)
	}
	logger.Info("kubectl found", "event", "kubectl_found", "path", kubectlPath)

	engine, err := newChatEngine(loadedConfig, kubectlPath, logger)
	if err != nil {
		return err
	}
	defer engine.Close()

	return runTUI(engine)
}

func newChatEngine(loadedConfig config.Config, kubectlPath string, logger *slog.Logger) (*chat.Engine, error) {
	model, err := ollama.New(loadedConfig.OllamaURL, loadedConfig.Model, loadedConfig.OllamaTimeout, logger)
	if err != nil {
		return nil, fmt.Errorf("initialize Ollama client: %w", err)
	}
	executor, err := kubectl.NewExecutor(kubectl.ExecutorConfig{
		Path:           kubectlPath,
		Timeout:        loadedConfig.KubectlTimeout,
		MaxOutputBytes: loadedConfig.MaxToolOutputBytes,
		Logger:         logger,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize kubectl executor: %w", err)
	}
	resolver, err := kubectl.NewTargetResolver(kubectlPath, loadedConfig.KubectlTimeout)
	if err != nil {
		return nil, fmt.Errorf("initialize kubectl target resolver: %w", err)
	}
	registry, err := tools.NewRegistry(kubectl.New(executor, resolver))
	if err != nil {
		return nil, fmt.Errorf("initialize tool registry: %w", err)
	}
	engine, err := chat.New(model, registry, chat.Config{
		MaxHistoryChars: loadedConfig.MaxHistoryChars,
		MaxInputBytes:   16 * 1024,
		MaxIterations:   loadedConfig.MaxIterations,
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("initialize chat engine: %w", err)
	}
	return engine, nil
}

func valuesFromFlags(values config.Values, flags *pflag.FlagSet) config.Values {
	values.ModelSet = flags.Changed("model")
	values.OllamaURLSet = flags.Changed("ollama-url")
	values.DebugLogSet = flags.Changed("debug-log")
	if values.DebugLog == autoDebugLogFlagValue {
		values.DebugLog = ""
	}

	return values
}
