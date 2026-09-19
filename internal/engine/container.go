package engine

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/Gu1llaum-3/koffr/internal/domain/resolve"
)

// ContainerOptions configures the exec strategy. Its zero value talks to the
// Docker the machine is configured for.
type ContainerOptions struct {
	// Socket, when set, is the Docker socket to use instead of the configured
	// one. Naming it makes a failure say which socket koffr wanted.
	Socket string

	// Timeout bounds one `--version` inside a container.
	Timeout time.Duration
}

// ContainerFinder resolves a tool inside the container of a database — the
// `exec` strategy of § 5.2 F2.9. It is declared per database and never
// globally, because it hands koffr the Docker socket (E-046).
type ContainerFinder struct {
	options ContainerOptions
}

// NewContainerFinder builds the adapter.
func NewContainerFinder(options ContainerOptions) *ContainerFinder {
	if options.Timeout <= 0 {
		options.Timeout = defaultTimeout
	}

	return &ContainerFinder{options: options}
}

// FindIn enumerates the tools of a family inside a container, and reads their
// version by running them **there**. A MariaDB image ships the client that
// matches its own server, which is the tool the strategy exists to reach.
//
// Without a reachable Docker socket it fails saying so: falling back on a host
// tool would be the silent substitution E-046 refuses — the operator asked for
// the container's tool for a reason.
func (f *ContainerFinder) FindIn(ctx context.Context, name string, family resolve.Family, tool resolve.Tool) ([]resolve.Candidate, error) {
	names, known := binaryNames[family][tool]
	if !known {
		return nil, resolve.ErrUnsupportedEngine
	}

	docker, err := f.connect()
	if err != nil {
		return nil, fmt.Errorf("reach Docker to look inside %s: %w", name, err)
	}
	defer func() { _ = docker.Close() }()

	if _, err := docker.ContainerInspect(ctx, name); err != nil {
		return nil, fmt.Errorf("look inside the container %s: %w", name, err)
	}

	var found []resolve.Candidate

	for _, binary := range names {
		announced, err := f.runInside(ctx, docker, name, binary)
		if err != nil {
			continue
		}

		version := resolve.ParseVersion(announced)
		if version.IsZero() {
			continue
		}

		toolFamily, named := toolFamily(announced)
		if !named {
			continue
		}

		found = append(found, resolve.Candidate{
			Family:  toolFamily,
			Tool:    tool,
			Path:    binary,
			Version: version,
			Source:  resolve.Container,
		})
	}

	return found, nil
}

// connect opens the Docker client. When a socket is named, koffr uses that one
// and nothing else, so that an error can say which socket it wanted.
func (f *ContainerFinder) connect() (*client.Client, error) {
	options := []client.Opt{client.WithAPIVersionNegotiation()}

	if f.options.Socket != "" {
		options = append(options, client.WithHost("unix://"+f.options.Socket))
	} else {
		options = append(options, client.FromEnv)
	}

	docker, err := client.NewClientWithOpts(options...)
	if err != nil {
		return nil, fmt.Errorf("the Docker socket %s: %w", f.socketName(), err)
	}

	if _, err := docker.Ping(context.Background()); err != nil {
		_ = docker.Close()

		return nil, fmt.Errorf("the Docker socket %s did not answer: %w", f.socketName(), err)
	}

	return docker, nil
}

func (f *ContainerFinder) socketName() string {
	if f.options.Socket != "" {
		return f.options.Socket
	}

	return "of this machine"
}

// runInside executes one `--version` in the container and returns what it said.
func (f *ContainerFinder) runInside(ctx context.Context, docker *client.Client, name, binary string) (string, error) {
	bounded, cancel := context.WithTimeout(ctx, f.options.Timeout)
	defer cancel()

	created, err := docker.ContainerExecCreate(bounded, name, container.ExecOptions{
		Cmd:          []string{binary, "--version"},
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return "", fmt.Errorf("ask %s for its version in %s: %w", binary, name, err)
	}

	attached, err := docker.ContainerExecAttach(bounded, created.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", fmt.Errorf("read what %s answered in %s: %w", binary, name, err)
	}
	defer attached.Close()

	var out, errOut bytes.Buffer
	if _, err := stdcopy.StdCopy(&out, &errOut, attached.Reader); err != nil {
		return "", fmt.Errorf("read what %s answered in %s: %w", binary, name, err)
	}

	answer := strings.TrimSpace(out.String())
	if answer == "" {
		answer = strings.TrimSpace(errOut.String())
	}

	return answer, nil
}
