// Package config loads and validates Koffr's configuration.
//
// The file is the single source of truth (PD-005, DEC-005). That draws a line
// the CLI has to respect: the file says what *exists* -- sources, destinations,
// recipients -- and a command-line flag says what to *do* now and how to report
// it. A flag never redefines a source, because a backup nothing in the
// configuration describes is a backup the UI cannot show and the next run will
// not repeat.
//
// Validation is total and reports every problem at once. Correcting a
// configuration one message at a time is the difference between a tool people
// keep and one they fight, and the problems are structured rather than
// formatted so that the CLI can render them and `--output json` can emit them.
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Gu1llaum-3/koffr/internal/logging"
	"github.com/Gu1llaum-3/koffr/internal/notify"
	"github.com/Gu1llaum-3/koffr/internal/retention"
	"github.com/Gu1llaum-3/koffr/internal/scheduler"
)

// Version is the configuration layout this build understands.
const Version = 1

// Config is a whole configuration, after validation.
type Config struct {
	Version      int                    `yaml:"version"`
	Crypto       Crypto                 `yaml:"crypto"`
	Catalog      Catalog                `yaml:"catalog"`
	Scheduler    Scheduler              `yaml:"scheduler,omitempty"`
	Notify       Notify                 `yaml:"notify,omitempty"`
	HTTP         HTTP                   `yaml:"http,omitempty"`
	Log          Log                    `yaml:"log,omitempty"`
	Destinations map[string]Destination `yaml:"destinations"`
	Sources      map[string]Source      `yaml:"sources"`

	// path is the file this came from, for error messages. An operator editing
	// the wrong file is a long afternoon.
	path string
}

// Crypto holds the encryption settings (EF-050, EF-051).
type Crypto struct {
	Recipients []string `yaml:"recipients"`
	Identity   Secret   `yaml:"identity"`
}

// Catalog is the local cache of the repository (DEC-004).
type Catalog struct {
	Path string `yaml:"path"`
}

// Scheduler is how the built-in timetable behaves (EF-090, EF-093, EF-094).
//
// Every field has a working default. A source with a schedule and nothing else
// configured runs nightly with sane retries, because the common case should not
// require reading this section.
type Scheduler struct {
	// Timezone the schedules are read in. Empty means UTC, which is the only
	// choice that does not move twice a year -- a 02:00 job in a DST zone runs
	// twice on one night and not at all on another.
	Timezone string `yaml:"timezone,omitempty"`

	// MaxConcurrent caps jobs running at once, so a nightly window does not
	// saturate the link or the destination (EF-093). Zero means one.
	MaxConcurrent int `yaml:"max_concurrent,omitempty"`

	Retry Retry `yaml:"retry,omitempty"`

	// Prune is when the scheduler applies retention policies, as a cron spec.
	// Empty means never: a purge that ran without anyone deciding it should is
	// the one automation whose mistakes cannot be undone.
	//
	// It runs at most one source at a time and skips a source whose backup is
	// in flight, like everything else the scheduler drives.
	Prune string `yaml:"prune,omitempty"`

	// CatchUp picks up a scheduled window that went by while Koffr was not
	// running. A pointer so that leaving it out means yes: a machine rebooting
	// at 2 AM otherwise loses the night with nothing to show for it, and losing
	// a night quietly is the failure the scheduler exists to prevent.
	CatchUp *bool `yaml:"catch_up,omitempty"`

	// Window is when a job may start (EF-093). A schedule says when to try;
	// this says when trying is allowed at all, which is what keeps a large
	// backup off the link during business hours.
	Window Window `yaml:"window,omitempty"`

	location *time.Location
	window   scheduler.Window
}

// Window is the daily span during which backups may start.
type Window struct {
	Start string `yaml:"start,omitempty"`
	End   string `yaml:"end,omitempty"`

	// CancelOnClose stops a backup still running when the window closes.
	//
	// Off by default, and deliberately: cancelling at 95 % leaves nothing, and
	// with no resumable upload that turns a late backup into no backup. Someone
	// whose link is the constraint wants the other answer and says so.
	CancelOnClose bool `yaml:"cancel_on_close,omitempty"`
}

// ExecutionWindow is the parsed window.
func (s Scheduler) ExecutionWindow() scheduler.Window { return s.window }

