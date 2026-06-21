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

// DefaultID is the uid/gid used when HostUID/HostGID are left unset (nil). Note
// that 0 is a real, valid value (root): a nil pointer means "unset", not zero,
// so an explicit uid/gid of 0 passes through unchanged (preserving root/CI
// bind-mount ownership). See C4.
const DefaultID = 1000

// DockerfileData parameterizes the sandbox Dockerfile template.
type DockerfileData struct {
	// BaseImage is the FROM image; defaults to DefaultBaseImage.
	BaseImage string
	// HostUID/HostGID match the host user so bind mounts are owned correctly.
	// A nil pointer means "unset" and defaults to DefaultID; an explicit 0 is
	// honored (root) rather than being rewritten.
	HostUID, HostGID *int
	// Username is the in-image user name.
	Username string
	// ExtraPackages are additional apt packages to install.
	ExtraPackages []string
}

// dockerfileView is the resolved (pointer-free) data handed to the template.
type dockerfileView struct {
	BaseImage        string
	HostUID, HostGID int
	Username         string
	ExtraPackages    []string
}

// RenderDockerfile renders the embedded Dockerfile template with data.
func RenderDockerfile(data DockerfileData) (string, error) {
	view := dockerfileView{
		BaseImage:     data.BaseImage,
		HostUID:       DefaultID,
		HostGID:       DefaultID,
		Username:      data.Username,
		ExtraPackages: data.ExtraPackages,
	}
	if view.BaseImage == "" {
		view.BaseImage = DefaultBaseImage
	}
	if view.Username == "" {
		view.Username = "agent"
	}
	if data.HostUID != nil {
		view.HostUID = *data.HostUID
	}
	if data.HostGID != nil {
		view.HostGID = *data.HostGID
	}
	return render("templates/Dockerfile.tmpl", view)
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
