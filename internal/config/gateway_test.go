package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hewenyu/claude-bot/internal/channel/dingtalk"
	"github.com/hewenyu/claude-bot/internal/channel/discord"
	"github.com/hewenyu/claude-bot/internal/channel/github"
	"github.com/hewenyu/claude-bot/internal/channel/lark"
	"github.com/hewenyu/claude-bot/internal/channel/slack"
	"github.com/hewenyu/claude-bot/internal/channel/telegram"
	"github.com/hewenyu/claude-bot/internal/jev"
	"github.com/hewenyu/claude-bot/internal/store"
)

// projectDir returns <a new repository root>/.claude-bot.
func newProjectDir(t *testing.T, name string) string {
	t.Helper()

	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}

	return filepath.Join(root, DirName)
}

func clearEnv(t *testing.T) {
	t.Helper()

	for _, v := range []string{
		larkAppIDEnv, larkSecretEnv, larkDomainEnv, discordTokenEnv, telegramTokenEnv,
		slackAppTokenEnv, slackBotTokenEnv, dingtalkIDEnv, dingtalkSecretEnv,
	} {
		t.Setenv(v, "")
	}
}

func TestSave_registersAndShares(t *testing.T) { //nolint:paralleltest // sets the environment
	clearEnv(t)
	home := isolate(t)
	ctx := t.Context()

	api := newProjectDir(t, "api")
	web := newProjectDir(t, "web")

	p := must(Save(ctx, api, Config{
		Lark:     &lark.Config{AppID: "cli_api", AppSecret: "s1", Proxy: "direct"},
		GitHub:   &github.Config{Token: "ghp_shared", Repo: "o/api"},
		TypeSafe: &jev.Config{APIKey: "ts-shared"},
	}))(t)
	expect(t, p.Name == "api" && p.Dir == filepath.Dir(api), "registered %+v", p)

	// The second project gets the shared key and token without setting them.
	must(Save(ctx, web, Config{GitHub: &github.Config{Repo: "o/web"}, DingTalk: &dingtalk.Config{ClientID: "ding1", ClientSecret: "s2"}}))(t)

	got := must(Load(ctx, web))(t)
	expect(t, got.Gateway != nil && got.Gateway.Name == "web", "Load().Gateway = %+v, want project web", got.Gateway)
	expect(t, got.GitHub != nil && got.GitHub.Token == "ghp_shared" && got.GitHub.Repo == "o/web", "Load().GitHub = %+v, want the shared token on o/web", got.GitHub)
	expect(t, got.TypeSafe != nil && got.TypeSafe.APIKey == "ts-shared", "Load().TypeSafe = %+v, want the shared key", got.TypeSafe)
	expect(t, got.Lark == nil, "web got api's lark app: %+v", got.Lark)

	// The token is stored once, as a setting, not in each channel.
	s := must(store.Open(ctx, home))(t)
	defer s.Close() //nolint:errcheck // test

	for _, c := range must(s.Channels(ctx, p.ID))(t) {
		expect(t, !strings.Contains(string(c.Config), "ghp_shared"), "channel %s stores the GitHub token: %s", c.Name, c.Config)
	}
}

func TestSave_appBelongsToOneProject(t *testing.T) { //nolint:paralleltest // sets the environment
	clearEnv(t)
	isolate(t)
	ctx := t.Context()

	api := newProjectDir(t, "api")
	web := newProjectDir(t, "web")

	if _, err := Save(ctx, api, Config{Lark: &lark.Config{AppID: "cli_1", AppSecret: "s"}}); err != nil {
		t.Fatal(err)
	}

	_, err := Save(ctx, web, Config{Lark: &lark.Config{AppID: "cli_1", AppSecret: "s"}})

	var conflict *store.ConflictError
	if !errors.As(err, &conflict) || conflict.Project.Name != "api" {
		t.Fatalf("second project on the same app: %v, want a conflict naming api", err)
	}

	// The failed save registered nothing: web keeps reading its config.json.
	if got, err := Read(ctx, web); err != nil || got.Gateway != nil {
		t.Errorf("Read(web) = %+v, %v; want unregistered", got.Gateway, err)
	}
}

