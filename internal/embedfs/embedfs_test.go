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
	if !strings.Contains(out, "ENTRYPOINT [\"sleep\"]") {
		t.Error("Dockerfile should keep the VM alive with sleep")
	}
	if !strings.Contains(out, "HOST_UID=1000") || !strings.Contains(out, "HOST_GID=1000") {
		t.Errorf("default uid/gid not applied:\n%s", out)
	}
}

func TestRenderDockerfileExtraPackages(t *testing.T) {
	out, err := RenderDockerfile(DockerfileData{
		HostUID:       501,
		HostGID:       20,
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
