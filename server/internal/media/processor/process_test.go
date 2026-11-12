package processor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProcessMemoryConfig(t *testing.T) {
	for _, memory := range []string{"512m", "512MiB", "536870912"} {
		sb, err := NewProcessSandbox(memory, time.Second)
		if err != nil || sb.MemoryBytes != 512<<20 {
			t.Fatalf("%s: %v, %v", memory, sb, err)
		}
	}
	for _, memory := range []string{"", "0", "-1m", "big", "18446744073709551615g"} {
		if _, err := NewProcessSandbox(memory, time.Second); err == nil {
			t.Fatalf("accepted %q", memory)
		}
	}
	if _, err := NewProcessSandbox("512m", 0); err == nil {
		t.Fatal("accepted zero timeout")
	}
}

func processProbe(t *testing.T, script string) (*ProcessSandbox, string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("Linux prlimit runner; exercised inside the release image")
	}
	if _, err := exec.LookPath("prlimit"); err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()
	if err := os.WriteFile(filepath.Join(tools, "vips"), []byte("#!/bin/sh\nset -eu\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+":"+os.Getenv("PATH"))
	sb, err := NewProcessSandbox("512m", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return sb, t.TempDir()
}

func TestProcessRestrictions(t *testing.T) {
	sb, _ := NewProcessSandbox("512m", time.Second)
	for _, argv := range [][]string{nil, {"sh", "-c", "true"}, {"/usr/bin/vips"}, {"vips", "/work/../escape"}} {
		if _, err := sb.Run(context.Background(), t.TempDir(), argv...); !errors.Is(err, ErrSandboxUnavailable) {
			t.Fatalf("%v: %v", argv, err)
		}
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := sb.Run(context.Background(), t.TempDir(), "vips"); !errors.Is(err, ErrSandboxUnavailable) {
		t.Fatalf("missing tool: %v", err)
	}
}

func TestProcessPathsEnvironmentAndLimits(t *testing.T) {
	sb, dir := processProbe(t, `printf '%s\n' "$PWD" "$1" "$2" "${DATABASE_URL-unset}" "${CANOPY_AI_API_KEY-unset}"
ulimit -v
ulimit -t
ulimit -n
ulimit -c
`)
	t.Setenv("DATABASE_URL", "must-not-reach-codec")
	t.Setenv("CANOPY_AI_API_KEY", "must-not-reach-codec")
	argv := []string{"vips", "/work/input with spaces.jpg", "$(touch injected)"}
	out, err := sb.Run(context.Background(), dir, argv...)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(out.Stdout)), "\n")
	want := []string{dir, filepath.Join(dir, "input with spaces.jpg"), "$(touch injected)", "unset", "unset", "524288", "4", "128", "0"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("unexpected probe: %q", lines)
	}
	if argv[1] != "/work/input with spaces.jpg" {
		t.Fatal("mutated caller argv")
	}
}

func TestProcessBoundedOutputAndFailure(t *testing.T) {
	sb, dir := processProbe(t, "head -c 2000000 /dev/zero\n")
	out, err := sb.Run(context.Background(), dir, "vips")
	var se *SandboxError
	if !errors.As(err, &se) || len(out.Stdout) != 1<<20 {
		t.Fatalf("unbounded/partial-success output: %d %v", len(out.Stdout), err)
	}
	sb, dir = processProbe(t, "echo corrupt >&2\nexit 7\n")
	_, err = sb.Run(context.Background(), dir, "vips")
	if !errors.As(err, &se) || se.Exit != 7 || !strings.Contains(se.Stderr, "corrupt") {
		t.Fatalf("failure lost: %v", err)
	}
}

func TestProcessCancellationKillsDescendants(t *testing.T) {
	for _, parentDeadline := range []bool{false, true} {
		t.Run(strconv.FormatBool(parentDeadline), func(t *testing.T) {
			sb, dir := processProbe(t, "sleep 30 &\necho $! > child.pid\nwait\n")
			sb.Timeout = 400 * time.Millisecond
			ctx := context.Background()
			cancel := func() {}
			if parentDeadline {
				ctx, cancel = context.WithTimeout(ctx, 250*time.Millisecond)
			}
			defer cancel()
			start := time.Now()
			_, err := sb.Run(ctx, dir, "vips")
			if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second {
				t.Fatalf("timeout: %v", err)
			}
			data, err := os.ReadFile(filepath.Join(dir, "child.pid"))
			if err != nil {
				t.Fatal(err)
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Fatal(err)
			}
			// A killed orphan may remain briefly as a zombie until PID 1 reaps it.
			stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
			if err == nil && !strings.Contains(string(stat), ") Z ") && syscall.Kill(pid, 0) == nil {
				t.Fatalf("codec descendant %d survived cancellation", pid)
			}
		})
	}
}

func TestProcessRealPhotoCorpus(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("run inside combined image")
	}
	if _, err := exec.LookPath("vips"); err != nil {
		t.Skip("codec tools not installed")
	}
	sb, err := NewProcessSandbox("512m", 120*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []string{"iphone-12mp.heic", "rotated-12mp.jpg", "photo-640.webp", "tiny.png", "corrupt.jpg", "bomb.png"} {
		t.Run(fixture, func(t *testing.T) {
			dir, src := corpusWorkDir(t, fixture)
			ds, err := Run(context.Background(), sb, dir, src, 6, DefaultMaxPixels)
			switch fixture {
			case "corrupt.jpg":
				var se *SandboxError
				if !errors.As(err, &se) {
					t.Fatalf("corrupt: %v", err)
				}
				return
			case "bomb.png":
				var be *BudgetError
				if !errors.As(err, &be) {
					t.Fatalf("bomb: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(ds) != len(Variants) {
				t.Fatalf("missing variants: %v", ds)
			}
			for _, d := range ds {
				data, err := os.ReadFile(d.SrcPath)
				if err != nil || len(data) < 3 || data[0] != 0xff || strings.Contains(string(data), "Exif") {
					t.Fatalf("invalid/private metadata in %s: %v", d.Name, err)
				}
			}
			if fixture == "iphone-12mp.heic" && (ds[len(ds)-1].Width != 3024 || ds[len(ds)-1].Height != 4032) {
				t.Fatal("HEIC rotation changed")
			}
		})
	}
}