func TestImport(t *testing.T) { //nolint:paralleltest // sets the environment
	clearEnv(t)
	isolate(t)
	ctx := t.Context()

	api := newProjectDir(t, "api")
	web := newProjectDir(t, "web")

	legacy := Config{
		Lark:     &lark.Config{AppID: "cli_api", AppSecret: "s1", Domain: "feishu"},
		DingTalk: &dingtalk.Config{ClientID: "ding_api", ClientSecret: "s2"},
		GitHub:   &github.Config{Token: "ghp_api", Repo: "o/api"},
		TypeSafe: &jev.Config{APIKey: "ts-api"},
	}
	if err := WriteFile(api, legacy); err != nil {
		t.Fatal(err)
	}

	if err := WriteFile(web, Config{GitHub: &github.Config{Token: "ghp_web", Repo: "o/web"}, TypeSafe: &jev.Config{APIKey: "ts-web"}}); err != nil {
		t.Fatal(err)
	}

	// Unregistered, a project reads its config.json.
	got := must(Read(ctx, api))(t)
	expect(t, got.Gateway == nil && got.Lark != nil, "before import Read = %+v", got)

	imp := must(Import(ctx, api))(t)
	expect(t, strings.Join(imp.Channels, ",") == "lark,github,dingtalk" && len(imp.Kept) == 0, "Import(api) = %+v", imp)

	_, err := os.Stat(filepath.Join(api, "config.json"))
	expect(t, errors.Is(err, os.ErrNotExist), "config.json still there after import")

	_, err = os.Stat(imp.Backup)
	expect(t, err == nil, "no backup: %v", err)

	got = must(Read(ctx, api))(t)
	expect(t, got.Gateway != nil && *got.Lark == *legacy.Lark && *got.DingTalk == *legacy.DingTalk &&
		*got.GitHub == *legacy.GitHub && *got.TypeSafe == *legacy.TypeSafe, "after import Read = %+v", got)

	imp = must(Import(ctx, web))(t)
	expect(t, strings.Join(imp.Kept, ",") == "TypeSafe API key", "Import(web).Kept = %v", imp.Kept)

	got = must(Read(ctx, web))(t)
	expect(t, got.TypeSafe.APIKey == "ts-api", "web after import uses TypeSafe key %q, want the shared one", got.TypeSafe.APIKey)

	_, err = Import(ctx, web)
	expect(t, errors.Is(err, ErrNothingToImport), "importing again = %v, want ErrNothingToImport", err)
}

func TestImport_githubTokens(t *testing.T) { //nolint:paralleltest // sets the environment
	clearEnv(t)
	isolate(t)
	ctx := t.Context()

	imported := func(name, token string) (Imported, Config) {
		t.Helper()

		dir := newProjectDir(t, name)
		if err := WriteFile(dir, Config{GitHub: &github.Config{Token: token, Repo: "o/" + name}}); err != nil {
			t.Fatal(err)
		}

		return must(Import(ctx, dir))(t), must(Read(ctx, dir))(t)
	}

	// The first token becomes the default one.
	imp, api := imported("api", "ghp_api")
	expect(t, imp.GitHubToken == "" && api.GitHub.Token == "ghp_api", "api: %+v uses %q", imp, api.GitHub.Token)

	// A token of another account is kept under the project's name.
	imp, web := imported("web", "ghp_web")
	expect(t, imp.GitHubToken == "web" && web.GitHub.Token == "ghp_web" && web.GitHubTokenName == "web",
		"web: %+v uses %q (%q)", imp, web.GitHub.Token, web.GitHubTokenName)

	// A token the gateway has already is used by name, whichever it is.
	for name, want := range map[string]string{"site": "web", "docs": ""} {
		token := map[string]string{"web": "ghp_web", "": "ghp_api"}[want]
		imp, got := imported(name, token)
		expect(t, imp.GitHubToken == want && got.GitHub.Token == token, "%s: %+v uses %q", name, imp, got.GitHub.Token)
	}
}

// must returns v, failing the test if err is set: must(f())(t).
func must[T any](v T, err error) func(*testing.T) T {
	return func(t *testing.T) T {
		t.Helper()

		if err != nil {
			t.Fatal(err)
		}

		return v
	}
}

// expect reports a failed check without stopping the test.
func expect(t *testing.T, ok bool, format string, args ...any) {
	t.Helper()

	if !ok {
		t.Errorf(format, args...)
	}
}

func TestRead_unregisteredGetsSharedKey(t *testing.T) { //nolint:paralleltest // sets the environment
	clearEnv(t)
	isolate(t)
	ctx := t.Context()

	if _, err := Save(ctx, newProjectDir(t, "api"), Config{TypeSafe: &jev.Config{APIKey: "ts-shared"}}); err != nil {
		t.Fatal(err)
	}

	got, err := Read(ctx, newProjectDir(t, "new"))
	if err != nil || got.TypeSafe == nil || got.TypeSafe.APIKey != "ts-shared" || got.Gateway != nil {
		t.Errorf("Read(new project) = %+v, %v; want the shared key, unregistered", got, err)
	}
}

func TestRead_noGatewayCreatesNothing(t *testing.T) { //nolint:paralleltest // sets the environment
	clearEnv(t)
	home := isolate(t)

	if _, err := Load(t.Context(), newProjectDir(t, "api")); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Load created the gateway home: %v", err)
	}
}

func TestAppKey(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, id, want string }{
		{lark.Name, "cli_a1", "lark:cli_a1"},
		{dingtalk.Name, "dingabc", "dingtalk:dingabc"},
		{telegram.Name, "123456:AAE-secret", "telegram:123456"},
		{discord.Name, "MTIzNDU2.Gx.secret", "discord:MTIzNDU2"},
		{slack.Name, "xapp-1-A0123-456-secret", "slack:A0123"},
		{github.Name, "Hewenyu/Claude-Bot", "github:hewenyu/claude-bot"},
	} {
		if got := AppKey(tc.name, tc.id); got != tc.want {
			t.Errorf("AppKey(%s, %s) = %s, want %s", tc.name, tc.id, got, tc.want)
		}
	}

	// An unparsable credential is hashed, never stored as is.
	if got := AppKey(telegram.Name, "garbage-secret"); strings.Contains(got, "garbage") || !strings.HasPrefix(got, "telegram:sha256-") {
		t.Errorf("AppKey(unparsable) = %s", got)
	}

	if AppKey(discord.Name, "a") == AppKey(discord.Name, "b") {
		t.Error("different credentials share a key")
	}
}
