// Package cli is the command-line interface, and the only interface M1 has.
//
// Two rules shape it, and both come from the configuration being the single
// source of truth (PD-005, DEC-005). The file says what *exists* -- sources,
// destinations, recipients -- and a flag says what to *do* now and how to
// report it. And every command is a script's command: the exit code is a
// documented contract, --output json is a stable document, and neither changes
// because a message got reworded.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Gu1llaum-3/koffr/internal/config"
	"github.com/Gu1llaum-3/koffr/internal/logging"
	"github.com/Gu1llaum-3/koffr/internal/version"
)

// Exit codes (EF-113). They are a public interface: an operator's cron job
// branches on them, so each means one thing and none is ever reused.
const (
	// ExitOK means the command did what was asked.
	ExitOK = 0
	// ExitFailure is a failure with no more specific code: an unreachable
	// repository, a broken catalog, an interrupted run.
	ExitFailure = 1
	// ExitUsage means the command line was wrong. Nothing was attempted.
	ExitUsage = 2
	// ExitConfig means the configuration is invalid. Nothing was attempted,
	// and the fix is in a file rather than in the database.
	ExitConfig = 3
	// ExitBackup means a backup was attempted and did not complete. This is
	// the code that pages someone.
	ExitBackup = 4
	// ExitVerify means a backup exists but did not verify: it is present and
	// not trustworthy, which is worse than missing.
	ExitVerify = 5
	// ExitRestore means a restore was attempted and did not complete.
	ExitRestore = 6
)

// Streams are the process's streams, injected so tests see exactly what a
// shell would.
type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

func (s Streams) out() io.Writer {
	if s.Out == nil {
		return io.Discard
	}
	return s.Out
}

func (s Streams) err() io.Writer {
	if s.Err == nil {
		return io.Discard
	}
	return s.Err
}

// Fault is an error that knows which exit code it deserves.
//
// Classification happens where the failure is understood, not in a switch at
// the top that has to guess from a message. An unwrapped error reaching the top
// is ExitFailure, which is the honest answer for something nobody classified.
type Fault struct {
	Code int
	Err  error
	// Problems is set for configuration faults, so --output json can hand a
	// script the whole list rather than a paragraph.
	Problems []config.Problem
}

func (f *Fault) Error() string { return f.Err.Error() }
func (f *Fault) Unwrap() error { return f.Err }

func fault(code int, format string, args ...any) *Fault {
	return &Fault{Code: code, Err: fmt.Errorf(format, args...)}
}

// codeName is the token that appears in JSON output and in the root help. It is
// part of the contract, so it is defined once.
func codeName(code int) string {
	switch code {
	case ExitOK:
		return "ok"
	case ExitUsage:
		return "usage"
	case ExitConfig:
		return "config"
	case ExitBackup:
		return "backup"
	case ExitVerify:
		return "verify"
	case ExitRestore:
		return "restore"
	default:
		return "failure"
	}
}

// Run executes argv and returns the process exit code.
func Run(ctx context.Context, args []string, s Streams) int {
	a := &app{streams: s}
	// contextcheck cannot see through cobra: newRoot takes no context because a
	// command tree is built before there is one, and every RunE gets its own
	// from cmd.Context(), which ExecuteContext below supplies.
	//nolint:contextcheck // the context arrives through ExecuteContext
	root := a.newRoot()
	root.SetArgs(args)

	err := root.ExecuteContext(ctx)
	if err == nil {
		a.emitSuccess()
		return ExitOK
	}

	// Anything that fails before a command body starts is cobra rejecting the
	// command line: an unknown command, a bad flag, the wrong number of
	// arguments. That is a usage error whatever the message says.
	code := ExitUsage
	var f *Fault
	if errors.As(err, &f) {
		code = f.Code
	} else if a.started {
		code = ExitFailure
	}
	a.emitError(code, err)
	return code
}

// app is one invocation.
type app struct {
	streams Streams

	// configPath and format are the two persistent flags.
	configPath string
	format     string

	// started records that a command body began, which is what separates a
	// usage error from a failure.
	started bool

	// log is the structured logger. Nil until a command that needs one builds
	// it: `koffr version` has nothing to log and should not open a file to say
	// so.
	log      *slog.Logger
	closeLog func() error

	// command, result and renderText are filled in by whichever command ran.
	command    string
	result     any
	renderText func(p *printer)
}

const (
	formatText = "text"
	formatJSON = "json"
)

