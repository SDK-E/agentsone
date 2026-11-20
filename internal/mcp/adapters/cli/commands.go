// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/SDK-E/agentsone/internal/mcp/app"
	"github.com/SDK-E/agentsone/internal/mcp/domain"
)

// Exit codes of agentctl.
const (
	ExitOK       = 0
	ExitFailure  = 1 // the daemon could not be reached, or another runtime failure
	ExitUsage    = 2 // the command line is wrong
	ExitClient   = 3 // the daemon answered a 4xx problem
	ExitServer   = 4 // the daemon answered a 5xx problem
	bodyJSONFlag = "body-json"
)

// IO is where agentctl reads and writes.
type IO struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// usageError is a mistake in the command line.
type usageError struct{ err error }

func (e usageError) Error() string { return e.err.Error() }
func (e usageError) Unwrap() error { return e.err }

// runtimeError is a failure after the command line was accepted.
type runtimeError struct{ err error }

func (e runtimeError) Error() string { return e.err.Error() }
func (e runtimeError) Unwrap() error { return e.err }

// problemError is a problem answer from the daemon.
type problemError struct {
	problem domain.Problem
	body    []byte
	raw     bool
}

func (e problemError) Error() string { return e.problem.Error() }

func (e problemError) code() int {
	if e.problem.Status >= 500 {
		return ExitServer
	}
	return ExitClient
}

// Run executes agentctl with args and returns the process exit code. Errors
// are written to io.Err in plain words; nothing else is printed on failure.
func Run(ctx context.Context, cat *app.Catalog, version string, args []string, io IO, opts ...Option) int {
	root := NewRoot(cat, version, io, opts...)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	var p problemError
	var rt runtimeError
	switch {
	case errors.As(err, &p):
		if p.raw {
			report(io, "%s\n", problemJSON(p))
		} else {
			report(io, "error: %s\n", p.Error())
		}
		return p.code()
	case errors.As(err, &rt):
		report(io, "error: %s\n", err)
		return ExitFailure
	}
	report(io, "error: %s\nTry --help.\n", err)
	return ExitUsage
}

// report writes a failure message to stderr. The exit code is what carries
// the failure, so a write that fails has nowhere better to be reported.
func report(io IO, format string, args ...any) {
	_, _ = fmt.Fprintf(io.Err, format, args...) //nolint:errcheck // stderr is the last place to report; the exit code still tells the failure
}

func problemJSON(p problemError) []byte {
	if len(bytes.TrimSpace(p.body)) > 0 && json.Valid(p.body) {
		return bytes.TrimSpace(p.body)
	}
	b, _ := json.Marshal(p.problem) //nolint:errcheck // a Problem holds only strings and an int, so marshalling cannot fail
	return b
}

// globals are the flags every command shares.
type globals struct {
	url       string
	token     string
	tokenFile string
	json      bool
}

// NewRoot builds the whole command tree: one command per API operation,
// grouped by tag, plus version and mcp. Nothing is listed by hand: the
// commands come from the catalog. Only the help is written: the common
// tasks (see tasks.go).
func NewRoot(cat *app.Catalog, version string, io IO, opts ...Option) *cobra.Command {
	g := &globals{}
	o := options{}
	for _, opt := range opts {
		opt(&o)
	}
	root := &cobra.Command{
		Use:           "agentctl",
		Short:         "Talk to the Agents One daemon",
		Long:          rootHelp(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetIn(io.In)
	root.SetOut(io.Out)
	root.SetErr(io.Err)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })
	pf := root.PersistentFlags()
	pf.StringVar(&g.url, "url", DefaultURL, "daemon address: a socket path, unix:///path, or host:port")
	pf.StringVar(&g.token, "token", "", "bearer token, for a TCP address")
	pf.StringVar(&g.tokenFile, "token-file", "", "read the bearer token from this file")
	pf.BoolVar(&g.json, "json", false, "print the raw JSON response")

	root.AddCommand(versionCommand(version))
	groups := map[string]*cobra.Command{}
	for _, c := range cat.Commands() {
		grp, ok := groups[c.Group]
		if !ok {
			grp = groupCommand(c.Group, o.tags[c.Group])
			groups[c.Group] = grp
			root.AddCommand(grp)
		}
		grp.AddCommand(newCommand(c, g, io))
	}
	root.AddCommand(mcpCommand(g, io))
	return root
}

