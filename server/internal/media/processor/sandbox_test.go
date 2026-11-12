package processor

import (
	"slices"
	"strings"
	"testing"
)

// TestDockerArgs pins the isolation contract and the argument order:
// flags, then the pinned image, then the codec argv. If the image were
// missing, docker would silently treat argv[0] as an image name and try
// to pull it from the network — the exact failure this test caught.
func TestDockerArgs(t *testing.T) {
	sb := &DockerSandbox{Image: "canopy-media:r2", Memory: "512m", PIDs: 64}
	args := sb.dockerArgs("canopy-media-test", "/tmp/wd", []string{"vipsheader", "-a", "/work/in.jpg"})

	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--network=none",
		"--memory 512m",
		"--memory-swap 512m",
		"--pids-limit 64",
		"--user ",
		"-v /tmp/wd:/work",
		"--rm",
		"--name canopy-media-test",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("docker args %q missing %q", joined, want)
		}
	}
	i := slices.Index(args, "canopy-media:r2")
	if i < 0 {
		t.Fatal("image missing from docker args")
	}
	if args[i+1] != "vipsheader" || args[i+2] != "-a" {
		t.Errorf("argv must follow the image directly, got %v", args[i:])
	}
	for _, a := range args {
		if a == "bash" || a == "sh" {
			t.Errorf("production sandbox argv must not invoke a shell: %v", args)
		}
	}
}
