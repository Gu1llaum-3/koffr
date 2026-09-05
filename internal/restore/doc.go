// Package restore turns a stored backup back into a database.
//
// Its most important output is not code. Every backup ships a RESTORE.md
// generated for that backup, holding the exact commands to bring it back with
// nothing but age, zstd, tar and the database's own tools. That document is
// PD-001: if it is wrong, "restorable without Koffr" is a slogan.
package restore

import (
	"fmt"
	"io"
	"strings"
	"text/template"

	"github.com/Gu1llaum-3/koffr/internal/manifest"
)

// DocInput is everything the document needs.
type DocInput struct {
	Manifest manifest.Manifest
	// Repository is where the backup lives, written the way an operator would
	// type it: an S3 URL, a path, whatever the destination is called.
	Repository string
	// Prefix is the backup's key prefix inside the repository.
	Prefix string
}

// WriteDoc renders the restore procedure for one backup.
func WriteDoc(w io.Writer, in DocInput) error {
	m := in.Manifest
	if m.BackupID == "" || m.Engine == "" || m.Kind == "" {
		return fmt.Errorf("restore: cannot write a procedure for an incomplete manifest")
	}
	if len(m.Objects) == 0 {
		return fmt.Errorf("restore: backup %s lists no objects", m.BackupID)
	}

	tmpl, err := template.New("restore").Funcs(template.FuncMap{
		"bytes": humanBytes,
	}).Parse(docTemplate)
	if err != nil {
		return fmt.Errorf("restore: parse template: %w", err)
	}

	objects := viewObjects(m)
	proc := procedureFor(m.Engine, m.Kind, objects)
	data := docData{
		DocInput:      in,
		Objects:       viewObjects(m),
		Procedure:     proc,
		Recipients:    recipientsOf(m),
		NamesTargetDB: strings.Contains(proc.commands(), "DBNAME"),
	}
	if err := tmpl.Execute(w, data); err != nil {
		return fmt.Errorf("restore: render procedure for %s: %w", m.BackupID, err)
	}
	return nil
}

type docData struct {
	DocInput
	Objects       []objectView
	Procedure     procedure
	Recipients    []string
	NamesTargetDB bool
}

// objectView precomputes the filenames each step works with, so the template
// holds prose and the naming rules stay here where they can be read.
type objectView struct {
	manifest.Object
	// Unsealed is what age writes: the key without its .age suffix.
	Unsealed string
	// Plain is the usable file, after decompression if there was any.
	Plain string
	// Compressed says whether a zstd step is needed at all.
	Compressed bool
}

func viewObjects(m manifest.Manifest) []objectView {
	out := make([]objectView, 0, len(m.Objects))
	for _, o := range m.Objects {
		unsealed := strings.TrimSuffix(o.Key, ".age")
		out = append(out, objectView{
			Object:     o,
			Unsealed:   unsealed,
			Plain:      strings.TrimSuffix(unsealed, ".zst"),
			Compressed: o.Codec == "zstd",
		})
	}
	return out
}

// procedure is the engine-specific half of the document.
type procedure struct {
	Title string
	// Steps are rendered in order. Each carries its own explanation, because a
	// command without a reason is a command someone will run in the wrong
	// order.
	Steps []step
}

// commands joins every command in the procedure, for deciding which notes the
// document needs.
func (p procedure) commands() string {
	var b strings.Builder
	for _, s := range p.Steps {
		b.WriteString(s.Command)
		b.WriteByte('\n')
	}
	return b.String()
}

type step struct {
	Title   string
	Body    string
	Command string
}

func procedureFor(engine, kind string, objects []objectView) procedure {
	switch {
	case engine == "postgresql" && kind == "logical":
		return postgresLogical(objects)
	case engine == "postgresql":
		return postgresPhysical(objects)
	case engine == "mariadb" && kind == "logical":
		return mariadbLogical(objects)
	default:
		return mariadbPhysical(objects)
	}
}

// pipeThrough builds the command that feeds one object into a tool.
//
// It names the file step 3 actually produced: the key without .age, with the
// .zst still on. Naming the fully decompressed file would send the reader
// looking for something no step created, and the document is worthless the
// moment one command does not run as written.
func pipeThrough(o objectView, tool string) string {
	if o.Compressed {
		return "zstd -d " + o.Unsealed + " --stdout | " + tool
	}
	return tool + " < " + o.Unsealed
}

// find returns the object whose plain name ends in suffix.
func find(objects []objectView, suffix string) (objectView, bool) {
	for _, o := range objects {
		if strings.HasSuffix(o.Plain, suffix) {
			return o, true
		}
	}
	return objectView{}, false
}

// primary is the object a procedure works on when there is only one.
func primary(objects []objectView) objectView {
	if len(objects) == 0 {
		return objectView{Unsealed: "backup"}
	}
	return objects[0]
}

func postgresLogical(objects []objectView) procedure {
	var steps []step
	if globals, ok := find(objects, "globals.sql"); ok {
		steps = append(steps, step{
			Title: "Recreate the roles and tablespaces",
			Body: "Roles live in the cluster, not in a database, so a dump of one database " +
				"does not carry them. Restoring without this step produces a database whose " +
				"owners and grants do not exist.",
			Command: pipeThrough(globals, "psql --dbname=postgres"),
		})
	}
	dump, ok := find(objects, ".pgdump")
	if !ok {
		dump = primary(objects)
	}
	steps = append(steps, step{
		Title: "Restore the database",
		Body: "pg_restore reads the archive from a pipe. It stops at the archive's end " +
			"marker, so zstd is killed by SIGPIPE and exits 141 even when the restore " +
			"succeeded: check pg_restore's own status, and do not wrap this in a shell " +
			"with pipefail set.\n\n" +
			"For a parallel restore, decompress to a file first: pg_restore needs to seek, " +
			"and refuses --jobs on standard input.",
		Command: pipeThrough(dump, "pg_restore --dbname=DBNAME --no-owner"),
	})
	return procedure{Title: "Restore a PostgreSQL logical backup", Steps: steps}
}

