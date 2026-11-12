package processor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Sandbox runs codec commands as direct argv inside a fresh, resource-
// limited container. argv is never interpolated into a shell, workDir is
// a server-created temp directory mounted at /work, and the container
// has no network. A hostile image can only damage its own sandbox.
type Sandbox interface {
	Run(ctx context.Context, workDir string, argv ...string) (Output, error)
}

// Output carries the captured streams of one sandbox command.
type Output struct {
	Stdout []byte
	Stderr []byte
}

// SandboxError means the command ran and exited non-zero: a domain-level
// verdict (corrupt input, OOM-killed bomb that slipped the budget).
type SandboxError struct {
	Argv   []string
	Exit   int // container exit code (137 = SIGKILL, e.g. cgroup OOM)
	Stderr string
}

func (e *SandboxError) Error() string {
	return fmt.Sprintf("sandbox command %v exited %d: %s", e.Argv, e.Exit, e.Stderr)
}

// ErrSandboxUnavailable is an infrastructure failure (docker missing,
// image absent, daemon down): the job must be retried, not failed at the
// domain level.
var ErrSandboxUnavailable = errors.New("media sandbox unavailable")

// DockerSandbox is the production Sandbox. Memory and swap are pinned
// equal so the cgroup cannot lean on swap; pids are capped so fork bombs
// die at once; --user maps the container to the host uid so bind-mounted
// outputs stay manageable.
type DockerSandbox struct {
	Image  string
	Memory string // e.g. "512m"
	PIDs   int
	// NamePrefix scopes container names; tests use a unique prefix so
	// their "no leftovers" assertions cannot be tripped by sandboxes
	// belonging to other packages running concurrently.
	NamePrefix string
}

func (s *DockerSandbox) prefix() string {
	if s.NamePrefix != "" {
		return s.NamePrefix
	}
	return "canopy-media-"
}

func (s *DockerSandbox) Run(ctx context.Context, workDir string, argv ...string) (Output, error) {
	name := s.prefix() + uuid.NewString()[:12]
	args := s.dockerArgs(name, workDir, argv)

	var out, errb bytes.Buffer
	cmd := exec.Command("docker", args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Start(); err != nil {
		return Output{}, fmt.Errorf("%w: %v", ErrSandboxUnavailable, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		o := Output{Stdout: out.Bytes(), Stderr: errb.Bytes()}
		if err == nil {
			return o, nil
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return o, &SandboxError{Argv: argv, Exit: ee.ExitCode(), Stderr: tail(o.Stderr, 400)}
		}
		return o, err
	case <-ctx.Done():
		// kill the container, then reap the client; --rm removes it
		kctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = exec.CommandContext(kctx, "docker", "kill", name).Run()
		<-done
		// --rm removal is asynchronous on the daemon: under load the
		// container can still be listed right after the client exits.
		// Wait until it is actually gone so callers never observe a
		// "killed" sandbox still present (bounded by kctx).
		for {
			out, err := exec.CommandContext(kctx, "docker", "ps", "-a", "-q",
				"--filter", "name="+name).Output()
			if err != nil || strings.TrimSpace(string(out)) == "" {
				break
			}
			select {
			case <-kctx.Done():
			case <-time.After(200 * time.Millisecond):
			}
			if kctx.Err() != nil {
				break
			}
		}
		return Output{}, ctx.Err()
	}
}

// dockerArgs assembles the docker invocation: hard isolation flags
// first, then the pinned image, then the codec command as direct argv
// (never a shell). The image must sit before argv or docker would read
// argv[0] as the image name.
func (s *DockerSandbox) dockerArgs(name, workDir string, argv []string) []string {
	args := []string{"run", "--rm", "--name", name,
		"--network=none",
		"--memory", s.Memory, "--memory-swap", s.Memory,
		"--pids-limit", strconv.Itoa(s.PIDs),
		"--user", strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()),
		"-v", workDir + ":/work",
		s.Image,
	}
	return append(args, argv...)
}

func tail(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[len(b)-n:])
}
