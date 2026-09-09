package binlog_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcmariadb "github.com/testcontainers/testcontainers-go/modules/mariadb"

	"github.com/Gu1llaum-3/koffr/internal/binlog"
	"github.com/Gu1llaum-3/koffr/internal/crypto"
	"github.com/Gu1llaum-3/koffr/internal/executor/local"
	"github.com/Gu1llaum-3/koffr/internal/notify"
	"github.com/Gu1llaum-3/koffr/internal/source/mariadb"
	"github.com/Gu1llaum-3/koffr/internal/storage"
	"github.com/Gu1llaum-3/koffr/internal/storage/memory"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

const (
	adminUser = "root"
	adminPass = testutil.SecretSentinel
	database  = "shop"
)

var shared struct {
	host    string
	port    int
	skipWhy string
	// target is a second server with no binary log: where a point-in-time
	// recovery lands, kept apart from the source so the two cannot be confused.
	targetHost string
	targetPort int
}

func TestMain(m *testing.M) {
	os.Exit(func() int {
		if why := testutil.EnsureDockerHost(); why != "" {
			shared.skipWhy = why
		}
		if shared.skipWhy == "" {
			if _, err := exec.LookPath("mariadb-binlog"); err != nil {
				if _, err := exec.LookPath("mysqlbinlog"); err != nil {
					shared.skipWhy = "neither mariadb-binlog nor mysqlbinlog is on PATH"
				}
			}
		}
		var container, target testcontainers.Container
		if shared.skipWhy == "" {
			c, err := start()
			if err != nil {
				shared.skipWhy = fmt.Sprintf("mariadb container unavailable: %v", err)
			}
			container = c
		}
		if shared.skipWhy == "" {
			c, err := startTarget()
			if err != nil {
				shared.skipWhy = fmt.Sprintf("target container unavailable: %v", err)
			}
			target = c
		}
		if _, fatal := testutil.SkipOrFailWithoutDocker(shared.skipWhy); fatal != "" {
			fmt.Fprintln(os.Stderr, fatal)
			return 1
		}
		defer func() {
			if container != nil {
				_ = testcontainers.TerminateContainer(container)
			}
			if target != nil {
				_ = testcontainers.TerminateContainer(target)
			}
		}()
		return m.Run()
	}())
}

