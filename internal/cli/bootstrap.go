package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/pflag"

	"k8s-watson/internal/chat"
	"k8s-watson/internal/config"
	"k8s-watson/internal/diagnostics"
	"k8s-watson/internal/models/ollama"
	"k8s-watson/internal/tools"
)

func runApplication(values config.Values, flags *pflag.FlagSet, runTUI tuiRunner) (returnErr error) {
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

	model, err := ollama.New(loadedConfig.OllamaURL, loadedConfig.Model, loadedConfig.OllamaTimeout, logger)
	if err != nil {
		return fmt.Errorf("initialize Ollama client: %w", err)
	}
	registry, err := tools.NewRegistry()
	if err != nil {
		return fmt.Errorf("initialize tool registry: %w", err)
	}
	engine, err := chat.New(model, registry, chat.Config{
		MaxHistoryChars: loadedConfig.MaxHistoryChars,
		MaxInputBytes:   16 * 1024,
		MaxIterations:   loadedConfig.MaxIterations,
	}, logger)
	if err != nil {
		return fmt.Errorf("initialize chat engine: %w", err)
	}
	defer engine.Close()

	return runTUI(engine)
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
