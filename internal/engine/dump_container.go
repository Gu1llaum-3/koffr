package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// dumpInContainer runs the dump inside the container of the database, with the
// client that image ships. ADR-0015 makes this the only way to serve a machine
// carrying both a MySQL and a MariaDB, since their clients cannot coexist.
func (e *Engine) dumpInContainer(ctx context.Context, request DumpRequest) (io.ReadCloser, error) {
	running, cancel := request.bounded(ctx)

	docker, err := e.docker.connect()
	if err != nil {
		cancel()

		return nil, fmt.Errorf("reach Docker to dump inside %s: %w", request.Container, err)
	}

	if _, err := docker.ContainerInspect(running, request.Container); err != nil {
		cancel()
		_ = docker.Close()

		return nil, fmt.Errorf("look inside the container %s: %w", request.Container, err)
	}

	argv := DumpCommand(request)

	created, err := docker.ContainerExecCreate(running, request.Container, container.ExecOptions{
		Cmd:          argv,
		Env:          request.passwordEnvironment(),
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		cancel()
		_ = docker.Close()

		return nil, fmt.Errorf("start %s in %s: %w", argv[0], request.Container, err)
	}

	attached, err := docker.ContainerExecAttach(running, created.ID, container.ExecAttachOptions{})
	if err != nil {
		cancel()
		_ = docker.Close()

		return nil, fmt.Errorf("read the output of %s in %s: %w", argv[0], request.Container, err)
	}

	out, into := io.Pipe()
	said := &tail{}
	done := make(chan struct{})

	go func() {
		defer close(done)

		// Docker multiplexes the two outputs on one connection; the archive is
		// the one that must reach the caller byte for byte.
		_, err := stdcopy.StdCopy(into, said, attached.Reader)
		_ = into.CloseWithError(err)
	}()

	return &containerDump{
		out:       out,
		what:      fmt.Sprintf("%s in %s", argv[0], request.Container),
		said:      said,
		done:      done,
		docker:    docker,
		attached:  closeFunc(attached.Close),
		execution: created.ID,
		cancel:    cancel,
	}, nil
}

type closeFunc func()

// containerDump is the output of a dump running inside a container. Its Close
// carries the same promise as the one on the host: it waits for the process and
// reports a non-zero exit, because an archive of nothing looks like an archive.
type containerDump struct {
	out       *io.PipeReader
	what      string
	said      *tail
	done      chan struct{}
	docker    *client.Client
	attached  closeFunc
	execution string
	cancel    context.CancelFunc

	drained bool
	closed  bool
}

func (c *containerDump) Read(into []byte) (int, error) {
	read, err := c.out.Read(into)
	if errors.Is(err, io.EOF) {
		c.drained = true
	}

	return read, err //nolint:wrapcheck // a pass-through of the demultiplexed stream
}

func (c *containerDump) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true

	c.attached()
	_ = c.out.CloseWithError(io.ErrClosedPipe)
	<-c.done // nothing writes to said after this, so it can be read

	defer c.cancel()
	defer func() { _ = c.docker.Close() }()

	if !c.drained {
		return nil // the caller gave up first; its own error is the one that matters
	}

	code, err := c.exitCode()
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("%s exited with %d%s", c.what, code, c.said.suffix())
	}

	return nil
}

// exitCode asks Docker how the execution ended. The stream can close a moment
// before the process is reaped, so koffr waits for it rather than reading a
// zero that means "not finished".
func (c *containerDump) exitCode() (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	for {
		inspected, err := c.docker.ContainerExecInspect(ctx, c.execution)
		if err != nil {
			return 0, fmt.Errorf("ask Docker how %s ended: %w", c.what, err)
		}
		if !inspected.Running {
			return inspected.ExitCode, nil
		}

		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("%s did not report an exit code", c.what)

		case <-time.After(50 * time.Millisecond):
		}
	}
}