func (a *app) newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "koffr",
		Short: "Back up and restore PostgreSQL and MariaDB",
		Long: "Koffr streams a database into an encrypted repository and brings it back.\n\n" +
			"Nothing is written to the database host and nothing is staged on disk: the\n" +
			"dump is compressed, encrypted and uploaded as it is produced.",
		// Cobra's default suggests a command on a typo, which is exactly what
		// someone who typed one needs.
		SuggestionsMinimumDistance: 2,
		// --version as well as `version`, because both are what people type.
		Version:       version.Value,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(a.streams.out())
	root.SetErr(a.streams.err())

	root.PersistentFlags().StringVar(&a.configPath, "config", "",
		"configuration file (default: $KOFFR_CONFIG, ./koffr.yml, ~/.config/koffr/koffr.yml, /etc/koffr/koffr.yml)")
	root.PersistentFlags().StringVar(&a.format, "output", formatText,
		"output format: text or json")

	root.SetHelpTemplate(helpTemplate)
	root.AddCommand(
		a.versionCmd(),
		a.configCmd(),
		a.catalogCmd(),
		a.scheduleCmd(),
		a.checkCmd(),
		a.backupCmd(),
		a.lsCmd(),
		a.showCmd(),
		a.fetchCmd(),
		a.restoreCmd(),
	)
	return root
}

// helpTemplate appends the exit codes to the root help. They are a contract,
// and a contract nobody can find is a contract nobody relies on.
const helpTemplate = `{{with .Long}}{{.}}

{{end}}{{if .Runnable}}Usage:
  {{.UseLine}}

{{end}}{{if .HasAvailableSubCommands}}Usage:
  {{.CommandPath}} [command]

Commands:{{range .Commands}}{{if .IsAvailableCommand}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}

{{end}}{{if .HasAvailableLocalFlags}}Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}

{{end}}{{if .HasAvailableInheritedFlags}}Global flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}

{{end}}{{if not .HasParent}}Exit codes:
  0  ok       the command did what was asked
  1  failure  something went wrong with no more specific code
  2  usage    the command line was wrong; nothing was attempted
  3  config   the configuration is invalid; nothing was attempted
  4  backup   a backup was attempted and did not complete
  5  verify   a backup exists but did not verify
  6  restore  a restore was attempted and did not complete

{{end}}{{if .HasAvailableSubCommands}}Run "{{.CommandPath}} [command] --help" for more about a command.
{{end}}`

// begin marks a command body as started and records its name for the JSON
// envelope. Every RunE calls it first.
func (a *app) begin(cmd *cobra.Command) error {
	a.started = true
	a.command = strings.TrimPrefix(cmd.CommandPath(), "koffr ")
	if a.format != formatText && a.format != formatJSON {
		a.started = false // the command line was wrong, not the command
		return fault(ExitUsage, "--output must be %q or %q, not %q", formatText, formatJSON, a.format)
	}
	return nil
}

// emit records what a command produced. Text is rendered by the command's own
// closure, JSON by the envelope; both come from the same call so neither can be
// forgotten.
func (a *app) emit(result any, renderText func(p *printer)) {
	a.result, a.renderText = result, renderText
}

// setupLogging builds the logger for a long-running command (EF-136).
//
// Called by the scheduler and by nothing else for now. A command run by hand
// reports through printf and warnf, which is prose for a person; a daemon needs
// lines a machine can filter, and a file that does not grow without end.
func (a *app) setupLogging(cfg config.Config) error {
	format := cfg.Log.Format
	if format == "" {
		// EF-114: prose on a terminal, structured otherwise. A container and a
		// systemd unit both get JSON without anyone having configured it, and
		// someone watching the same daemon in a shell gets lines they can read.
		format = "text"
		if !a.interactive() {
			format = "json"
		}
	}

	log, closeLog, err := logging.New(logging.Config{
		Format:       format,
		Level:        cfg.Log.Level,
		Path:         cfg.Log.Path,
		MaxSizeBytes: int64(cfg.Log.MaxSizeMB) << 20,
		MaxFiles:     cfg.Log.MaxFiles,
		Writer:       a.streams.err(),
	})
	if err != nil {
		return &Fault{Code: ExitConfig, Err: err}
	}
	a.log, a.closeLog = log, closeLog
	return nil
}

// logf records a structured line when there is a logger, and prose otherwise.
//
// Both, never neither: a message that exists only in one of the two modes is a
// message somebody will look for in the other.
func (a *app) logf(ctx context.Context, level slog.Level, msg string, args ...any) {
	if a.log != nil {
		a.log.Log(ctx, level, msg, args...)
		return
	}
	a.warnf("%s", msg)
}