// groupCommand is a tag: it only lists its commands, and refuses a name it does not have.
// about is the tag's description in the contract, when there is one.
func groupCommand(tag, about string) *cobra.Command {
	short := "Commands for " + tag
	if about = strings.TrimSuffix(strings.TrimSpace(about), "."); about != "" {
		short = about
	}
	return &cobra.Command{
		Use:   tag,
		Short: short,
		Long:  groupHelp(tag, short),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usageError{fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())}
			}
			return cmd.Help()
		},
	}
}

func versionCommand(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the agentctl version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			cmd.Println(version)
		},
	}
}

func newCommand(c domain.Command, g *globals, io IO) *cobra.Command {
	use := c.Name
	for _, a := range c.Args {
		use += " <" + a.Name + ">"
	}
	cmd := &cobra.Command{
		Use:     use,
		Short:   c.Operation.Summary,
		Long:    longHelp(c),
		Args:    positional(c),
		Aliases: aliases[c.Operation.ID],
	}
	fs := cmd.Flags()
	for _, f := range c.Flags {
		addFlag(fs, f)
	}
	if c.HasBody {
		fs.String(bodyJSONFlag, "", "the request body as JSON: a literal, @file, or - for stdin")
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return execute(cmd.Context(), cmd, c, args, g, io)
	}
	return cmd
}

func longHelp(c domain.Command) string {
	text := c.Operation.Summary
	if c.Operation.Description != "" {
		text += "\n\n" + strings.TrimSpace(c.Operation.Description)
	}
	return text + "\n\nAPI: " + c.Operation.Method + " " + c.Operation.Path
}

func positional(c domain.Command) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) < len(c.Args) {
			return usageError{fmt.Errorf("missing argument <%s>", c.Args[len(args)].Name)}
		}
		if len(args) > len(c.Args) {
			return usageError{fmt.Errorf("unexpected argument %q", args[len(c.Args)])}
		}
		return nil
	}
}

func addFlag(fs *pflag.FlagSet, f domain.Flag) {
	// pflag takes a back-quoted word as the value name, so quotes are plain here.
	parts := []string{strings.Join(strings.Fields(strings.ReplaceAll(f.Description, "`", "'")), " ")}
	if len(f.Enum) > 0 {
		parts = append(parts, "(one of: "+strings.Join(f.Enum, ", ")+")")
	}
	if f.Kind == domain.FlagJSON {
		parts = append(parts, "(JSON)")
	}
	if f.Kind == domain.FlagList {
		parts = append(parts, "(repeat for more)")
	}
	if f.Required {
		parts = append(parts, "(required)")
	}
	usage := strings.Join(slices.DeleteFunc(parts, func(s string) bool { return s == "" }), " ")
	switch f.Kind {
	case domain.FlagInt:
		fs.Int64(f.Name, 0, usage)
	case domain.FlagNumber:
		fs.Float64(f.Name, 0, usage)
	case domain.FlagBool:
		fs.Bool(f.Name, false, usage)
	case domain.FlagList:
		fs.StringArray(f.Name, nil, usage)
	case domain.FlagJSON:
		fs.String(f.Name, "", usage)
	default:
		fs.String(f.Name, "", usage)
	}
}

func execute(ctx context.Context, cmd *cobra.Command, c domain.Command, pos []string, g *globals, io IO) error {
	args, err := collectArgs(cmd, c, pos, io.In)
	if err != nil {
		return usageError{err}
	}
	req, err := domain.BuildRequest(c.Operation, args)
	if err != nil {
		return usageError{err}
	}
	token, err := ReadToken(g.token, g.tokenFile)
	if err != nil {
		return usageError{err}
	}
	ep, err := ParseEndpoint(g.url)
	if err != nil {
		return usageError{err}
	}
	client := NewClient(ep, token)

	if c.Operation.Stream {
		err := client.Stream(ctx, req, func(data []byte) error {
			_, werr := fmt.Fprintf(io.Out, "%s\n", data)
			return werr
		})
		return classify(err, g)
	}
	status, body, err := client.Do(ctx, req)
	if err != nil {
		return runtimeError{err}
	}
	if status >= 400 {
		return problemError{problem: domain.ParseProblem(status, body), body: body, raw: g.json}
	}
	if err := Render(io.Out, body, g.json); err != nil {
		return runtimeError{err}
	}
	return nil
}