func start() (testcontainers.Container, error) {
	ctx := context.Background()
	c, err := tcmariadb.Run(ctx, mariadbImage(),
		tcmariadb.WithDatabase(database),
		tcmariadb.WithUsername("koffr"),
		tcmariadb.WithPassword(adminPass),
		// A small max_binlog_size makes the server rotate often, so a test
		// sees closed files in seconds rather than after a gigabyte of writes.
		testcontainers.WithCmd("mariadbd", "--log-bin=binlog", "--server-id=1", "--max-binlog-size=65536"),
	)
	if err != nil {
		return nil, err
	}
	host, err := c.Host(ctx)
	if err != nil {
		return c, err
	}
	port, err := c.MappedPort(ctx, "3306/tcp")
	if err != nil {
		return c, err
	}
	shared.host = host
	shared.port, _ = strconv.Atoi(port.Port())
	for range 60 {
		db, err := sql.Open("mysql", adminDSN(shared.host, shared.port))
		if err == nil && db.PingContext(ctx) == nil {
			_ = db.Close()
			return c, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return c, fmt.Errorf("server never answered")
}

func startTarget() (testcontainers.Container, error) {
	ctx := context.Background()
	c, err := tcmariadb.Run(ctx, mariadbImage(),
		tcmariadb.WithDatabase(database),
		tcmariadb.WithUsername("koffr"),
		tcmariadb.WithPassword(adminPass),
	)
	if err != nil {
		return nil, err
	}
	host, err := c.Host(ctx)
	if err != nil {
		return c, err
	}
	port, err := c.MappedPort(ctx, "3306/tcp")
	if err != nil {
		return c, err
	}
	shared.targetHost = host
	shared.targetPort, _ = strconv.Atoi(port.Port())
	for range 60 {
		db, err := sql.Open("mysql", adminDSN(shared.targetHost, shared.targetPort))
		if err == nil && db.PingContext(ctx) == nil {
			_ = db.Close()
			return c, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return c, fmt.Errorf("target never answered")
}

func adminDSN(host string, port int) string {
	cfg := mysql.NewConfig()
	cfg.User, cfg.Passwd = adminUser, adminPass
	cfg.Net, cfg.Addr, cfg.DBName = "tcp", net.JoinHostPort(host, strconv.Itoa(port)), database
	return cfg.FormatDSN()
}

func skipUnlessReady(t *testing.T) {
	t.Helper()
	if shared.skipWhy != "" {
		t.Skip(shared.skipWhy)
	}
}

func execSQL(t *testing.T, statements ...string) {
	t.Helper()
	db, err := sql.Open("mysql", adminDSN(shared.host, shared.port))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	for _, s := range statements {
		_, err := db.ExecContext(context.Background(), s)
		require.NoError(t, err, s)
	}
}

// execOn runs statements on the given server, in the shared test database.
func execOn(t *testing.T, host string, port int, statements ...string) {
	t.Helper()
	db, err := sql.Open("mysql", adminDSN(host, port))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	for _, s := range statements {
		_, err := db.ExecContext(context.Background(), s)
		require.NoError(t, err, s)
	}
}

// churn writes enough to rotate the binary log several times.
func churn(t *testing.T, rows int) {
	t.Helper()
	execSQL(t, "CREATE TABLE IF NOT EXISTS churn (id INT AUTO_INCREMENT PRIMARY KEY, v VARCHAR(255)) ENGINE=InnoDB")
	db, err := sql.Open("mysql", adminDSN(shared.host, shared.port))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	for i := 0; i < rows; i++ {
		_, err := db.ExecContext(context.Background(),
			"INSERT INTO churn (v) VALUES (REPEAT('x', 200))")
		require.NoError(t, err)
	}
}

type rig struct {
	store  *memory.Storage
	sealer crypto.Sealer
	opener crypto.Opener
	src    storage.Source
	events []notify.Event
	mu     sync.Mutex
}

func newRig(t *testing.T) *rig {
	t.Helper()
	op, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	rec, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	sealer, err := crypto.NewSealer([]string{op.Recipient().String(), rec.Recipient().String()})
	require.NoError(t, err)
	opener, err := crypto.NewOpener(op.String())
	require.NoError(t, err)
	src, err := storage.ForSource("shop")
	require.NoError(t, err)
	return &rig{store: memory.New(), sealer: sealer, opener: opener, src: src}
}

func (r *rig) archive() *binlog.Archive {
	return &binlog.Archive{Source: r.src, Storage: r.store, Sealer: r.sealer}
}

func (r *rig) notify(ev notify.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *rig) config(host string, port int) mariadb.Config {
	return mariadb.Config{
		Host: host, Port: port, User: adminUser, Password: adminPass,
		Database: database, TLS: "disable", ToolRunner: local.New(),
	}
}

func (r *rig) supervisor(t *testing.T, host string, port int) *binlog.Supervisor {
	t.Helper()
	return &binlog.Supervisor{
		SourceID: "shop",
		Config:   r.config(host, port),
		Tools:    local.New(),
		Reach:    local.New(),
		Spool:    t.TempDir(),
		Bounds:   binlog.Bounds{High: 64 << 20},
		ServerID: 4242,
		Archive:  r.archive(),
		Notify:   r.notify,
		Logf:     t.Logf,
		Tick:     200 * time.Millisecond,
		// Short, so a test that expects a crash loop sees it in seconds.
		TransientUptime: 2 * time.Second,
		MinBackoff:      100 * time.Millisecond,
		MaxBackoff:      time.Second,
	}
}

// The core promise: closed files, and only closed files, reach the repository,
// under their own names, with an index that says what they hold.
func TestSupervisor_ArchivesClosedFiles(t *testing.T) {
	skipUnlessReady(t)
	r := newRig(t)
	sup := r.supervisor(t, shared.host, shared.port)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()

	churn(t, 600) // ~120 KiB of events against a 64 KiB max_binlog_size
	require.Eventually(t, func() bool {
		names, err := r.archive().Archived(t.Context())
		return err == nil && len(names) >= 2
	}, 30*time.Second, 250*time.Millisecond, "closed files never reached the repository")

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)

	names, err := r.archive().Archived(t.Context())
	require.NoError(t, err)
	gaps, err := binlog.Gaps(names)
	require.NoError(t, err)
	assert.Empty(t, gaps, "the archive must be continuous")

	// The newest file the server holds is the one being written; it must not
	// be in the repository.
	st, err := r.config(shared.host, shared.port).Binlog(t.Context(), local.New())
	require.NoError(t, err)
	for _, n := range names {
		assert.NotEqual(t, st.File, n.String(), "the open file was archived")
	}

	// Every archived file has an index naming a real digest and a first event.
	for _, n := range names {
		e, err := r.archive().Index(t.Context(), n)
		require.NoError(t, err, n)
		assert.Len(t, e.SHA256, 64)
		assert.Positive(t, e.PlainSize)
		assert.False(t, e.FirstEventAt.IsZero(), "the first event's time is what a PITR selects files by")
		assert.WithinDuration(t, time.Now(), e.FirstEventAt, time.Hour)
	}
	// And the spool holds only the open file.
	left, err := os.ReadDir(sup.Spool)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(left), 1, "closed files must leave the spool once archived")
}

// severingProxy sits between the receiver and the server and, when told to,
// tears down every connection it carries -- live ones included, which is the
// lesson of the netcut probe: refusing only new connections proves nothing,
// because a stream reuses the one it has.
type severingProxy struct {
	ln     net.Listener
	target string
	mu     sync.Mutex
	live   map[net.Conn]struct{}
	down   atomic.Bool
	cuts   atomic.Int32
}

func newSeveringProxy(t *testing.T, target string) *severingProxy {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	p := &severingProxy{ln: ln, target: target, live: map[net.Conn]struct{}{}}
	go p.serve()
	t.Cleanup(func() { _ = ln.Close(); p.sever() })
	return p
}

func (p *severingProxy) addr() (string, int) {
	host, port, _ := net.SplitHostPort(p.ln.Addr().String())
	n, _ := strconv.Atoi(port)
	return host, n
}

func (p *severingProxy) serve() {
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer func() { _ = c.Close() }()
			if p.down.Load() {
				return
			}
			u, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", p.target)
			if err != nil {
				return
			}
			defer func() { _ = u.Close() }()
			p.mu.Lock()
			p.live[c], p.live[u] = struct{}{}, struct{}{}
			p.mu.Unlock()
			defer func() {
				p.mu.Lock()
				delete(p.live, c)
				delete(p.live, u)
				p.mu.Unlock()
			}()
			done := make(chan struct{}, 2)
			cp := func(d, s net.Conn) { _, _ = io.Copy(d, s); done <- struct{}{} }
			go cp(u, c)
			go cp(c, u)
			<-done
		}(c)
	}
}