// CatchUpEnabled reports whether a missed window should be picked up.
func (s Scheduler) CatchUpEnabled() bool { return s.CatchUp == nil || *s.CatchUp }

// Retention is EF-060. Rules are a union: a backup any rule wants is kept.
type Retention struct {
	// KeepLast keeps this many of the most recent, whatever their age.
	KeepLast int `yaml:"keep_last,omitempty"`

	// KeepWithin keeps everything taken more recently than this.
	KeepWithin time.Duration `yaml:"keep_within,omitempty"`

	// Hourly to Yearly keep the newest backup of each of that many periods.
	// "daily: 7" means seven days, not seven backups.
	Hourly  int `yaml:"hourly,omitempty"`
	Daily   int `yaml:"daily,omitempty"`
	Weekly  int `yaml:"weekly,omitempty"`
	Monthly int `yaml:"monthly,omitempty"`
	Yearly  int `yaml:"yearly,omitempty"`
}

// Policy is the retention policy this source declares.
func (r Retention) Policy() retention.Policy {
	return retention.Policy{
		KeepLast: r.KeepLast, KeepWithin: r.KeepWithin,
		Hourly: r.Hourly, Daily: r.Daily, Weekly: r.Weekly,
		Monthly: r.Monthly, Yearly: r.Yearly,
	}
}

// Log is EF-136.
//
// Format is left empty on purpose in the common case: the first person to run
// Koffr is a person, and the CLI picks text or JSON from whether there is a
// terminal on the other end (EF-114). Setting it here overrides that, which is
// what a container wants.
type Log struct {
	// Level is debug, info, warn or error. Empty means info.
	Level string `yaml:"level,omitempty"`

	// Format is "text" or "json". Empty means: decide from the terminal.
	Format string `yaml:"format,omitempty"`

	// Path is a log file, in addition to the stream. Empty means none.
	Path string `yaml:"path,omitempty"`

	// MaxSizeMB is when the file rotates. Zero means 10.
	MaxSizeMB int `yaml:"max_size_mb,omitempty"`

	// MaxFiles is how many are kept, the live one included. Zero means 5.
	MaxFiles int `yaml:"max_files,omitempty"`
}

// HTTP is the health and status listener (EF-132 to EF-135).
//
// Off unless a listen address is given. It is unauthenticated by design, so the
// default when someone does turn it on is loopback: reaching it from elsewhere
// should be a decision, made once, by someone who has read EF-135.
type HTTP struct {
	// Listen is host:port, or empty for off. A bare ":9633" listens on every
	// interface, which is refused unless allow_public says otherwise.
	Listen string `yaml:"listen,omitempty"`

	// AllowPublic opts into listening beyond loopback. The endpoints expose no
	// secret, but they do say which sources exist and when each was last backed
	// up -- which is a map of what is worth attacking and when nobody is
	// looking.
	AllowPublic bool `yaml:"allow_public,omitempty"`
}

// Notify is EF-130 and EF-131.
//
// Everything here is optional. A Koffr with no notifications configured is a
// Koffr nobody hears from, which is a legitimate choice for a laptop and a
// terrible one for a server -- so `koffr check` says which it is rather than
// this refusing to load.
type Notify struct {
	Webhooks []Webhook `yaml:"webhooks,omitempty"`
	Email    *Email    `yaml:"email,omitempty"`

	// DeadMansSwitch maps a source id to the monitor watching it (EF-131).
	// Per source, because that is how Healthchecks.io and Uptime Kuma work:
	// one check, one URL, one schedule to compare against.
	DeadMansSwitch map[string]Secret `yaml:"dead_mans_switch,omitempty"`
}

// Webhook is one POST target.
type Webhook struct {
	URL Secret `yaml:"url"`

	// MinSeverity is info, warning or error. Empty means warning: a channel
	// that reports every nightly success is a channel people mute, and a muted
	// channel reports nothing at all.
	MinSeverity string `yaml:"min_severity,omitempty"`

	// Kinds restricts to these events. Empty means all of them.
	Kinds []string `yaml:"kinds,omitempty"`

	Headers  map[string]Secret `yaml:"headers,omitempty"`
	Template string            `yaml:"template,omitempty"`
}

