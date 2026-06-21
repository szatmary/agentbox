package embedfs

import (
	"strings"
	"testing"
)

func TestRenderDockerfileDefaults(t *testing.T) {
	out, err := RenderDockerfile(DockerfileData{})
	if err != nil {
		t.Fatalf("RenderDockerfile: %v", err)
	}
	if !strings.Contains(out, "FROM "+DefaultBaseImage) {
		t.Errorf("missing default base image:\n%s", out)
	}
	if !strings.Contains(out, "claude-code") {
		t.Error("Dockerfile should install Claude Code")
	}
	// openssh-server/-client back SSH-over-exec attach (sshd -i + ssh-keygen).
	if !strings.Contains(out, "openssh-server") || !strings.Contains(out, "openssh-client") {
		t.Errorf("Dockerfile should install openssh-server/-client:\n%s", out)
	}
	if !strings.Contains(out, "ENTRYPOINT [\"sleep\"]") {
		t.Error("Dockerfile should keep the VM alive with sleep")
	}
	if !strings.Contains(out, "HOST_UID=1000") || !strings.Contains(out, "HOST_GID=1000") {
		t.Errorf("default uid/gid not applied:\n%s", out)
	}
}

func intPtr(i int) *int { return &i }

func TestRenderDockerfileExtraPackages(t *testing.T) {
	out, err := RenderDockerfile(DockerfileData{
		HostUID:       intPtr(501),
		HostGID:       intPtr(20),
		Username:      "matt",
		ExtraPackages: []string{"golang", "poppler-utils"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"HOST_UID=501", "HOST_GID=20", "golang", "poppler-utils", "matt"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered Dockerfile missing %q:\n%s", want, out)
		}
	}
}

// TestRenderDockerfileUIDZero verifies that an explicit uid/gid of 0 (root) is
// honored rather than rewritten to 1000 — the old setDefaults rewrote 0→1000,
// breaking root/CI bind-mount ownership. A nil pointer still defaults. See C4.
func TestRenderDockerfileUIDZero(t *testing.T) {
	out, err := RenderDockerfile(DockerfileData{
		HostUID:  intPtr(0),
		HostGID:  intPtr(0),
		Username: "root",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "HOST_UID=0") || !strings.Contains(out, "HOST_GID=0") {
		t.Errorf("explicit uid/gid 0 must pass through, got:\n%s", out)
	}
	if strings.Contains(out, "HOST_UID=1000") || strings.Contains(out, "HOST_GID=1000") {
		t.Errorf("uid/gid 0 must not be remapped to 1000:\n%s", out)
	}

	// nil (unset) still defaults to DefaultID.
	def, err := RenderDockerfile(DockerfileData{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(def, "HOST_UID=1000") {
		t.Errorf("unset uid should default to 1000:\n%s", def)
	}
}

func TestTaskTemplate(t *testing.T) {
	out, err := TaskTemplate()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "/work/control/STATUS") || !strings.Contains(out, "DONE") {
		t.Error("task template should document the STATUS contract")
	}
}

func TestConfigTemplate(t *testing.T) {
	out, err := ConfigTemplate("go2110")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `name = "go2110"`) {
		t.Errorf("config template missing name:\n%s", out)
	}
	if !strings.Contains(out, "[guards]") || !strings.Contains(out, "[autorun]") {
		t.Error("config template missing sections")
	}
	// Default name when empty.
	def, err := ConfigTemplate("")
	if err != nil || !strings.Contains(def, `name = "myjob"`) {
		t.Errorf("default name not applied: %v\n%s", err, def)
	}
}
