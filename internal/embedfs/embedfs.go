// Package embedfs holds agentbox's embedded templates: the default sandbox
// Dockerfile and the scaffolding files written by `agentbox init`.
package embedfs

import (
	"bytes"
	"embed"
	"text/template"
)

//go:embed templates/Dockerfile.tmpl templates/task.md.tmpl templates/agentbox.toml.tmpl
var files embed.FS

// DefaultBaseImage is the base image the sandbox Dockerfile builds upon.
const DefaultBaseImage = "debian:bookworm-slim"

// DockerfileData parameterizes the sandbox Dockerfile template.
type DockerfileData struct {
	// BaseImage is the FROM image; defaults to DefaultBaseImage.
	BaseImage string
	// HostUID/HostGID match the host user so bind mounts are owned correctly.
	HostUID, HostGID int
	// Username is the in-image user name.
	Username string
	// ExtraPackages are additional apt packages to install.
	ExtraPackages []string
}

func (d *DockerfileData) setDefaults() {
	if d.BaseImage == "" {
		d.BaseImage = DefaultBaseImage
	}
	if d.Username == "" {
		d.Username = "agent"
	}
	if d.HostUID == 0 {
		d.HostUID = 1000
	}
	if d.HostGID == 0 {
		d.HostGID = 1000
	}
}

// RenderDockerfile renders the embedded Dockerfile template with data.
func RenderDockerfile(data DockerfileData) (string, error) {
	data.setDefaults()
	return render("templates/Dockerfile.tmpl", data)
}

// TaskTemplate returns the raw `task.md` scaffold written by `agentbox init`.
func TaskTemplate() (string, error) {
	return readFile("templates/task.md.tmpl")
}

// ConfigTemplate renders the `agentbox.toml` scaffold for the given job name.
func ConfigTemplate(name string) (string, error) {
	if name == "" {
		name = "myjob"
	}
	return render("templates/agentbox.toml.tmpl", struct{ Name string }{Name: name})
}

func render(name string, data any) (string, error) {
	raw, err := files.ReadFile(name)
	if err != nil {
		return "", err
	}
	tmpl, err := template.New(name).Parse(string(raw))
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func readFile(name string) (string, error) {
	b, err := files.ReadFile(name)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