// Email is the SMTP channel.
type Email struct {
	Host        string   `yaml:"host"`
	Port        int      `yaml:"port,omitempty"`
	From        string   `yaml:"from"`
	To          []string `yaml:"to"`
	Username    string   `yaml:"username,omitempty"`
	Password    Secret   `yaml:"password,omitempty"`
	MinSeverity string   `yaml:"min_severity,omitempty"`
	Kinds       []string `yaml:"kinds,omitempty"`
}

// Retry is EF-094.
type Retry struct {
	// Attempts counts the first try, so 1 means no retry. Zero means 3.
	Attempts     int           `yaml:"attempts,omitempty"`
	InitialDelay time.Duration `yaml:"initial_delay,omitempty"`
	MaxDelay     time.Duration `yaml:"max_delay,omitempty"`
}

// Location is the timezone schedules are read in.
func (s Scheduler) Location() *time.Location {
	if s.location == nil {
		return time.UTC
	}
	return s.location
}

// Destination is one place backups are written.
type Destination struct {
	Type string `yaml:"type"`

	// fs
	Path string `yaml:"path,omitempty"`

	// s3
	Bucket          string `yaml:"bucket,omitempty"`
	Prefix          string `yaml:"prefix,omitempty"`
	Region          string `yaml:"region,omitempty"`
	Endpoint        string `yaml:"endpoint,omitempty"`
	AccessKeyID     Secret `yaml:"access_key_id,omitempty"`
	SecretAccessKey Secret `yaml:"secret_access_key,omitempty"`
}

// Source is one database to back up.
type Source struct {
	Engine   string `yaml:"engine"`
	Host     string `yaml:"host"`
	Port     int    `yaml:"port,omitempty"`
	User     string `yaml:"user"`
	Password Secret `yaml:"password"`
	Database string `yaml:"database"`

	// Schedule is cron, or a shortcut like @daily. Empty means this source is
	// only ever backed up by hand or by the operator's own cron (EF-091).
	Schedule string `yaml:"schedule,omitempty"`

	SSLMode     string `yaml:"sslmode,omitempty"`
	SSLRootCert string `yaml:"sslrootcert,omitempty"`
	BinDir      string `yaml:"bin_dir,omitempty"`

	// Retention is what may be deleted. Absent means nothing is: a source with
	// no policy keeps every backup for ever, which is the only safe default
	// for a setting whose mistakes are unrecoverable (EF-105).
	Retention Retention `yaml:"retention,omitempty"`

	Destinations []string `yaml:"destinations"`
	SSH          *SSH     `yaml:"ssh,omitempty"`
}

// SSH reaches a database that publishes no port (EF-002).
type SSH struct {
	Address            string `yaml:"address"`
	User               string `yaml:"user"`
	Password           Secret `yaml:"password,omitempty"`
	PrivateKey         Secret `yaml:"private_key,omitempty"`
	PrivateKeyPassword Secret `yaml:"private_key_password,omitempty"`
	KnownHostsFile     string `yaml:"known_hosts_file,omitempty"`

	// AllowExec opts into running commands on the host, which only MariaDB
	// physical backup needs (CT-002).
	AllowExec bool `yaml:"allow_exec,omitempty"`

	InsecureIgnoreHostKey bool `yaml:"insecure_ignore_host_key,omitempty"`
}

// Path is the file this configuration came from.
func (c Config) Path() string { return c.path }

// Source looks a source up by id, which is what every command does first.
func (c Config) Source(id string) (Source, bool) {
	s, ok := c.Sources[id]
	return s, ok
}

// SourceIDs are sorted, so listings and errors are stable.
func (c Config) SourceIDs() []string {
	return slices.Sorted(keys(c.Sources))
}

// Problem is one thing wrong with a configuration.
//
// Structured rather than formatted: the CLI renders it, `--output json` emits
// it, and neither has to parse a sentence.
type Problem struct {
	// Path locates the setting, in the shape a reader can find in the file:
	// "sources.prod-pg-main.host".
	Path    string `json:"path"`
	Message string `json:"message"`
	// Hint says what to write instead. A problem without one leaves the
	// operator to guess, which for a security setting is where bad defaults
	// come from.
	Hint string `json:"hint,omitempty"`
}

// ValidationError carries every problem found, not the first.
type ValidationError struct {
	File     string    `json:"file"`
	Problems []Problem `json:"problems"`
}

