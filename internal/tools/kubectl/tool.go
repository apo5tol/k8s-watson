package kubectl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"

	"k8s-watson/internal/agent"
	"k8s-watson/internal/tools"
)

const (
	toolName          = "kubectl"
	MetadataNamespace = "namespace"
)

type inputSchema struct {
	Type                 string                      `json:"type"`
	Properties           map[string]inputSchemaField `json:"properties"`
	Required             []string                    `json:"required"`
	AdditionalProperties bool                        `json:"additionalProperties"`
}

type inputSchemaField struct {
	Type  string            `json:"type"`
	Items *inputSchemaField `json:"items,omitempty"`
}

var kubectlInputSchema = inputSchema{
	Type: "object",
	Properties: map[string]inputSchemaField{
		"verb": {Type: "string"},
		"args": {
			Type: "array",
			Items: &inputSchemaField{
				Type: "string",
			},
		},
	},
	Required:             []string{"verb"},
	AdditionalProperties: false,
}

type Executor interface {
	Execute(context.Context, []string) (agent.ToolResult, error)
}

type Tool struct {
	executor Executor
}

func New(executor Executor) *Tool {
	return &Tool{executor: executor}
}

func (*Tool) Definition() agent.ToolDefinition {
	inputSchema, err := json.Marshal(kubectlInputSchema)
	if err != nil {
		panic(fmt.Sprintf("marshal kubectl input schema: %v", err))
	}

	return agent.ToolDefinition{
		Name:        toolName,
		Description: "Runs a non-interactive kubectl command against the current Kubernetes context.",
		InputSchema: inputSchema,
	}
}

func (t *Tool) Prepare(_ context.Context, call agent.ToolCall) (tools.PreparedCall, error) {
	input, err := decodeCall(call.Arguments)
	if err != nil {
		return nil, err
	}
	if err := validateCall(input.verb, input.args); err != nil {
		return nil, err
	}

	namespace, err := namespaceFromArgs(input.args)
	if err != nil {
		return nil, err
	}

	argv := make([]string, 0, len(input.args)+2)
	argv = append(argv, toolName, input.verb)
	argv = append(argv, input.args...)
	metadata := map[string]string{}
	if namespace != "" {
		metadata[MetadataNamespace] = namespace
	}

	return preparedCall{
		argv:             argv,
		display:          quoteCommand(argv),
		requiresApproval: input.verb != "get" && input.verb != "describe" && input.verb != "list",
		metadata:         metadata,
		executor:         t.executor,
	}, nil
}

type callInput struct {
	verb string
	args []string
}