// sever cuts every live connection and refuses new ones until restore.
func (p *severingProxy) sever() {
	p.down.Store(true)
	p.cuts.Add(1)
	p.mu.Lock()
	for c := range p.live {
		_ = c.Close()
	}
	p.live = map[net.Conn]struct{}{}
	p.mu.Unlock()
}

func (p *severingProxy) restore() { p.down.Store(false) }

// The reason this package exists. The link to the server is cut, repeatedly,
// while the server keeps writing; when it is over the archive has every file
// exactly once. No hole, no duplicate, and nothing that needed an operator.
func TestSupervisor_SurvivesTheLinkBeingCut(t *testing.T) {
	skipUnlessReady(t)
	r := newRig(t)
	proxy := newSeveringProxy(t, net.JoinHostPort(shared.host, strconv.Itoa(shared.port)))
	host, port := proxy.addr()
	sup := r.supervisor(t, host, port)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()

	// Writes and cuts, interleaved: the receiver dies mid-file more than once.
	for round := 0; round < 4; round++ {
		churn(t, 150)
		proxy.sever()
		time.Sleep(700 * time.Millisecond)
		proxy.restore()
		time.Sleep(300 * time.Millisecond)
	}
	churn(t, 300)

	st, err := r.config(shared.host, shared.port).Binlog(t.Context(), local.New())
	require.NoError(t, err)
	// Everything the server has closed must end up archived.
	want := len(st.Files) - 1
	require.Eventually(t, func() bool {
		names, err := r.archive().Archived(t.Context())
		return err == nil && len(names) >= want
	}, 60*time.Second, 250*time.Millisecond, "the archive never caught up after the cuts")

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)

	names, err := r.archive().Archived(t.Context())
	require.NoError(t, err)
	gaps, err := binlog.Gaps(names)
	require.NoError(t, err)
	assert.Empty(t, gaps, "a cut must never leave a hole")
	assert.GreaterOrEqual(t, int(proxy.cuts.Load()), 4, "the proxy must actually have cut something")

	// No gap was ever reported, and no crash loop: the cuts were accidents,
	// which is what the supervisor exists to ride out.
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ev := range r.events {
		assert.NotEqual(t, "binlog.crashloop", ev.Kind, "a link that drops is not a crash loop")
		assert.NotEqual(t, "binlog.gap", ev.Kind)
	}

	// Each archived file's content matches what the server wrote: read one
	// back through age and zstd and check its magic and digest agree with the
	// index.
	e, err := r.archive().Index(t.Context(), names[0])
	require.NoError(t, err)
	e2, err := r.archive().Reindex(t.Context(), names[0], r.opener, time.Now())
	require.NoError(t, err)
	assert.Equal(t, e.SHA256, e2.SHA256, "the stored object must hash to what its index says")
	assert.Equal(t, e.PlainSize, e2.PlainSize)
}