func (e *ValidationError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s has %d problem(s):", e.File, len(e.Problems))
	for _, p := range e.Problems {
		fmt.Fprintf(&b, "\n  %s: %s", p.Path, p.Message)
		if p.Hint != "" {
			fmt.Fprintf(&b, "\n    %s", p.Hint)
		}
	}
	return b.String()
}

// Load reads, resolves and validates a configuration.
func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the path is the operator's own
	if err != nil {
		return Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}

	var cfg Config
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	// A typo in a key is a setting that silently does not apply, which for a
	// backup tool means a retention policy nobody is enforcing.
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("config: parse %s: %w", path, err)
	}
	cfg.path = path

	if cfg.Version != Version {
		return Config{}, fmt.Errorf(
			"config: %s declares version %d; this build understands version %d",
			path, cfg.Version, Version)
	}

	cfg.applyDefaults()

	v := &validator{file: path}
	cfg.validate(v)
	if len(v.problems) > 0 {
		return Config{}, &ValidationError{File: path, Problems: v.problems}
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	for id, s := range c.Sources {
		if s.Port == 0 {
			s.Port = defaultPort(s.Engine)
		}
		if s.SSLMode == "" {
			// libpq's own default is "prefer", which silently accepts
			// plaintext. PD-004: a weaker setting is asked for, not inherited.
			s.SSLMode = "verify-full"
		}
		c.Sources[id] = s
	}
}

func defaultPort(engine string) int {
	if engine == "mariadb" {
		return 3306
	}
	return 5432
}

// validator collects problems instead of returning at the first one.
type validator struct {
	file     string
	problems []Problem
}

func (v *validator) add(path, message, hint string) {
	v.problems = append(v.problems, Problem{Path: path, Message: message, Hint: hint})
}

func (c *Config) validate(v *validator) {
	c.Crypto.validate(v)

	if c.Catalog.Path == "" {
		v.add("catalog.path", "no catalog path",
			"point it at a file on local or block storage, never on NFS: "+
				"SQLite's locking is unreliable there and the catalog will be corrupted")
	} else {
		checkCatalogFilesystem(v, c.Catalog.Path)
	}

	c.Scheduler.validate(v)
	c.Notify.validate(v, c)
	c.HTTP.validate(v)
	c.Log.validate(v)

	if len(c.Destinations) == 0 {
		v.add("destinations", "no destinations", "a backup needs somewhere to go")
	}
	for _, id := range slices.Sorted(keys(c.Destinations)) {
		d := c.Destinations[id]
		d.validate(v, "destinations."+id)
		c.Destinations[id] = d
	}

	if len(c.Sources) == 0 {
		v.add("sources", "no sources", "a backup needs a database to read")
	}
	for _, id := range c.SourceIDs() {
		s := c.Sources[id]
		s.validate(v, "sources."+id, c.Destinations)
		c.Sources[id] = s
	}
}

func (c *Crypto) validate(v *validator) {
	// EF-051, at the last moment an operator can still fix it.
	if len(c.Recipients) < 2 {
		v.add("crypto.recipients",
			fmt.Sprintf("%d recipient(s); backups need at least 2", len(c.Recipients)),
			"add an offline recovery recipient alongside the operational key, "+
				"or losing the Koffr host means losing every backup")
	}
	for i, r := range c.Recipients {
		if !strings.HasPrefix(r, "age1") {
			v.add(fmt.Sprintf("crypto.recipients[%d]", i),
				fmt.Sprintf("%q is not an age recipient", r),
				"recipients are public keys and start with age1")
		}
	}
	c.Identity.validate(v, "crypto.identity", true)
}

func (d *Destination) validate(v *validator, path string) {
	switch d.Type {
	case "fs":
		if d.Path == "" {
			v.add(path+".path", "no path", "a filesystem destination needs a directory")
		}
	case "s3":
		if d.Bucket == "" {
			v.add(path+".bucket", "no bucket", "name the bucket backups go to")
		}
		// Optional, and deliberately so: left unset, the SDK finds instance
		// credentials, which is what running in EKS or on EC2 wants. Set, they
		// are secrets like any other and have to be resolved here -- nothing
		// else does it, and an unresolved key reaches the SDK as an empty
		// string that fails at the first upload.
		d.AccessKeyID.validate(v, path+".access_key_id", false)
		d.SecretAccessKey.validate(v, path+".secret_access_key", false)
		if d.AccessKeyID.IsZero() != d.SecretAccessKey.IsZero() {
			v.add(path+".secret_access_key",
				"an access key without its secret, or the other way round",
				"set both, or neither and let the SDK find instance credentials")
		}
	case "":
		v.add(path+".type", "no type", `one of "fs" or "s3"`)
	default:
		v.add(path+".type", fmt.Sprintf("%q is not a destination type", d.Type),
			`one of "fs" or "s3"`)
	}
}

