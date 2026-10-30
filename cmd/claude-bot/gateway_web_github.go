package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/hewenyu/claude-bot/internal/channel/github"
	"github.com/hewenyu/claude-bot/internal/config"
	"github.com/hewenyu/claude-bot/internal/store"
)

// GitHub tokens on the page. The default token serves every project that
// names no other; a project of another account names a token of its own.
// One token, the default, is the common case.

// githubTokenView is a GitHub token, masked, with the projects it serves.
type githubTokenView struct {
	Name     string     `json:"name"` // "" for the default token
	Token    secretView `json:"token"`
	Projects []string   `json:"projects"`
}

// githubProject is a project with a GitHub channel.
type githubProject struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Repo      string `json:"repo"`
	TokenName string `json:"token_name,omitempty"`

	proxy string
}

// githubProjects returns the projects with a GitHub channel.
func (w *web) githubProjects(ctx context.Context) ([]githubProject, error) {
	projects, err := w.g.store.Projects(ctx)
	if err != nil {
		return nil, err
	}

	out := []githubProject{}

	for _, p := range projects {
		chans, err := w.g.store.Channels(ctx, p.ID)
		if err != nil {
			return nil, err
		}

		for _, c := range chans {
			if c.Name != github.Name {
				continue
			}

			cfg, err := config.ChannelConfig(c.Name, c.Config)
			if err != nil {
				return nil, fmt.Errorf("project %s: %w", p.Name, err)
			}

			out = append(out, githubProject{ID: p.ID, Name: p.Name, Repo: cfg.GitHub.Repo, TokenName: cfg.GitHubTokenName, proxy: cfg.GitHub.Proxy})
		}
	}

	return out, nil
}

// githubTokens returns the default token and the named ones, and the
// projects with a GitHub channel.
func (w *web) githubTokens(ctx context.Context) ([]githubTokenView, []githubProject, error) {
	def, err := w.g.store.Setting(ctx, store.SettingGitHubToken)
	if err != nil {
		return nil, nil, err
	}

	named, err := config.GitHubTokens(ctx, w.g.store)
	if err != nil {
		return nil, nil, err
	}

	projects, err := w.githubProjects(ctx)
	if err != nil {
		return nil, nil, err
	}

	names := make([]string, 0, len(named))
	for name := range named {
		names = append(names, name)
	}

	slices.Sort(names)

	// A project naming a token the gateway lost still shows, under it.
	for _, p := range projects {
		if p.TokenName != "" && named[p.TokenName] == "" && !slices.Contains(names, p.TokenName) {
			names = append(names, p.TokenName)
		}
	}

	views := []githubTokenView{{Token: maskSecret(def), Projects: []string{}}}
	for _, name := range names {
		views = append(views, githubTokenView{Name: name, Token: maskSecret(named[name]), Projects: []string{}})
	}

	for _, p := range projects {
		i := slices.IndexFunc(views, func(v githubTokenView) bool { return v.Name == p.TokenName })
		views[i].Projects = append(views[i].Projects, p.Name)
	}

	return views, projects, nil
}

// checkGitHubAccess checks that token can read gh.Repo.
func checkGitHubAccess(ctx context.Context, token string, gh github.Config) error {
	gh.Token = token

	ch, err := github.New(gh, quiet)
	if err != nil {
		return err
	}

	_, err = ch.Account(ctx)

	return err
}

// checkGitHubToken checks a token against the repository of every project
// that uses the token called name.
func (w *web) checkGitHubToken(ctx context.Context, token, name string) error {
	projects, err := w.githubProjects(ctx)
	if err != nil {
		return err
	}

	for _, p := range projects {
		if p.TokenName != name {
			continue
		}

		if err := w.checkGitHub(ctx, token, github.Config{Repo: p.Repo, Proxy: p.proxy}); err != nil {
			return fmt.Errorf("%s (%s): %w", p.Name, p.Repo, err)
		}
	}

	return nil
}

// githubTokenInput adds or changes a named token: an empty token keeps the
// stored one. Projects are the ids of those it serves from now on; the
// projects it served and no longer does go back to the default token.
type githubTokenInput struct {
	Token    string  `json:"token"`
	Projects []int64 `json:"projects"`
}

func (w *web) putGitHubToken(rw http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := config.CheckTokenName(name); err != nil {
		w.fail(rw, fmt.Errorf("%w: %w", errBadRequest, err))

		return
	}

	var in githubTokenInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		w.fail(rw, fmt.Errorf("%w: %w", errBadRequest, err))

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), webTimeout)
	defer cancel()

	token, err := w.githubTokenToSave(ctx, name, strings.TrimSpace(in.Token), in.Projects)
	if err != nil {
		w.fail(rw, err)

		return
	}

	if err := w.g.store.SetSetting(ctx, store.SettingGitHubTokenPrefix+name, token); err != nil {
		w.fail(rw, err)

		return
	}

	if err := w.assignGitHubToken(ctx, name, in.Projects); err != nil {
		w.fail(rw, err)

		return
	}

	w.settings(rw, r)
}