// warnf writes a diagnostic to stderr.
//
// The write's error is discarded, and that is the one place it is the right
// call: this *is* the error path, and there is nowhere left to report a stderr
// that will not take bytes.
func (a *app) warnf(format string, args ...any) {
	if a.log != nil {
		a.log.Warn(fmt.Sprintf(format, args...))
		return
	}
	_, _ = fmt.Fprintf(a.streams.err(), format+"\n", args...)
}

// printf writes progress and prose. It goes to stderr, always: stdout belongs to
// the answer, and in JSON mode it belongs to exactly one document.
func (a *app) printf(format string, args ...any) {
	if a.format == formatJSON {
		return
	}
	// Through the logger once there is one, so a daemon's stderr is entirely
	// structured. Mixing a prose line into a stream of JSON defeats the whole
	// point: a log shipper parsing the stream chokes on the sentence, and the
	// sentence is usually the one saying something interesting happened.
	if a.log != nil {
		a.log.Info(fmt.Sprintf(format, args...))
		return
	}
	a.warnf(format, args...)
}

// response is the JSON envelope. Its shape is a contract with every script that
// parses it: fields may be added, never renamed or repurposed.
type response struct {
	Koffr   string     `json:"koffr"`
	Command string     `json:"command"`
	OK      bool       `json:"ok"`
	Result  any        `json:"result,omitempty"`
	Error   *errorBody `json:"error,omitempty"`
}

type errorBody struct {
	Code     string           `json:"code"`
	Message  string           `json:"message"`
	Problems []config.Problem `json:"problems,omitempty"`
}

func (a *app) emitSuccess() {
	if a.format == formatJSON {
		a.writeJSON(response{
			Koffr: version.Value, Command: a.command, OK: true,
			Result: a.result,
		})
		return
	}
	if a.renderText != nil {
		p := newPrinter(a.streams.out())
		a.renderText(p)
		if err := p.Err(); err != nil {
			a.warnf("koffr: writing output: %v", err)
		}
	}
}

func (a *app) emitError(code int, err error) {
	var f *Fault
	_ = errors.As(err, &f)

	if a.format == formatJSON {
		body := &errorBody{Code: codeName(code), Message: err.Error()}
		if f != nil {
			body.Problems = f.Problems
		}
		// The result rides along when the command produced one. `koffr check`
		// fails precisely when it has the most to say, and the first version of
		// this threw those findings away -- the command that exists to report
		// what is wrong reported nothing as soon as something was.
		a.writeJSON(response{
			Koffr: version.Value, Command: a.command, OK: false,
			Result: a.result, Error: body,
		})
		return
	}

	if a.renderText != nil {
		p := newPrinter(a.streams.out())
		a.renderText(p)
		if renderErr := p.Err(); renderErr != nil {
			a.warnf("koffr: writing output: %v", renderErr)
		}
	}
	a.warnf("koffr: %s", err.Error())
	if f != nil {
		for _, p := range f.Problems {
			a.warnf("  %s: %s", p.Path, p.Message)
			if p.Hint != "" {
				a.warnf("      %s", p.Hint)
			}
		}
	}
}

func (a *app) writeJSON(r response) {
	enc := json.NewEncoder(a.streams.out())
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		a.warnf("koffr: writing output: %v", err)
	}
}

// loadConfig resolves and loads the configuration, turning every failure into a
// config fault carrying whatever the validator found.
func (a *app) loadConfig() (config.Config, error) {
	workdir, err := os.Getwd()
	if err != nil {
		workdir = "."
	}
	path, err := config.ResolvePath(a.configPath, workdir)
	if err != nil {
		return config.Config{}, &Fault{Code: ExitConfig, Err: err}
	}
	cfg, err := config.Load(path)
	if err != nil {
		f := &Fault{Code: ExitConfig, Err: err}
		var v *config.ValidationError
		if errors.As(err, &v) {
			f.Problems = v.Problems
		}
		return config.Config{}, f
	}
	return cfg, nil
}

// source looks a source up, and on a typo says what does exist. A list of the
// four things that would have worked is worth more than "not found".
func (a *app) source(cfg config.Config, id string) (config.Source, error) {
	src, ok := cfg.Source(id)
	if !ok {
		ids := cfg.SourceIDs()
		if len(ids) == 0 {
			return config.Source{}, fault(ExitUsage,
				"no source %q, and %s defines none", id, cfg.Path())
		}
		return config.Source{}, fault(ExitUsage,
			"no source %q in %s; defined sources are: %s", id, cfg.Path(), strings.Join(ids, ", "))
	}
	return src, nil
}

// New builds the command tree, for tests that need to walk it.
func New(s Streams) *cobra.Command { return (&app{streams: s}).newRoot() }