// engines are what M1 supports. MariaDB arrives in M3; naming it here would
// accept a configuration that cannot run.
var engines = []string{"postgresql"}

func (s *Source) validate(v *validator, path string, destinations map[string]Destination) {
	if !slices.Contains(engines, s.Engine) {
		v.add(path+".engine", fmt.Sprintf("%q is not a supported engine", s.Engine),
			"supported: "+strings.Join(engines, ", "))
	}
	if s.Host == "" {
		v.add(path+".host", "no host", "the address the database answers on")
	}
	if s.User == "" {
		v.add(path+".user", "no user", "a read-only role is enough for a logical backup")
	}
	if s.Database == "" {
		v.add(path+".database", "no database", "the database to back up")
	}
	s.Password.validate(v, path+".password", true)

	if len(s.Destinations) == 0 {
		v.add(path+".destinations", "no destination", "name one from the destinations block")
	}
	for _, d := range s.Destinations {
		if _, ok := destinations[d]; !ok {
			v.add(path+".destinations",
				fmt.Sprintf("%q is not a destination", d),
				"defined destinations: "+strings.Join(slices.Sorted(keys(destinations)), ", "))
		}
	}

	if s.SSH != nil {
		s.SSH.validate(v, path+".ssh")
	}
	// A policy with a negative count is a typo, and guessing what it meant is
	// how a purge does something nobody asked for.
	if err := s.Retention.Policy().Validate(); err != nil {
		v.add(path+".retention", err.Error(), "counts are whole numbers, and none of them negative")
	}

	// A schedule that does not parse is a source that silently never runs.
	// Finding out at load time is the whole of PD-006 (EF-090).
	if s.Schedule != "" {
		if err := scheduler.ValidateSpec(s.Schedule); err != nil {
			v.add(path+".schedule", fmt.Sprintf("%q is not a schedule: %v", s.Schedule, err),
				"cron, or a shortcut: @hourly, @daily, @weekly, or @every 6h")
		}
	}
}

func (s *SSH) validate(v *validator, path string) {
	if s.Address == "" {
		v.add(path+".address", "no address", "host:port of the SSH server")
	}
	if s.User == "" {
		v.add(path+".user", "no user", "the account to connect as")
	}
	if s.PrivateKey.raw == "" && s.Password.raw == "" {
		v.add(path, "no private key and no password", "set private_key or password")
	}
	s.PrivateKey.validate(v, path+".private_key", false)
	s.Password.validate(v, path+".password", false)

	// EF-004: verification is on unless it is turned off explicitly, and doing
	// so is a decision, not a fallback.
	if s.InsecureIgnoreHostKey && s.KnownHostsFile != "" {
		v.add(path+".insecure_ignore_host_key",
			"host key verification is disabled while a known_hosts file is configured",
			"remove one: the file is ignored while this is set")
	}
}

// Redacted renders the configuration with secrets left as the references they
// are written as, so the output is safe to paste into a ticket.
func (c Config) Redacted() (string, error) {
	out, err := yaml.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("config: render: %w", err)
	}
	return string(out), nil
}

