package processor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Integration tests for the codec sandbox and pipeline against the real
// pinned runtime image and the self-authored corpus. Skipped when the
// image is absent so unit-only environments stay green; CI and the T14
// gate run them for real.

const testImage = "canopy-media:r2"

func requireMediaImage(t *testing.T) {
	t.Helper()
	if out, err := exec.Command("docker", "image", "inspect", testImage).CombinedOutput(); err != nil {
		t.Skipf("media image %s not present (build via make media-image): %s", testImage, strings.TrimSpace(string(out)))
	}
}

func newTestSandbox() *DockerSandbox {
	return &DockerSandbox{Image: testImage, Memory: "512m", PIDs: 64}
}

// corpusWorkDir copies one fixture into a fresh work dir under a
// server-chosen name (never the fixture's own name).
func corpusWorkDir(t *testing.T, fixture string) (dir, srcName string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("../../../testdata/media-corpus", fixture))
	if err != nil {
		t.Fatalf("read corpus %s: %v", fixture, err)
	}
	ext := strings.TrimPrefix(filepath.Ext(fixture), ".")
	dir = t.TempDir()
	srcName = "in." + ext
	if err := os.WriteFile(filepath.Join(dir, srcName), b, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, srcName
}

func shaOf(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// TestPipelineNormalizesIPhoneHEIC is the iPhone-shaped acceptance case:
// orientation applied (portrait output), responsive derivatives, and
// every output free of EXIF/GPS. It also records the 12MP performance
// baseline and pins retry determinism: two runs produce byte-identical
// variants.
func TestPipelineNormalizesIPhoneHEIC(t *testing.T) {
	requireMediaImage(t)
	sb := newTestSandbox()
	dir, src := corpusWorkDir(t, "iphone-12mp.heic")

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	start := time.Now()
	ds, err := Run(ctx, sb, dir, src, 6, DefaultMaxPixels)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	t.Logf("12MP HEIC baseline: %v", elapsed.Round(time.Millisecond))
	if elapsed > 120*time.Second {
		t.Errorf("12MP processing took %v, over 120s baseline", elapsed)
	}
	if len(ds) != len(Variants) {
		t.Fatalf("got %d derivatives, want %d", len(ds), len(Variants))
	}
	byName := map[string]Derivative{}
	for _, d := range ds {
		byName[d.Name] = d
	}

	// orientation 6 on a 4032x3024 stored landscape => every derivative
	// must be portrait (height > width): the phone-photo correctness case
	for _, name := range []string{"thumb", "medium", "display", "original"} {
		d, ok := byName[name]
		if !ok {
			t.Fatalf("missing variant %s", name)
		}
		if d.Height <= d.Width {
			t.Errorf("%s is %dx%d, want portrait (orientation not applied)", name, d.Width, d.Height)
		}
		b, err := os.ReadFile(d.SrcPath)
		if err != nil {
			t.Fatal(err)
		}
		if len(b) < 3 || b[0] != 0xff || b[1] != 0xd8 || b[2] != 0xff {
			t.Errorf("%s is not a JPEG", name)
		}
		if strings.Contains(string(b), "Exif") {
			t.Errorf("%s still carries EXIF (GPS could survive)", name)
		}
	}
	if byName["thumb"].Width != 240 || byName["thumb"].Height != 320 {
		t.Errorf("thumb = %dx%d, want 240x320", byName["thumb"].Width, byName["thumb"].Height)
	}
	if byName["display"].Height != 2048 {
		t.Errorf("display height = %d, want 2048", byName["display"].Height)
	}
	if byName["original"].Width != 3024 || byName["original"].Height != 4032 {
		t.Errorf("original = %dx%d, want 3024x4032", byName["original"].Width, byName["original"].Height)
	}

	// determinism: a retried job must produce byte-identical variants
	dir2, src2 := corpusWorkDir(t, "iphone-12mp.heic")
	ds2, err := Run(ctx, sb, dir2, src2, 6, DefaultMaxPixels)
	if err != nil {
		t.Fatalf("pipeline rerun: %v", err)
	}
	if len(ds2) != len(ds) {
		t.Fatalf("rerun produced %d variants", len(ds2))
	}
	byName2 := map[string]Derivative{}
	for _, d := range ds2 {
		byName2[d.Name] = d
	}
	for _, v := range Variants {
		if shaOf(t, byName[v.Name].SrcPath) != shaOf(t, byName2[v.Name].SrcPath) {
			t.Errorf("variant %s not byte-identical across runs", v.Name)
		}
	}
}

func TestPipelineCorruptInput(t *testing.T) {
	requireMediaImage(t)
	sb := newTestSandbox()
	dir, src := corpusWorkDir(t, "corrupt.jpg")
	_, err := Run(context.Background(), sb, dir, src, 1, DefaultMaxPixels)
	var se *SandboxError
	if !errors.As(err, &se) {
		t.Fatalf("got %v, want *SandboxError", err)
	}
}

// TestPipelineDecodesWebP covers the fourth decode format (JPEG/PNG/
// WebP/HEIC): strict webpload, no rotation, landscape preserved.
func TestPipelineDecodesWebP(t *testing.T) {
	requireMediaImage(t)
	sb := newTestSandbox()
	dir, src := corpusWorkDir(t, "photo-640.webp")
	ds, err := Run(context.Background(), sb, dir, src, 1, DefaultMaxPixels)
	if err != nil {
		t.Fatalf("webp pipeline: %v", err)
	}
	if len(ds) != len(Variants) {
		t.Fatalf("got %d derivatives, want %d", len(ds), len(Variants))
	}
	for _, d := range ds {
		if d.Width <= d.Height {
			t.Errorf("%s is %dx%d, want landscape preserved", d.Name, d.Width, d.Height)
		}
		b, err := os.ReadFile(d.SrcPath)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "Exif") {
			t.Errorf("%s still carries EXIF", d.Name)
		}
	}
}