// githubTokenToSave returns the token to save under name, after checking it
// against the repository of every project it is to serve.
func (w *web) githubTokenToSave(ctx context.Context, name, token string, serve []int64) (string, error) {
	if token == "" {
		stored, err := config.GitHubToken(ctx, w.g.store, name)
		if errors.Is(err, config.ErrNoGitHubToken) {
			return "", fmt.Errorf("%w: a new GitHub token needs its value", errBadRequest)
		}

		if err != nil {
			return "", err
		}

		token = stored
	}

	projects, err := w.githubProjects(ctx)
	if err != nil {
		return "", err
	}

	for _, id := range serve {
		i := slices.IndexFunc(projects, func(p githubProject) bool { return p.ID == id })
		if i < 0 {
			return "", fmt.Errorf("%w: project %d has no GitHub channel", errBadRequest, id)
		}

		p := projects[i]
		if err := w.checkGitHub(ctx, token, github.Config{Repo: p.Repo, Proxy: p.proxy}); err != nil {
			return "", fmt.Errorf("%w: GitHub token not accepted for %s (%s): %w", errBadRequest, p.Name, p.Repo, err)
		}
	}

	return token, nil
}

// assignGitHubToken has the projects in serve use the token called name,
// and those that used it and are not in serve the default token.
func (w *web) assignGitHubToken(ctx context.Context, name string, serve []int64) error {
	projects, err := w.githubProjects(ctx)
	if err != nil {
		return err
	}

	for _, p := range projects {
		want := p.TokenName

		switch {
		case slices.Contains(serve, p.ID):
			want = name
		case p.TokenName == name:
			want = ""
		}

		if want == p.TokenName {
			continue
		}

		if err := w.setGitHubTokenName(ctx, p.ID, want); err != nil {
			return fmt.Errorf("project %s: %w", p.Name, err)
		}
	}

	return nil
}

// setGitHubTokenName has a project's GitHub channel use the token called
// name.
func (w *web) setGitHubTokenName(ctx context.Context, project int64, name string) error {
	chans, err := w.g.store.Channels(ctx, project)
	if err != nil {
		return err
	}

	i := slices.IndexFunc(chans, func(c store.Channel) bool { return c.Name == github.Name })
	if i < 0 {
		return store.ErrNotFound
	}

	cfg, err := config.ChannelConfig(github.Name, chans[i].Config)
	if err != nil {
		return err
	}

	cfg.GitHubTokenName = name

	rows, err := cfg.Channels()
	if err != nil {
		return err
	}

	return w.g.store.PutChannel(ctx, project, rows[0])
}

// deleteGitHubToken removes a named token no project uses.
func (w *web) deleteGitHubToken(rw http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	projects, err := w.githubProjects(r.Context())
	if err != nil {
		w.fail(rw, err)

		return
	}

	var users []string

	for _, p := range projects {
		if p.TokenName == name {
			users = append(users, p.Name)
		}
	}

	if len(users) > 0 {
		apiError(rw, http.StatusConflict, fmt.Sprintf("GitHub token %q still serves %s: give them another token first", name, strings.Join(users, ", ")))

		return
	}

	if err := w.g.store.SetSetting(r.Context(), store.SettingGitHubTokenPrefix+name, ""); err != nil {
		w.fail(rw, err)

		return
	}

	w.settings(rw, r)
}

// channelTypes are the channels a project can add, with the choices it has:
// the GitHub tokens, the repository of its GitHub remote. A repository not
// on GitHub has nothing there to watch.
func (w *web) channelTypes(rw http.ResponseWriter, r *http.Request) {
	p, err := w.project(r)
	if err != nil {
		w.fail(rw, err)

		return
	}

	named, err := config.GitHubTokens(r.Context(), w.g.store)
	if err != nil {
		w.fail(rw, err)

		return
	}

	names := make([]string, 0, len(named))
	for name := range named {
		names = append(names, name)
	}

	slices.Sort(names)

	remote := githubRemote(r.Context(), p.Dir)
	out := make([]channelType, len(channelTypes))

	for i, t := range channelTypes {
		t.Fields = slices.Clone(t.Fields)

		if t.Name == github.Name {
			for j, f := range t.Fields {
				switch f.Key {
				case "repo":
					t.Fields[j].Default = remote
				case "token_name":
					t.Fields[j].Options = names
				}
			}

			if remote == "" {
				t.Unavailable = "仓库没有 GitHub 上的 remote"
			}
		}

		out[i] = t
	}

	writeAPI(rw, http.StatusOK, out)
}
