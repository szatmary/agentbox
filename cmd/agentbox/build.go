package main

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/szatmary/agentbox/internal/config"
	"github.com/szatmary/agentbox/internal/container"
	"github.com/szatmary/agentbox/internal/embedfs"
)

func newBuildCmd(g *globalFlags) *cobra.Command {
	var tag, baseImage string
	var noCache bool
	cmd := &cobra.Command{
		Use:   "build [job.toml]",
		Short: "Build (or rebuild) the sandbox image from the embedded Dockerfile",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var extra []string
			if p := configArg(args); fileExists(p) {
				if cfg, err := config.Load(p); err == nil {
					extra = cfg.Image.ExtraPackages
				}
			}
			uid, gid, username := hostIdentity()
			df, err := embedfs.RenderDockerfile(embedfs.DockerfileData{
				BaseImage:     baseImage,
				HostUID:       &uid,
				HostGID:       &gid,
				Username:      username,
				ExtraPackages: extra,
			})
			if err != nil {
				return err
			}
			ctxDir, err := os.MkdirTemp("", "agentbox-build-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(ctxDir)
			dfPath := filepath.Join(ctxDir, "Dockerfile")
			if err := os.WriteFile(dfPath, []byte(df), 0o644); err != nil {
				return err
			}

			rt := container.NewCLIRuntime()
			fmt.Fprintf(cmd.OutOrStdout(), "Building %s (uid=%d gid=%d, %d extra packages)...\n",
				tag, uid, gid, len(extra))
			return rt.Build(cmd.Context(), container.BuildOptions{
				Tag:        tag,
				ContextDir: ctxDir,
				Dockerfile: dfPath,
				BuildArgs: map[string]string{
					"HOST_UID": strconv.Itoa(uid),
					"HOST_GID": strconv.Itoa(gid),
					"USERNAME": username,
				},
				NoCache: noCache,
			})
		},
	}
	cmd.Flags().StringVar(&tag, "tag", defaultImageTag, "image tag to build")
	cmd.Flags().StringVar(&baseImage, "base-image", "", "override the Dockerfile base image")
	cmd.Flags().BoolVar(&noCache, "no-cache", false, "build without layer cache")
	return cmd
}

// hostIdentity returns the host uid/gid/username for the image build args.
func hostIdentity() (uid, gid int, username string) {
	uid, gid = os.Getuid(), os.Getgid()
	if uid < 0 {
		uid = 1000 // non-unix fallback (keeps the build deterministic)
	}
	if gid < 0 {
		gid = 1000
	}
	username = "agent"
	if u, err := user.Current(); err == nil && u.Username != "" {
		username = sanitizeName(u.Username)
	}
	return uid, gid, username
}