// ResolvePath finds the configuration file, in a fixed order.
//
// Explicit beats environment beats working directory beats the system-wide
// file. The order matters less than it being the same every time and the
// result being reported: an operator editing a file the tool never reads is a
// long afternoon.
func ResolvePath(flag, workdir string) (string, error) {
	candidates := []string{}
	if flag != "" {
		// An explicit path that does not exist is an error, not a reason to
		// fall through to something else and back up the wrong thing.
		if _, err := os.Stat(flag); err != nil {
			return "", fmt.Errorf("config: %s: %w", flag, err)
		}
		return flag, nil
	}
	if fromEnv := os.Getenv("KOFFR_CONFIG"); fromEnv != "" {
		candidates = append(candidates, fromEnv)
	}
	candidates = append(candidates, filepath.Join(workdir, "koffr.yml"))
	if home, err := os.UserConfigDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, "koffr", "koffr.yml"))
	}
	candidates = append(candidates, "/etc/koffr/koffr.yml")

	for _, c := range candidates {
		// gosec sees a path derived from the environment. That is what this
		// function is for: choosing which configuration file to read is the
		// operator's decision, and anyone able to set KOFFR_CONFIG could pass
		// --config instead, or has the process already.
		if _, err := os.Stat(c); err == nil { //nolint:gosec // the operator chooses their own configuration path
			return c, nil
		}
	}
	return "", fmt.Errorf("config: no configuration found; looked in %s",
		strings.Join(candidates, ", "))
}

func keys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// validate resolves the timezone and applies the defaults.
//
// The timezone is resolved here rather than when the scheduler starts, because
// a name the machine cannot find is a configuration mistake and PD-006 says
// those are found at load time -- not at 2 AM, by a job that did not run.
func (s *Scheduler) validate(v *validator) {
	if s.Timezone == "" {
		s.location = time.UTC
	} else {
		loc, err := time.LoadLocation(s.Timezone)
		if err != nil {
			v.add("scheduler.timezone",
				fmt.Sprintf("%q is not a timezone this machine knows: %v", s.Timezone, err),
				"use an IANA name such as Europe/Paris, or leave it out for UTC")
		} else {
			s.location = loc
		}
	}

	if s.MaxConcurrent < 0 {
		v.add("scheduler.max_concurrent", "cannot be negative", "leave it out for one at a time")
	}
	if s.MaxConcurrent == 0 {
		s.MaxConcurrent = 1
	}

	switch {
	case s.Retry.Attempts < 0:
		v.add("scheduler.retry.attempts", "cannot be negative", "1 means no retry")
	case s.Retry.Attempts == 0:
		s.Retry.Attempts = 3
	}
	if s.Retry.InitialDelay == 0 {
		s.Retry.InitialDelay = time.Minute
	}
	if s.Retry.MaxDelay == 0 {
		s.Retry.MaxDelay = 30 * time.Minute
	}
	if s.Prune != "" {
		if err := scheduler.ValidateSpec(s.Prune); err != nil {
			v.add("scheduler.prune", fmt.Sprintf("%q is not a schedule: %v", s.Prune, err),
				"cron, or a shortcut: @daily is the usual answer")
		}
	}

	w, err := scheduler.ParseWindow(s.Window.Start, s.Window.End)
	if err != nil {
		v.add("scheduler.window", err.Error(), "write the times as HH:MM, for example 22:00 to 06:00")
	} else {
		s.window = w
	}

	if s.Retry.MaxDelay < s.Retry.InitialDelay {
		v.add("scheduler.retry.max_delay", "is shorter than the initial delay",
			"the delay doubles up to this ceiling, so the ceiling has to be the larger one")
	}
}

// validate resolves the notification secrets and refuses a channel that could
// not deliver.
//
// A webhook URL is a Secret because Slack, Discord and Healthchecks.io all put
// a token in the path: a configuration file carrying one verbatim is a
// configuration file that cannot be committed, which is the whole point of
// EF-103.
func (n *Notify) validate(v *validator, cfg *Config) {
	for i := range n.Webhooks {
		n.Webhooks[i].validate(v, fmt.Sprintf("notify.webhooks[%d]", i))
	}
	if n.Email != nil {
		n.Email.validate(v, "notify.email")
	}
	for source, url := range n.DeadMansSwitch {
		path := "notify.dead_mans_switch." + source
		url.validate(v, path, true)
		n.DeadMansSwitch[source] = url

		// A monitor watching a source that does not exist will alarm tonight,
		// for a reason nobody will be able to find: the check is armed and
		// nothing will ever ping it.
		if _, known := cfg.Sources[source]; !known {
			v.add(path, fmt.Sprintf("there is no source called %q", source),
				"the key is a source id; check it against the sources section")
			continue
		}
		if url.Value() == "" {
			continue
		}
		if _, err := notify.NewDeadMansSwitch(notify.DeadMansSwitchConfig{
			URLs: map[string]string{source: url.Value()},
		}); err != nil {
			v.add(path, err.Error(), "an https URL from Healthchecks.io or Uptime Kuma")
		}
	}
}