func postgresPhysical(objects []objectView) procedure {
	return procedure{
		Title: "Restore a PostgreSQL base backup",
		Steps: []step{
			{
				Title: "Extract the data directory",
				Body: "The archive is the cluster's data directory. Extract it into an empty " +
					"directory owned by the postgres user, with mode 0700, or the server will " +
					"refuse to start.",
				Command: "mkdir -p restored && " + pipeThrough(primary(objects), "tar -x -C restored"),
			},
			{
				Title: "Replay the write-ahead log",
				Body: "The backup is consistent only from its end LSN onwards. Restoring to a " +
					"point in time means replaying WAL from the start LSN; restoring the backup " +
					"as it stands means replaying up to the end LSN and no further.",
				Command: "pg_ctl -D restored start",
			},
		},
	}
}

func mariadbLogical(objects []objectView) procedure {
	return procedure{
		Title: "Restore a MariaDB logical backup",
		Steps: []step{
			{
				Title:   "Load the dump",
				Body:    "The dump is plain SQL and is replayed by the client.",
				Command: pipeThrough(primary(objects), "mariadb --user=USER --password --database=DBNAME"),
			},
		},
	}
}

func mariadbPhysical(objects []objectView) procedure {
	return procedure{
		Title: "Restore a MariaDB physical backup",
		Steps: []step{
			{
				Title: "Unpack the stream",
				Body: "The archive is an xbstream, which mbstream extracts. This needs disk space " +
					"for the whole uncompressed backup, so check there is room before starting: " +
					"running out halfway leaves a directory that looks complete and is not.",
				Command: "mkdir -p restored && " + pipeThrough(primary(objects), "mbstream -x -C restored"),
			},
			{
				Title: "Prepare the backup",
				Body: "A physical backup is a copy taken while the server was running, so it is " +
					"not consistent until InnoDB has replayed its redo log over it. It cannot be " +
					"started before this step, and this step needs disk space of its own.",
				Command: "mariabackup --prepare --target-dir=restored",
			},
			{
				Title: "Put it in place",
				Body: "Stop the server, move the old data directory aside rather than deleting " +
					"it, and give the new one to the mysql user.",
				Command: "mariabackup --copy-back --target-dir=restored && " +
					"chown -R mysql:mysql /var/lib/mysql",
			},
		},
	}
}

func recipientsOf(m manifest.Manifest) []string {
	seen := map[string]bool{}
	var out []string
	for _, o := range m.Objects {
		for _, r := range o.Recipients {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	return out
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

const docTemplate = `# Restoring backup {{.Manifest.BackupID}}

This backup can be restored without Koffr. Everything below uses only ` + "`age`" + `,
` + "`zstd`" + ` and the tools that ship with the database.

| | |
|---|---|
| Source | ` + "`{{.Manifest.SourceID}}`" + ` |
| Engine | {{.Manifest.Engine}} {{.Manifest.ServerVersion}} |
| Kind | {{.Manifest.Kind}} |
| Taken | {{.Manifest.StartedAt.UTC.Format "2006-01-02 15:04:05 UTC"}} |
| Repository | ` + "`{{.Repository}}`" + ` |
| Prefix | ` + "`{{.Prefix}}`" + ` |
{{- with .Manifest.PostgreSQL}}
| WAL range | ` + "`{{.StartLSN}}`" + ` to ` + "`{{.EndLSN}}`" + ` on timeline {{.Timeline}} |
{{- end}}

## 1. Fetch the objects

{{range .Objects -}}
- ` + "`{{.Key}}`" + ` ({{bytes .SizeBytes}})
{{end}}
Download them from ` + "`{{$.Repository}}`" + ` under ` + "`{{$.Prefix}}`" + `, with whatever
tool that destination takes. Koffr is not needed for this.

## 2. Check them before you trust them

The digests below cover the encrypted bytes, so they can be verified
**without any key at all**. A mismatch means the object is damaged, and no
amount of decryption will fix it.

` + "```sh" + `
{{range .Objects -}}
echo "{{.SHA256}}  {{.Key}}" | sha256sum -c
{{end -}}
` + "```" + `

## 3. Decrypt

The backup is encrypted for these recipients:

{{range .Recipients -}}
- ` + "`{{.}}`" + `
{{end}}
You need the matching age identity. Put it in ` + "`koffr-identity.txt`" + `, readable
only by you, and delete it when you are done.

Decompression is left to the steps below, which pipe through ` + "`zstd`" + `. Writing the
uncompressed backup out first would need disk space for the whole of it, and the
next step is going to read it once and stream it anyway.

` + "```sh" + `
chmod 600 koffr-identity.txt
{{range .Objects -}}
age -d -i koffr-identity.txt {{.Key}} > {{.Unsealed}}
{{end -}}
` + "```" + `

## {{.Procedure.Title}}

{{if .NamesTargetDB}}` + "`DBNAME`" + ` below is the database to restore *into*, which is your choice and
not something the backup can name: the source's own database name lives in the
encrypted ` + "`details.json.age`" + `, not in the plaintext manifest.
{{end}}{{range $i, $s := .Procedure.Steps}}
### {{$s.Title}}

{{$s.Body}}

` + "```sh" + `
{{$s.Command}}
` + "```" + `
{{end}}
---

Generated by Koffr {{.Manifest.KoffrVersion}} from ` + "`manifest.json`" + `. The manifest is
plaintext and holds everything above; this document exists so nobody has to read
it to get their data back.
`
