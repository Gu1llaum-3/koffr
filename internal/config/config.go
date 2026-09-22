package config

import "time"

// Config is the whole of koffr.yaml. Its shape is exactly the target form of
// the specification § 5.1 — no more, because strict parsing turns any key we
// invent into an error for the operator, and no less, because E-037 requires
// that form to be accepted as written (N-7).
type Config struct {
	Agent        Agent         `yaml:"agent"`
	Encryption   Encryption    `yaml:"encryption"`
	Databases    []Database    `yaml:"databases"`
	Destinations []Destination `yaml:"destinations"`
	Alerts       Alerts        `yaml:"alerts"`
	Server       Server        `yaml:"server"`

	// Resolved at load time from Agent.Timezone; never read from YAML.
	location *time.Location
}

// Agent is what identifies this installation and how much it may do at once.
type Agent struct {
	ID string `yaml:"id"`
	// Never inherited from the system: E-036 wants it written down.
	Timezone        string `yaml:"timezone"`
	MaxParallelJobs int    `yaml:"max_parallel_jobs"`
}

// Encryption says where the age public keys live. Every archive is encrypted
// for them; nothing here is a private key.
type Encryption struct {
	RecipientsFile string `yaml:"recipients_file"`
}

// Database is one database to back up: how to reach it, how to dump it, where
// to send the archive and how long to keep it.
type Database struct {
	ID       string `yaml:"id"`
	Engine   string `yaml:"engine"`
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Database string `yaml:"database"`
	User     string `yaml:"user"`

	Password     Secret `yaml:"password,omitempty"`
	PasswordEnv  string `yaml:"password_env"`
	PasswordFile string `yaml:"password_file"`

	// RecipientsFile, when set, **replaces** encryption.recipients_file for
	// this database — never completes it (`Q-04`, ADR-0016). The field exists
	// even where nobody uses it, so that adding it later cannot break a
	// configuration written in between (A-17).
	RecipientsFile string `yaml:"recipients_file"`

	Tools               Tools     `yaml:"tools"`
	Staging             string    `yaml:"staging"`
	Schedule            string    `yaml:"schedule"`
	MinInterval         string    `yaml:"min_interval"`
	AllowRemoteSchedule bool      `yaml:"allow_remote_schedule"`
	AllowRestore        bool      `yaml:"allow_restore"`
	Destinations        []string  `yaml:"destinations"`
	Retention           Retention `yaml:"retention"`
}

// Retention is the grandfather-father-son policy of one database, in archives
// kept per bucket.
type Retention struct {
	Last    int `yaml:"last"`
	Daily   int `yaml:"daily"`
	Weekly  int `yaml:"weekly"`
	Monthly int `yaml:"monthly"`
}

// Destination is one place archives are written to. The specification gives
// three types — filesystem, s3 and sftp — and one flat set of keys, so a field
// belongs to the type that uses it.
type Destination struct {
	ID   string `yaml:"id"`
	Type string `yaml:"type"`

	// filesystem, and the remote path of sftp
	Path string `yaml:"path"`

	// s3
	Endpoint            string `yaml:"endpoint"`
	Region              string `yaml:"region"`
	Bucket              string `yaml:"bucket"`
	AccessKeyID         Secret `yaml:"access_key_id,omitempty"`
	AccessKeyIDEnv      string `yaml:"access_key_id_env"`
	AccessKeyIDFile     string `yaml:"access_key_id_file"`
	SecretAccessKey     Secret `yaml:"secret_access_key,omitempty"`
	SecretAccessKeyEnv  string `yaml:"secret_access_key_env"`
	SecretAccessKeyFile string `yaml:"secret_access_key_file"`

	// sftp
	Host           string `yaml:"host"`
	Port           int    `yaml:"port"`
	User           string `yaml:"user"`
	PrivateKeyFile string `yaml:"private_key_file"`
}

// Alerts binds events to the channels that carry them.
type Alerts struct {
	Channels []Channel `yaml:"channels"`
	Rules    []Rule    `yaml:"rules"`
}

// Channel is one way out for an alert: an e-mail through SMTP, or a webhook.
type Channel struct {
	ID   string `yaml:"id"`
	Type string `yaml:"type"`

	// email
	To   []string `yaml:"to"`
	SMTP SMTP     `yaml:"smtp"`

	// webhook
	URL string `yaml:"url"`
}

// SMTP is the mail relay an email channel sends through.
type SMTP struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	User string `yaml:"user"`

	Password     Secret `yaml:"password,omitempty"`
	PasswordEnv  string `yaml:"password_env"`
	PasswordFile string `yaml:"password_file"`
}

// Rule routes a list of events to a list of channels.
type Rule struct {
	On       []string `yaml:"on"`
	Channels []string `yaml:"channels"`
}

// Server is the link to the central server. Disabled by default: koffr backs
// up, verifies and alerts on its own (ADR-0001).
type Server struct {
	Enabled        bool   `yaml:"enabled"`
	URL            string `yaml:"url"`
	TokenFile      string `yaml:"token_file"`
	DelegateAlerts bool   `yaml:"delegate_alerts"`
	FallbackAfter  string `yaml:"fallback_after"`
}
