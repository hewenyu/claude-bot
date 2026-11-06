package main

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/hewenyu/claude-bot/internal/store"
)

// The page's shared settings: the TypeSafe key and the GitHub tokens, which
// every project uses.

// settingsView is the shared settings, masked.
type settingsView struct {
	TypeSafe secretView `json:"typesafe_key"`
	// GitHubTokens are the default token (named "") and the named ones,
	// with the projects each serves.
	GitHubTokens   []githubTokenView `json:"github_tokens"`
	GitHubProjects []githubProject   `json:"github_projects"` // the projects with a GitHub channel
}

func (w *web) settingsNow(ctx context.Context) (settingsView, error) {
	var v settingsView

	key, err := w.store.Setting(ctx, store.SettingTypeSafeKey)
	if err != nil {
		return v, err
	}

	if v.GitHubTokens, v.GitHubProjects, err = w.githubTokens(ctx); err != nil {
		return v, err
	}

	v.TypeSafe = maskSecret(key)

	return v, nil
}

func (w *web) settings(r *http.Request) (any, error) {
	return w.settingsNow(r.Context())
}

// settingsInput replaces the settings given; empty ones are kept.
type settingsInput struct {
	TypeSafeKey string `json:"typesafe_key"`
	GitHubToken string `json:"github_token"`
}

// putSettings checks every setting given before it saves any: a key that
// is turned away leaves the settings as they were.
func (w *web) putSettings(r *http.Request) (any, error) {
	var in settingsInput
	if err := decodeBody(r, &in); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(r.Context(), webTimeout)
	defer cancel()

	key := strings.TrimSpace(in.TypeSafeKey)
	if key != "" {
		if err := w.checkJev(ctx, key); err != nil {
			return nil, badRequestf("TypeSafe API key not accepted: %w", err)
		}
	}

	token := strings.TrimSpace(in.GitHubToken)
	if token != "" {
		if err := w.checkGitHubToken(ctx, token, ""); err != nil {
			return nil, badRequestf("GitHub token not accepted: %w", err)
		}
	}

	for k, v := range map[string]string{store.SettingTypeSafeKey: key, store.SettingGitHubToken: token} {
		if v == "" {
			continue
		}

		if err := w.store.SetSetting(ctx, k, v); err != nil {
			return nil, err
		}
	}

	return w.settingsNow(ctx)
}

// sortedKeys is m's keys in order.
func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