func (w *Webhook) validate(v *validator, path string) {
	w.URL.validate(v, path+".url", true)
	for name, value := range w.Headers {
		value.validate(v, path+".headers."+name, true)
		w.Headers[name] = value
	}
	w.MinSeverity = validSeverity(v, path+".min_severity", w.MinSeverity)

	// Built here, not when the scheduler starts. Alerting that is broken is
	// worse than alerting that is absent -- absent is obvious, broken looks
	// like quiet -- so the URL and the template are checked while someone is
	// still looking at the file (PD-006).
	if w.URL.Value() == "" {
		return
	}
	if _, err := notify.NewWebhook(notify.WebhookConfig{
		URL: w.URL.Value(), Template: w.Template,
	}); err != nil {
		v.add(path, err.Error(), "an http or https URL, and a template that parses")
	}
}

func (e *Email) validate(v *validator, path string) {
	if e.Host == "" {
		v.add(path+".host", "no host", "name the SMTP server")
	}
	if e.From == "" {
		v.add(path+".from", "no from address", "mail needs a sender")
	}
	if len(e.To) == 0 {
		v.add(path+".to", "no recipients", "name at least one address to alert")
	}
	if !e.Password.IsZero() {
		e.Password.validate(v, path+".password", true)
	}
	if e.Username != "" && e.Password.IsZero() {
		v.add(path+".password", "a username with no password",
			"this is the shape a missing environment variable leaves behind")
		return
	}
	if _, err := notify.NewEmail(notify.EmailConfig{
		Host: e.Host, Port: e.Port, From: e.From, To: e.To,
		Username: e.Username, Password: e.Password.Value(),
	}); err != nil {
		v.add(path, err.Error(), "check the addresses and the server")
	}
	e.MinSeverity = validSeverity(v, path+".min_severity", e.MinSeverity)
}

// validSeverity defaults to warning.
//
// Not info: a channel that reports every nightly success is a channel people
// mute, and a muted channel reports nothing at all -- including the one night
// it mattered.
func validSeverity(v *validator, path, given string) string {
	switch given {
	case "":
		return "warning"
	case "info", "warning", "error":
		return given
	default:
		v.add(path, fmt.Sprintf("%q is not a severity", given), `one of "info", "warning" or "error"`)
		return "warning"
	}
}

// validate refuses a listener that would be reachable from outside without
// someone having said so.
func (h *HTTP) validate(v *validator) {
	if h.Listen == "" {
		return
	}
	host, port, err := net.SplitHostPort(h.Listen)
	if err != nil {
		v.add("http.listen", fmt.Sprintf("%q is not host:port: %v", h.Listen, err),
			`write it as "127.0.0.1:9633"`)
		return
	}
	if port == "" {
		v.add("http.listen", "no port", `write it as "127.0.0.1:9633"`)
		return
	}
	if h.AllowPublic {
		return
	}

	// Empty host means every interface, and so does 0.0.0.0. Both are the shape
	// of someone copying an example rather than deciding.
	if host == "" || host == "0.0.0.0" || host == "::" {
		v.add("http.listen",
			fmt.Sprintf("%q listens on every interface and the endpoints have no authentication", h.Listen),
			`use "127.0.0.1:`+port+`" and reach it through an SSH tunnel, or set http.allow_public `+
				`if a reverse proxy is doing the authenticating`)
		return
	}
	if ip := net.ParseIP(host); ip != nil && !ip.IsLoopback() {
		v.add("http.listen",
			fmt.Sprintf("%s is reachable from outside this machine and the endpoints have no authentication", host),
			"set http.allow_public if that is deliberate")
	}
}

// validate refuses a level or format that would only fail at start-up.
func (l *Log) validate(v *validator) {
	if err := logging.ValidateConfig(logging.Config{Level: l.Level, Format: l.Format}); err != nil {
		v.add("log", err.Error(), "level: debug, info, warn or error; format: text or json")
	}
	if l.MaxSizeMB < 0 {
		v.add("log.max_size_mb", "cannot be negative", "leave it out for 10")
	}
	if l.MaxFiles < 0 {
		v.add("log.max_files", "cannot be negative", "leave it out for 5")
	}
}
