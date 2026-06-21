// Package auth resolves the credentials agentbox injects into a sandbox: a
// Claude credential, an optional GitHub token, and a git identity.
//
// All external touchpoints (environment, macOS keychain, the `gh` CLI) sit
// behind interfaces so resolution is unit-tested with fakes and never reads a
// real secret during `go test`. Injection is one-way: credential *values* are
// carried in an [Injection] for the runtime to push into the sandbox and are
// never logged. Only the *source* of each credential is reported.
package auth

import (
	"context"
	"fmt"

	"github.com/szatmary/agentbox/internal/config"
)

// KeychainService is the macOS keychain item holding Claude's OAuth credentials.
const KeychainService = "Claude Code-credentials"

// Env reads environment variables. The production implementation is [OSEnv];
// tests use [MapEnv].
type Env interface {
	Getenv(key string) string
}

// Keychain reads a generic-password value from the macOS keychain. The
// production implementation ([SystemKeychain]) is darwin-only; on other
// platforms it returns a clear "macOS only" error.
type Keychain interface {
	Find(service string) (string, error)
}

// GitHubTokener returns a GitHub token (e.g. from `gh auth token`).
type GitHubTokener interface {
	Token(ctx context.Context) (string, error)
}

// Injection is the resolved set of secrets and identity to push into a sandbox.
// Its Env values and ClaudeCredentialsJSON are secret and must never be logged.
type Injection struct {
	// Env are secret environment variables to set in the sandbox.
	Env map[string]string
	// ClaudeCredentialsJSON, when non-empty, is the keychain OAuth blob to write
	// to ~/.claude/.credentials.json inside the sandbox.
	ClaudeCredentialsJSON string
	// GitName/GitEmail configure git inside the sandbox (not secret).
	GitName, GitEmail string
	// ClaudeSource/GitHubSource record which source satisfied each credential,
	// for non-secret reporting (e.g. by `agentbox doctor`).
	ClaudeSource, GitHubSource string
}

// Resolver resolves credentials from its injected dependencies.
type Resolver struct {
	Env      Env
	Keychain Keychain
	GitHub   GitHubTokener
}

// NewResolver returns a Resolver wired to the host environment, keychain, and
// `gh` CLI.
func NewResolver() Resolver {
	return Resolver{
		Env:      OSEnv{},
		Keychain: SystemKeychain{},
		GitHub:   GHCLI{},
	}
}

// Resolve produces the full Injection for the given auth config. It fails with
// an actionable error if a required credential cannot be found.
func (r Resolver) Resolve(ctx context.Context, a config.Auth) (Injection, error) {
	inj := Injection{Env: map[string]string{}}

	if err := r.resolveClaude(a.Claude, &inj); err != nil {
		return Injection{}, err
	}
	if err := r.resolveGitHub(ctx, a.GitHub, &inj); err != nil {
		return Injection{}, err
	}
	inj.GitName, inj.GitEmail = r.gitIdentity()
	return inj, nil
}

func (r Resolver) resolveClaude(source string, inj *Injection) error {
	inj.ClaudeSource = source
	switch source {
	case config.ClaudeKeychain:
		if r.Keychain == nil {
			return fmt.Errorf("auth: claude=keychain but no keychain available")
		}
		blob, err := r.Keychain.Find(KeychainService)
		if err != nil {
			return fmt.Errorf("auth: reading keychain item %q: %w", KeychainService, err)
		}
		if blob == "" {
			return fmt.Errorf("auth: keychain item %q is empty; run `claude` once to sign in", KeychainService)
		}
		inj.ClaudeCredentialsJSON = blob
		return nil
	case config.ClaudeAPIKey:
		key := r.getenv("ANTHROPIC_API_KEY")
		if key == "" {
			return fmt.Errorf("auth: claude=api_key but ANTHROPIC_API_KEY is not set")
		}
		inj.Env["ANTHROPIC_API_KEY"] = key
		return nil
	case config.ClaudeToken:
		tok := r.getenv("CLAUDE_CODE_OAUTH_TOKEN")
		if tok == "" {
			return fmt.Errorf("auth: claude=token but CLAUDE_CODE_OAUTH_TOKEN is not set")
		}
		inj.Env["CLAUDE_CODE_OAUTH_TOKEN"] = tok
		return nil
	default:
		return fmt.Errorf("auth: unknown claude source %q", source)
	}
}

func (r Resolver) resolveGitHub(ctx context.Context, source string, inj *Injection) error {
	inj.GitHubSource = source
	switch source {
	case config.GitHubGH:
		if r.GitHub == nil {
			return fmt.Errorf("auth: github=gh but no gh client available")
		}
		tok, err := r.GitHub.Token(ctx)
		if err != nil {
			return fmt.Errorf("auth: `gh auth token`: %w", err)
		}
		if tok == "" {
			return fmt.Errorf("auth: `gh auth token` returned empty; run `gh auth login`")
		}
		setGitHubToken(inj, tok)
		return nil
	case config.GitHubPAT:
		tok := r.getenv("GITHUB_TOKEN")
		if tok == "" {
			tok = r.getenv("GH_TOKEN")
		}
		if tok == "" {
			return fmt.Errorf("auth: github=pat but neither GITHUB_TOKEN nor GH_TOKEN is set")
		}
		setGitHubToken(inj, tok)
		return nil
	case config.GitHubNone:
		return nil
	default:
		return fmt.Errorf("auth: unknown github source %q", source)
	}
}

func setGitHubToken(inj *Injection, tok string) {
	inj.Env["GITHUB_TOKEN"] = tok
	inj.Env["GH_TOKEN"] = tok
}

// gitIdentity reads the git author identity from the environment, preferring
// agentbox's own GIT_USER_* vars, then the standard git vars. Missing values
// are returned empty (non-fatal).
func (r Resolver) gitIdentity() (name, email string) {
	name = firstNonEmpty(r.getenv("GIT_USER_NAME"), r.getenv("GIT_AUTHOR_NAME"), r.getenv("GIT_COMMITTER_NAME"))
	email = firstNonEmpty(r.getenv("GIT_USER_EMAIL"), r.getenv("GIT_AUTHOR_EMAIL"), r.getenv("GIT_COMMITTER_EMAIL"))
	return name, email
}

func (r Resolver) getenv(key string) string {
	if r.Env == nil {
		return ""
	}
	return r.Env.Getenv(key)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