// A receiver that dies at once, over and over, is not a network problem. It is
// a changed password or a revoked grant, and relaunching it every second turns
// one alert into a night of them.
func TestSupervisor_StopsOnACrashLoopAndSaysSo(t *testing.T) {
	skipUnlessReady(t)
	r := newRig(t)
	sup := r.supervisor(t, shared.host, shared.port)
	sup.Config.Password = "not-the-password"
	sup.CrashLoopAfter = 3

	err := sup.Run(t.Context())
	require.ErrorIs(t, err, binlog.ErrCrashLoop)
	testutil.AssertNoSecretLeak(t, err.Error())

	r.mu.Lock()
	defer r.mu.Unlock()
	require.NotEmpty(t, r.events)
	assert.Equal(t, "binlog.crashloop", r.events[len(r.events)-1].Kind)
	assert.Equal(t, notify.SeverityError, r.events[len(r.events)-1].Severity)
}

// The spool is the buffer, and it is bounded. With the archive refusing every
// write, the receiver must be stopped once the spool passes the high mark
// rather than filling the disk.
func TestSupervisor_PausesTheReceiverWhenTheSpoolIsFull(t *testing.T) {
	skipUnlessReady(t)
	r := newRig(t)
	sup := r.supervisor(t, shared.host, shared.port)
	sup.Archive = &binlog.Archive{Source: r.src, Storage: refusing{r.store}, Sealer: r.sealer}
	sup.Bounds = binlog.Bounds{High: 100 << 10, Low: 20 << 10}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()

	churn(t, 1500)
	// The spool holds the files nothing could archive; once past the high
	// mark, the receiver is paused and the spool stops growing.
	var last uint64
	require.Eventually(t, func() bool {
		var total uint64
		entries, _ := os.ReadDir(sup.Spool)
		for _, e := range entries {
			if info, err := e.Info(); err == nil {
				total += uint64(info.Size())
			}
		}
		last = total
		return total >= 100<<10
	}, 30*time.Second, 200*time.Millisecond, "the spool never reached the high mark")

	churn(t, 800)
	time.Sleep(2 * time.Second)
	var after uint64
	entries, _ := os.ReadDir(sup.Spool)
	for _, e := range entries {
		if info, err := e.Info(); err == nil {
			after += uint64(info.Size())
		}
	}
	// One open file may still have been growing when the gate closed; it is
	// bounded by max_binlog_size, so the spool cannot have run away.
	assert.Less(t, after, last+2*64<<10, "the spool kept growing after the high mark")

	cancel()
	<-done
}

// refusing is a repository that accepts nothing, standing in for one that is
// unreachable.
type refusing struct{ storage.Storage }

