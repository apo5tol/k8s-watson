package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"k8s-watson/internal/config"
	"k8s-watson/internal/tui"
)

const (
	version               = "v0.1.0"
	autoDebugLogFlagValue = "__auto_debug_log__"
)

type tuiRunner func(tui.Engine) error

type applicationRunner func(config.Values, *pflag.FlagSet, tuiRunner) error

func Execute(args []string, stdout, stderr io.Writer) int {
	return execute(args, stdout, stderr, tui.Run)
}

func execute(args []string, stdout, stderr io.Writer, runTUI tuiRunner) int {
	return executeWithApplicationRunner(args, stdout, stderr, runTUI, runApplication)
}

func executeWithApplicationRunner(
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	runTUI tuiRunner,
	runApplication applicationRunner,
) int {
	command := newRootCommand(stdout, stderr, runTUI, runApplication)
	command.SetArgs(args)

	if err := command.Execute(); err != nil {
		if _, writeErr := fmt.Fprintln(stderr, err); writeErr != nil {
			return 1
		}

		var usageErr *usageError
		if errors.As(err, &usageErr) {
			return 2
		}

		return 1
	}

	return 0
}

func NewRootCommand(stdout, stderr io.Writer) *cobra.Command {
	return newRootCommand(stdout, stderr, tui.Run, runApplication)
}

func newRootCommand(
	stdout io.Writer,
	stderr io.Writer,
	runTUI tuiRunner,
	runApplication applicationRunner,
) *cobra.Command {
	var showVersion bool
	var values config.Values

	command := &cobra.Command{
		Use:           "k8s-watson",
		Short:         "Kubernetes diagnostics and analysis assistant",
		Args:          noArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if showVersion {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), version)
				return err
			}

			return runApplication(values, cmd.Flags(), runTUI)
		},
	}

	command.SetOut(stdout)
	command.SetErr(stderr)
	command.Flags().BoolVar(&showVersion, "version", false, "print the application version and exit")
	command.Flags().StringVar(&values.Model, "model", "", "model name")
	command.Flags().StringVar(&values.OllamaURL, "ollama-url", config.DefaultOllamaURL, "ollama server URL")
	command.Flags().DurationVar(&values.OllamaTimeout, "ollama-timeout", config.DefaultOllamaTimeout, "timeout for an Ollama request")
	command.Flags().DurationVar(&values.KubectlTimeout, "kubectl-timeout", config.DefaultKubectlTimeout, "timeout for a kubectl command")
	command.Flags().IntVar(&values.MaxToolOutputBytes, "max-tool-output-bytes", config.DefaultMaxToolOutputBytes, "maximum kubectl output size in bytes")
	command.Flags().IntVar(&values.MaxHistoryChars, "max-history-chars", config.DefaultMaxHistoryChars, "maximum chat history size in characters")
	command.Flags().IntVar(&values.MaxIterations, "max-iterations", config.DefaultMaxIterations, "maximum agent iterations per turn")
	command.Flags().StringVar(&values.DebugLog, "debug-log", "", "write debug log; omit the path to use an auto-generated file name")
	command.Flags().Lookup("debug-log").NoOptDefVal = autoDebugLogFlagValue
	command.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &usageError{err: err}
	})

	return command
}

func noArgs(_ *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}

	return &usageError{err: fmt.Errorf("unknown command %q", args[0])}
}

type usageError struct {
	err error
}

func (e *usageError) Error() string {
	return e.err.Error()
}

func (e *usageError) Unwrap() error {
	return e.err
}
