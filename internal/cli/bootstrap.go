package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/pflag"

	"k8s-watson/internal/config"
	"k8s-watson/internal/diagnostics"
)

func runApplication(values config.Values, flags *pflag.FlagSet, runTUI func(config.Config) error) (returnErr error) {
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

	return runTUI(loadedConfig)
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