func decodeCall(arguments json.RawMessage) (callInput, error) {
	var input struct {
		Verb string          `json:"verb"`
		Args json.RawMessage `json:"args"`
	}

	decoder := json.NewDecoder(strings.NewReader(string(arguments)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return callInput{}, fmt.Errorf("%w: decode arguments: %w", ErrInvalidCall, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return callInput{}, fmt.Errorf("%w: trailing JSON data", ErrInvalidCall)
	}
	if input.Verb == "" || strings.IndexFunc(input.Verb, unicode.IsSpace) >= 0 {
		return callInput{}, fmt.Errorf("%w: verb is required and cannot contain whitespace", ErrInvalidCall)
	}

	args := []string{}
	if input.Args != nil {
		if string(input.Args) == "null" {
			return callInput{}, fmt.Errorf("%w: args cannot be null", ErrInvalidCall)
		}
		if err := json.Unmarshal(input.Args, &args); err != nil {
			return callInput{}, fmt.Errorf("%w: args must be an array of strings: %w", ErrInvalidCall, err)
		}
		if args == nil {
			return callInput{}, fmt.Errorf("%w: args cannot be null", ErrInvalidCall)
		}
	}

	return callInput{verb: input.Verb, args: args}, nil
}

func validateCall(verb string, args []string) error {
	if hasShellSyntax(verb) {
		return fmt.Errorf("%w: shell syntax in verb", ErrForbiddenCall)
	}
	if isForbiddenVerb(verb) {
		return fmt.Errorf("%w: verb %q", ErrForbiddenCall, verb)
	}

	for _, arg := range args {
		if hasShellSyntax(arg) {
			return fmt.Errorf("%w: shell syntax in argument %q", ErrForbiddenCall, arg)
		}
		if isForbiddenFlag(arg) {
			return fmt.Errorf("%w: flag %q", ErrForbiddenCall, arg)
		}
	}
	if strings.EqualFold(verb, "config") && hasConfigMutation(args) {
		return fmt.Errorf("%w: config mutation", ErrForbiddenCall)
	}

	return nil
}

func isForbiddenVerb(verb string) bool {
	switch strings.ToLower(verb) {
	case "edit", "port-forward", "attach", "proxy", "exec":
		return true
	default:
		return false
	}
}

func hasShellSyntax(value string) bool {
	return strings.ContainsAny(value, "|;&<>") || strings.Contains(value, "$(") || strings.Contains(value, "`")
}

func isForbiddenFlag(arg string) bool {
	if hasFlagName(arg, "--context") || hasFlagName(arg, "--kubeconfig") {
		return true
	}
	if isEnabledFlag(arg, "--follow") || isEnabledFlag(arg, "-f") {
		return true
	}
	if isEnabledFlag(arg, "--watch") || isEnabledFlag(arg, "--watch-only") || isEnabledFlag(arg, "-w") {
		return true
	}
	if isEnabledFlag(arg, "--attach") || isEnabledFlag(arg, "--interactive") || isEnabledFlag(arg, "--stdin") || isEnabledFlag(arg, "--tty") {
		return true
	}
	return isInteractiveShorthand(arg)
}

func hasFlagName(arg, name string) bool {
	return arg == name || strings.HasPrefix(arg, name+"=")
}

func isEnabledFlag(arg, name string) bool {
	if arg == name {
		return true
	}
	if !strings.HasPrefix(arg, name+"=") {
		return false
	}

	value := strings.TrimPrefix(arg, name+"=")
	enabled, err := strconv.ParseBool(value)
	return err != nil || enabled
}

func isInteractiveShorthand(arg string) bool {
	if !strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "--") {
		return false
	}

	shorthand := strings.TrimPrefix(arg, "-")
	shorthand = strings.SplitN(shorthand, "=", 2)[0]
	return strings.ContainsAny(shorthand, "it")
}

func hasConfigMutation(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "set", "unset", "set-cluster", "set-context", "set-credentials", "rename-context", "use-context", "delete-cluster", "delete-context", "delete-user":
			return true
		}
	}
	return false
}

func namespaceFromArgs(args []string) (string, error) {
	namespace := ""
	for index := 0; index < len(args); index++ {
		arg := args[index]
		value := ""
		switch {
		case arg == "-n" || arg == "--namespace":
			if index+1 >= len(args) {
				return "", fmt.Errorf("%w: namespace value is required", ErrInvalidCall)
			}
			index++
			value = args[index]
		case strings.HasPrefix(arg, "--namespace="):
			value = strings.TrimPrefix(arg, "--namespace=")
		default:
			continue
		}
		if value == "" || strings.HasPrefix(value, "-") {
			return "", fmt.Errorf("%w: namespace value is required", ErrInvalidCall)
		}
		if namespace != "" {
			return "", fmt.Errorf("%w: namespace specified more than once", ErrInvalidCall)
		}
		namespace = value
	}
	return namespace, nil
}

type preparedCall struct {
	argv             []string
	display          string
	requiresApproval bool
	metadata         map[string]string
	executor         Executor
}

func (c preparedCall) Display() string {
	return c.display
}

func (c preparedCall) RequiresApproval() bool {
	return c.requiresApproval
}

func (c preparedCall) Metadata() map[string]string {
	metadata := make(map[string]string, len(c.metadata))
	for key, value := range c.metadata {
		metadata[key] = value
	}
	return metadata
}

func (c preparedCall) Execute(ctx context.Context) (agent.ToolResult, error) {
	if c.executor == nil {
		return agent.ToolResult{}, ErrExecutorRequired
	}
	return c.executor.Execute(ctx, append([]string{}, c.argv...))
}

func quoteCommand(argv []string) string {
	quoted := make([]string, len(argv))
	for index, arg := range argv {
		quoted[index] = quoteArgument(arg)
	}
	return strings.Join(quoted, " ")
}

func quoteArgument(arg string) string {
	if arg != "" && strings.IndexFunc(arg, isUnsafeShellCharacter) == -1 {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
}

func isUnsafeShellCharacter(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("_@%+=:,./-", r)
}
