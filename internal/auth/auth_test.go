package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/szatmary/agentbox/internal/config"
)

type fakeKeychain struct {
	val string
	err error
}

func (f fakeKeychain) Find(service string) (string, error) { return f.val, f.err }

type fakeGitHub struct {
	tok string
	err error
}

func (f fakeGitHub) Token(ctx context.Context) (string, error) { return f.tok, f.err }

func TestResolveClaude(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		env      MapEnv
		keychain Keychain
		wantErr  string
		check    func(t *testing.T, inj Injection)
	}{
		{
			name:     "keychain ok",
			source:   config.ClaudeKeychain,
			keychain: fakeKeychain{val: `{"oauth":"blob"}`},
			check: func(t *testing.T, inj Injection) {
				if inj.ClaudeCredentialsJSON != `{"oauth":"blob"}` {
					t.Errorf("creds = %q", inj.ClaudeCredentialsJSON)
				}
				if _, ok := inj.Env["ANTHROPIC_API_KEY"]; ok {
					t.Error("keychain path must not set ANTHROPIC_API_KEY")
				}
			},
		},
		{
			name:     "keychain empty",
			source:   config.ClaudeKeychain,
			keychain: fakeKeychain{val: ""},
			wantErr:  "empty",
		},
		{
			name:     "keychain error",
			source:   config.ClaudeKeychain,
			keychain: fakeKeychain{err: errors.New("locked")},
			wantErr:  "reading keychain",
		},
		{
			name:   "api_key ok",
			source: config.ClaudeAPIKey,
			env:    MapEnv{"ANTHROPIC_API_KEY": "sk-test"},
			check: func(t *testing.T, inj Injection) {
				if inj.Env["ANTHROPIC_API_KEY"] != "sk-test" {
					t.Errorf("env = %v", inj.Env)
				}
			},
		},
		{
			name:    "api_key missing",
			source:  config.ClaudeAPIKey,
			env:     MapEnv{},
			wantErr: "ANTHROPIC_API_KEY is not set",
		},
		{
			name:   "token ok",
			source: config.ClaudeToken,
			env:    MapEnv{"CLAUDE_CODE_OAUTH_TOKEN": "tok-123"},
			check: func(t *testing.T, inj Injection) {
				if inj.Env["CLAUDE_CODE_OAUTH_TOKEN"] != "tok-123" {
					t.Errorf("env = %v", inj.Env)
				}
			},
		},
		{
			name:    "token missing",
			source:  config.ClaudeToken,
			env:     MapEnv{},
			wantErr: "CLAUDE_CODE_OAUTH_TOKEN is not set",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Resolver{Env: tt.env, Keychain: tt.keychain, GitHub: fakeGitHub{tok: "x"}}
			inj, err := r.Resolve(context.Background(), config.Auth{Claude: tt.source, GitHub: config.GitHubNone})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if inj.ClaudeSource != tt.source {
				t.Errorf("ClaudeSource = %q, want %q", inj.ClaudeSource, tt.source)
			}
			if tt.check != nil {
				tt.check(t, inj)
			}
		})
	}
}

func TestResolveGitHub(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		env     MapEnv
		gh      GitHubTokener
		wantErr string
		wantTok string // expected GITHUB_TOKEN, "" means must be absent
	}{
		{"gh ok", config.GitHubGH, MapEnv{}, fakeGitHub{tok: "ghtok"}, "", "ghtok"},
		{"gh empty", config.GitHubGH, MapEnv{}, fakeGitHub{tok: ""}, "returned empty", ""},
		{"gh error", config.GitHubGH, MapEnv{}, fakeGitHub{err: errors.New("no login")}, "gh auth token", ""},
		{"pat from GITHUB_TOKEN", config.GitHubPAT, MapEnv{"GITHUB_TOKEN": "pat1"}, nil, "", "pat1"},
		{"pat from GH_TOKEN", config.GitHubPAT, MapEnv{"GH_TOKEN": "pat2"}, nil, "", "pat2"},
		{"pat missing", config.GitHubPAT, MapEnv{}, nil, "neither GITHUB_TOKEN nor GH_TOKEN", ""},
		{"none", config.GitHubNone, MapEnv{}, nil, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Resolver{Env: tt.env, Keychain: fakeKeychain{val: "blob"}, GitHub: tt.gh}
			inj, err := r.Resolve(context.Background(), config.Auth{Claude: config.ClaudeKeychain, GitHub: tt.source})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			got := inj.Env["GITHUB_TOKEN"]
			if got != tt.wantTok {
				t.Errorf("GITHUB_TOKEN = %q, want %q", got, tt.wantTok)
			}
			if tt.wantTok != "" && inj.Env["GH_TOKEN"] != tt.wantTok {
				t.Errorf("GH_TOKEN = %q, want %q", inj.Env["GH_TOKEN"], tt.wantTok)
			}
		})
	}
}

func TestResolveGitIdentity(t *testing.T) {
	r := Resolver{
		Env:      MapEnv{"GIT_USER_NAME": "Matthew Szatmary", "GIT_AUTHOR_EMAIL": "matt@szatmary.org"},
		Keychain: fakeKeychain{val: "blob"},
		GitHub:   fakeGitHub{tok: "x"},
	}
	inj, err := r.Resolve(context.Background(), config.Auth{Claude: config.ClaudeKeychain, GitHub: config.GitHubNone})
	if err != nil {
		t.Fatal(err)
	}
	if inj.GitName != "Matthew Szatmary" {
		t.Errorf("GitName = %q", inj.GitName)
	}
	if inj.GitEmail != "matt@szatmary.org" {
		t.Errorf("GitEmail = %q", inj.GitEmail)
	}
}

func TestResolveUnknownSources(t *testing.T) {
	r := Resolver{Env: MapEnv{}, Keychain: fakeKeychain{}, GitHub: fakeGitHub{}}
	if _, err := r.Resolve(context.Background(), config.Auth{Claude: "bogus", GitHub: config.GitHubNone}); err == nil {
		t.Error("expected error for unknown claude source")
	}
	if _, err := r.Resolve(context.Background(), config.Auth{Claude: config.ClaudeKeychain, GitHub: "bogus"}); err == nil {
		t.Error("expected error for unknown github source")
	}
}

// Verify the non-darwin stub keychain is wired so production resolution fails
// loudly on Linux rather than silently. (On darwin this exercises the real CLI
// path's error handling for a non-existent service.)
func TestSystemKeychainStub(t *testing.T) {
	_, err := SystemKeychain{}.Find("definitely-not-a-real-service-xyz")
	if err == nil {
		t.Error("expected error from SystemKeychain.Find for missing/unavailable service")
	}
}