// TestPipelineBombRejectedPreDecode: the 12000x12000 PNG (144MP) must be
// refused from the header alone — fast, and with no derivative files
// written (nothing decoded).
func TestPipelineBombRejectedPreDecode(t *testing.T) {
	requireMediaImage(t)
	sb := newTestSandbox()
	dir, src := corpusWorkDir(t, "bomb.png")
	start := time.Now()
	_, err := Run(context.Background(), sb, dir, src, 1, DefaultMaxPixels)
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Errorf("bomb rejection took %v; budget check must be pre-decode", elapsed)
	}
	var be *BudgetError
	if !errors.As(err, &be) {
		t.Fatalf("got %v, want *BudgetError", err)
	}
	if be.W != 12000 || be.H != 12000 {
		t.Errorf("budget error dims %dx%d, want 12000x12000", be.W, be.H)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "out-*.jpg")); len(left) != 0 {
		t.Errorf("no derivatives may exist after a pre-decode refusal, found %v", left)
	}
}

// TestSandboxTimeoutKillsContainer: the wall-clock deadline must kill
// the container, not just the local docker client — no zombie codec
// keeps burning CPU after the job gives up.
func TestSandboxTimeoutKillsContainer(t *testing.T) {
	requireMediaImage(t)
	// Unique prefix: go test runs packages concurrently and other
	// suites spawn canopy-media-* containers; a bare-prefix filter
	// would race with theirs.
	sb := newTestSandbox()
	sb.NamePrefix = fmt.Sprintf("canopy-media-t%04d-", time.Now().UnixNano()%10000)
	dir, src := corpusWorkDir(t, "iphone-12mp.heic")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := Run(ctx, sb, dir, src, 6, DefaultMaxPixels)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want context.DeadlineExceeded", err)
	}
	out, err := exec.Command("docker", "ps", "-a", "--filter", "name="+sb.NamePrefix, "-q").Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("leftover sandbox containers after timeout kill: %s", out)
	}
}

// TestSandboxLimits pins the three hostile-environment guards from the
// packet: no network, bounded memory (OOM kill, not swap escape), and a
// pids cap that stops fork bombs. The bash argv here are fixed,
// server-authored test probes — production argv is never a shell.
func TestSandboxLimits(t *testing.T) {
	requireMediaImage(t)
	dir := t.TempDir()
	ctx := context.Background()

	t.Run("network denied", func(t *testing.T) {
		var se *SandboxError
		_, err := newTestSandbox().Run(ctx, dir, "getent", "hosts", "example.com")
		if !errors.As(err, &se) {
			t.Fatalf("DNS from a no-network sandbox: got %v, want non-zero exit", err)
		}
	})

	t.Run("memory capped", func(t *testing.T) {
		sb := &DockerSandbox{Image: testImage, Memory: "128m", PIDs: 64}
		var se *SandboxError
		_, err := sb.Run(ctx, dir, "bash", "-c", `x=$(head -c 300000000 /dev/urandom | base64); echo "${#x}"`)
		if !errors.As(err, &se) {
			t.Fatalf("400MB allocation in a 128m sandbox: got %v, want OOM kill", err)
		}
		if se.Exit != 137 {
			t.Errorf("exit = %d, want 137 (cgroup OOM kill)", se.Exit)
		}
	})

	t.Run("pids capped", func(t *testing.T) {
		sb := &DockerSandbox{Image: testImage, Memory: "128m", PIDs: 4}
		var se *SandboxError
		_, err := sb.Run(ctx, dir, "bash", "-ec", "for i in $(seq 1 50); do sleep 30 & done")
		if !errors.As(err, &se) {
			t.Fatalf("fork bomb under pids-limit 4: got %v, want non-zero exit", err)
		}
	})
}