func (refusing) Put(context.Context, string, io.Reader, storage.PutOptions) (storage.ObjectInfo, error) {
	return storage.ObjectInfo{}, fmt.Errorf("repository unreachable")
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

func parseName(name string) (uint64, error) {
	n, err := binlog.Parse(name)
	if err != nil {
		return 0, err
	}
	return n.Seq, nil
}

// Rotation is optional and off by default; when on, it turns a quiet
// database's open file into something archivable -- and only when something
// was written, because rotating an idle server archives a padded, empty file
// every interval for nothing.
func TestSupervisor_RotatesOnlyWhenSomethingWasWritten(t *testing.T) {
	skipUnlessReady(t)
	r := newRig(t)
	sup := r.supervisor(t, shared.host, shared.port)
	sup.Rotate = 700 * time.Millisecond

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	defer func() { cancel(); <-done }()

	cfg := r.config(shared.host, shared.port)
	// The first pass only takes a baseline -- the very first rotation waits one
	// interval like every other -- so a write that lands before it is part of
	// the baseline and not growth. Let the baseline be taken first.
	time.Sleep(4 * sup.Tick)
	before, err := cfg.Binlog(t.Context(), local.New())
	require.NoError(t, err)

	// A write, then enough time for the rotation to be due: the file closes
	// without anyone asking the server by hand.
	execSQL(t, "CREATE TABLE IF NOT EXISTS rotated (id INT PRIMARY KEY) ENGINE=InnoDB",
		"INSERT IGNORE INTO rotated VALUES (1)")
	require.Eventually(t, func() bool {
		st, err := cfg.Binlog(t.Context(), local.New())
		return err == nil && len(st.Files) > len(before.Files)
	}, 10*time.Second, 200*time.Millisecond, "a written-to file was never rotated")

	// Then nothing is written. Several intervals go by; the file count must
	// hold, or the option would fill the archive with empty files on a server
	// that takes one write a day.
	settled, err := cfg.Binlog(t.Context(), local.New())
	require.NoError(t, err)
	time.Sleep(3 * sup.Rotate)
	after, err := cfg.Binlog(t.Context(), local.New())
	require.NoError(t, err)
	assert.Equal(t, len(settled.Files), len(after.Files), "an idle server was rotated")
}

// mariadbImage is the server under test; make verify-mariadb-matrix walks the
// supported majors through it, CI pins one.
func mariadbImage() string {
	if img := os.Getenv("KOFFR_MARIADB_IMAGE"); img != "" {
		return img
	}
	return "mariadb:11.4"
}

// The server purged files the archive never got -- an outage longer than
// expire_logs_days. Asking the server for them is a refusal on every start,
// which the supervisor used to read as a crash loop and stop for good. What is
// lost is lost either way; the supervisor says so once and archives what the
// server still has, so the next backup's recoveries are whole.
func TestSupervisor_ResumesPastAPurgeAndSaysSo(t *testing.T) {
	skipUnlessReady(t)
	r := newRig(t)
	ex := local.New()
	cfg := r.config(shared.host, shared.port)
	execSQL(t, "CREATE TABLE IF NOT EXISTS purged (id INT PRIMARY KEY) ENGINE=InnoDB")

	// First life: archive at least one file, then stop.
	sup := r.supervisor(t, shared.host, shared.port)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	execSQL(t, "INSERT INTO purged VALUES (FLOOR(RAND()*1000000000))")
	require.NoError(t, cfg.RotateBinlog(t.Context(), ex))
	require.Eventually(t, func() bool {
		names, err := r.archive().Archived(t.Context())
		return err == nil && len(names) > 0
	}, 30*time.Second, 200*time.Millisecond)
	cancel()
	<-done
	archived, err := r.archive().Archived(t.Context())
	require.NoError(t, err)
	resume, _ := binlog.ResumeFrom(archived)

	// Meanwhile: three more files, and the server purges everything but the
	// last two -- the resume file among them.
	for range 3 {
		execSQL(t, "INSERT INTO purged VALUES (FLOOR(RAND()*1000000000))")
		require.NoError(t, cfg.RotateBinlog(t.Context(), ex))
	}
	st, err := cfg.Binlog(t.Context(), ex)
	require.NoError(t, err)
	keepFrom := st.Files[len(st.Files)-2].Name
	execSQL(t, "PURGE BINARY LOGS TO '"+keepFrom+"'")
	st, err = cfg.Binlog(t.Context(), ex)
	require.NoError(t, err)
	oldest := mustSeq(t, st.Files[0].Name)
	require.Greater(t, oldest, resume.Seq, "the purge must have taken the resume file")

	// Second life.
	sup2 := r.supervisor(t, shared.host, shared.port)
	ctx2, cancel2 := context.WithCancel(t.Context())
	done2 := make(chan error, 1)
	go func() { done2 <- sup2.Run(ctx2) }()
	defer func() { cancel2(); <-done2 }()

	require.Eventually(t, func() bool {
		names, err := r.archive().Archived(t.Context())
		return err == nil && names[len(names)-1].Seq >= oldest
	}, 30*time.Second, 200*time.Millisecond, "archiving must go on from what the server still has")

	r.mu.Lock()
	defer r.mu.Unlock()
	var gaps []notify.Event
	for _, ev := range r.events {
		if ev.Kind == "binlog.gap" {
			gaps = append(gaps, ev)
		}
	}
	require.Len(t, gaps, 1, "the hole is reported exactly once")
	assert.Contains(t, gaps[0].Message, "purged")
	assert.Contains(t, gaps[0].Message, resume.String())
	assert.Contains(t, gaps[0].Message, st.Files[0].Name, "the operator is told where archiving resumes")
	select {
	case err := <-done2:
		t.Fatalf("the supervisor stopped instead of going on: %v", err)
	default:
	}
}