func classify(err error, g *globals) error {
	if err == nil {
		return nil
	}
	var p *domain.Problem
	if errors.As(err, &p) {
		return problemError{problem: *p, raw: g.json}
	}
	return runtimeError{err}
}

// collectArgs reads positional arguments and flags into the operation's input map.
func collectArgs(cmd *cobra.Command, c domain.Command, pos []string, stdin io.Reader) (map[string]any, error) {
	args := map[string]any{}
	for i, p := range c.Args {
		args[p.Name] = pos[i]
	}
	fs := cmd.Flags()

	wholeBody := false
	if c.HasBody {
		raw, err := fs.GetString(bodyJSONFlag)
		if err != nil {
			return nil, fmt.Errorf("read --%s: %w", bodyJSONFlag, err)
		}
		if fs.Changed(bodyJSONFlag) {
			wholeBody = true
			if err := mergeBody(args, c, raw, stdin); err != nil {
				return nil, err
			}
		}
	}
	var missing []string
	for _, f := range c.Flags {
		if !fs.Changed(f.Name) {
			if f.Required && (f.From == domain.InQuery || !wholeBody) {
				missing = append(missing, "--"+f.Name)
			}
			continue
		}
		v, err := flagValue(fs, f)
		if err != nil {
			return nil, err
		}
		args[f.Key] = v
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required flags: %s", strings.Join(missing, ", "))
	}
	return args, nil
}

func flagValue(fs *pflag.FlagSet, f domain.Flag) (any, error) {
	switch f.Kind {
	case domain.FlagInt:
		return fs.GetInt64(f.Name)
	case domain.FlagNumber:
		return fs.GetFloat64(f.Name)
	case domain.FlagBool:
		return fs.GetBool(f.Name)
	case domain.FlagList:
		items, err := fs.GetStringArray(f.Name)
		out := make([]any, len(items))
		for i, s := range items {
			out[i] = s
		}
		return out, err
	case domain.FlagJSON:
		text, err := fs.GetString(f.Name)
		if err != nil {
			return nil, err
		}
		v, err := decodeJSON(text)
		if err != nil {
			return nil, fmt.Errorf("--%s is not valid JSON: %w", f.Name, err)
		}
		return v, nil
	}
	text, err := fs.GetString(f.Name)
	if err != nil {
		return nil, err
	}
	if len(f.Enum) > 0 && !slices.Contains(f.Enum, text) {
		return nil, fmt.Errorf("--%s must be one of: %s", f.Name, strings.Join(f.Enum, ", "))
	}
	return text, nil
}

// mergeBody puts the --body-json document into args. For an object body its
// keys become inputs, and flags set later replace them.
func mergeBody(args map[string]any, c domain.Command, raw string, stdin io.Reader) error {
	text, err := readBodySource(raw, stdin)
	if err != nil {
		return err
	}
	v, err := decodeJSON(text)
	if err != nil {
		return fmt.Errorf("--%s is not valid JSON: %w", bodyJSONFlag, err)
	}
	if !c.BodyFlat {
		args[domain.BodyInput] = v
		return nil
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("--%s must be a JSON object", bodyJSONFlag)
	}
	for k, val := range obj {
		args[k] = val
	}
	return nil
}

func readBodySource(raw string, stdin io.Reader) (string, error) {
	switch {
	case raw == "-":
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read --%s from stdin: %w", bodyJSONFlag, err)
		}
		return string(b), nil
	case strings.HasPrefix(raw, "@"):
		b, err := os.ReadFile(strings.TrimPrefix(raw, "@"))
		if err != nil {
			return "", fmt.Errorf("read --%s file: %w", bodyJSONFlag, err)
		}
		return string(b), nil
	}
	return raw, nil
}

// decodeJSON parses one JSON value and keeps numbers as written.
func decodeJSON(text string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("more than one JSON value")
	}
	return v, nil
}
