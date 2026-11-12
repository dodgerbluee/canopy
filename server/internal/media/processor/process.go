package processor

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ProcessSandbox runs the installed Linux codec tools inside the application
// container. Limits apply to each codec, not to the API or Go worker. It is not
// a filesystem/network isolation boundary: codecs share the worker's UID.
type ProcessSandbox struct {
	MemoryBytes uint64
	Timeout     time.Duration
}

func NewProcessSandbox(memory string, timeout time.Duration) (*ProcessSandbox, error) {
	value := strings.ToLower(strings.TrimSpace(memory))
	multiplier := uint64(1)
	for _, unit := range []struct {
		suffix string
		factor uint64
	}{{"gib", 1 << 30}, {"mib", 1 << 20}, {"kib", 1 << 10}, {"gb", 1 << 30}, {"mb", 1 << 20}, {"kb", 1 << 10}, {"g", 1 << 30}, {"m", 1 << 20}, {"k", 1 << 10}, {"b", 1}} {
		if strings.HasSuffix(value, unit.suffix) {
			value = strings.TrimSuffix(value, unit.suffix)
			multiplier = unit.factor
			break
		}
	}
	amount, err := strconv.ParseUint(value, 10, 64)
	if err != nil || amount == 0 || amount > (1<<63-1)/multiplier {
		return nil, fmt.Errorf("invalid media memory limit")
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("media timeout must be positive")
	}
	return &ProcessSandbox{MemoryBytes: amount * multiplier, Timeout: timeout}, nil
}

func (s *ProcessSandbox) Run(ctx context.Context, workDir string, argv ...string) (Output, error) {
	if len(argv) == 0 {
		return Output{}, fmt.Errorf("%w: missing codec command", ErrSandboxUnavailable)
	}
	switch argv[0] {
	case "vips", "vipsheader", "tesseract", "pdftoppm":
	default:
		return Output{}, fmt.Errorf("%w: unsupported codec command", ErrSandboxUnavailable)
	}
	if !filepath.IsAbs(workDir) || s.MemoryBytes == 0 || s.Timeout <= 0 {
		return Output{}, fmt.Errorf("%w: invalid process limits or work directory", ErrSandboxUnavailable)
	}
	args := append([]string(nil), argv...)
	// The existing pipeline uses /work paths. Map only that virtual prefix;
	// OCR's relative paths already resolve against cmd.Dir.
	for i, a := range args[1:] {
		if strings.HasPrefix(a, "/work/") {
			relative := strings.TrimPrefix(a, "/work/")
			if !filepath.IsLocal(relative) {
				return Output{}, fmt.Errorf("%w: invalid codec path", ErrSandboxUnavailable)
			}
			args[i+1] = filepath.Join(workDir, relative)
		}
	}
	tool, err := exec.LookPath(args[0])
	if err != nil {
		return Output{}, fmt.Errorf("%w: codec %s is not installed", ErrSandboxUnavailable, args[0])
	}
	limiter, err := exec.LookPath("prlimit")
	if err != nil {
		return Output{}, fmt.Errorf("%w: prlimit is not installed", ErrSandboxUnavailable)
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	seconds := int64(s.Timeout/time.Second) + 1
	commandArgs := []string{"--as=" + strconv.FormatUint(s.MemoryBytes, 10), "--cpu=" + strconv.FormatInt(seconds, 10), "--fsize=1073741824", "--nofile=128", "--core=0", "--", tool}
	commandArgs = append(commandArgs, args[1:]...)
	cmd := exec.CommandContext(ctx, limiter, commandArgs...)
	cmd.Dir = workDir
	// Do not pass provider keys, database credentials or proxy settings to codecs.
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + workDir, "TMPDIR=" + workDir, "LANG=C.UTF-8", "VIPS_CONCURRENCY=2", "VIPS_DISC_THRESHOLD=64m", "MALLOC_ARENA_MAX=2", "OMP_THREAD_LIMIT=2"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	stdout, stderr := &limitedOutput{limit: 1 << 20}, &limitedOutput{limit: 64 << 10}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err = cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) && cmd.Process != nil {
		// A tool exited but a descendant retained a pipe. Do not leave that
		// descendant running after the job returns and removes its workspace.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	output := Output{Stdout: stdout.data, Stderr: stderr.data}
	if ctx.Err() != nil {
		return Output{}, ctx.Err()
	}
	if stdout.exceeded || stderr.exceeded {
		return output, &SandboxError{Argv: argv, Exit: 1, Stderr: "codec output limit exceeded"}
	}
	if err == nil {
		return output, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return output, &SandboxError{Argv: argv, Exit: exit.ExitCode(), Stderr: tail(output.Stderr, 400)}
	}
	return output, fmt.Errorf("%w: codec could not run: %v", ErrSandboxUnavailable, err)
}

// limitedOutput continues draining the pipe without retaining unbounded bytes.
type limitedOutput struct {
	data     []byte
	limit    int
	exceeded bool
}

func (b *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	room := b.limit - len(b.data)
	if n > room {
		b.exceeded = true
		p = p[:room]
	}
	b.data = append(b.data, p...)
	return n, nil
}
