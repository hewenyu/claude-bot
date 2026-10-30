package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hewenyu/claude-bot/internal/channel/dingtalk"
	"github.com/hewenyu/claude-bot/internal/channel/discord"
	"github.com/hewenyu/claude-bot/internal/channel/github"
	"github.com/hewenyu/claude-bot/internal/channel/lark"
	"github.com/hewenyu/claude-bot/internal/channel/slack"
	"github.com/hewenyu/claude-bot/internal/channel/telegram"
	"github.com/hewenyu/claude-bot/internal/jev"
	"github.com/hewenyu/claude-bot/internal/store"
)

// HomeEnv names the gateway's directory, ~/.claude-bot by default.
const HomeEnv = "CLAUDE_BOT_HOME"

// importedSuffix is added to config.json once the gateway holds its content.
const importedSuffix = ".imported"

// Home returns the gateway's directory: $CLAUDE_BOT_HOME, else
// ~/.claude-bot. It holds the database, the gateway's lock, heartbeat and log.
func Home() (string, error) {
	if h := os.Getenv(HomeEnv); h != "" {
		abs, err := filepath.Abs(h)
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", HomeEnv, err)
		}

		return abs, nil
	}

	u, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}

	return filepath.Join(u, DirName), nil
}

// OpenStore opens the gateway database, creating it if needed.
func OpenStore(ctx context.Context) (*store.Store, error) {
	home, err := Home()
	if err != nil {
		return nil, err
	}

	return store.Open(ctx, home)
}

// openGateway opens the gateway database if there is one.
func openGateway(ctx context.Context) (*store.Store, bool, error) {
	home, err := Home()
	if err != nil {
		return nil, false, err
	}

	return store.OpenExisting(ctx, home)
}

// FromStore returns the configuration of a registered project: its channels
// and the settings every project shares.
func FromStore(ctx context.Context, s *store.Store, p store.Project) (Config, error) {
	cfg := Config{Gateway: &p}

	chans, err := s.Channels(ctx, p.ID)
	if err != nil {
		return cfg, err
	}

	for _, c := range chans {
		if err := cfg.decode(c); err != nil {
			return cfg, fmt.Errorf("project %s: %w", p.Name, err)
		}
	}

	if cfg.GitHub != nil {
		if cfg.GitHub.Token, err = GitHubToken(ctx, s, cfg.GitHubTokenName); err != nil {
			return cfg, fmt.Errorf("project %s: %w", p.Name, err)
		}
	}

	key, err := s.Setting(ctx, store.SettingTypeSafeKey)
	if err != nil {
		return cfg, err
	}

	if key != "" {
		cfg.TypeSafe = &jev.Config{APIKey: key}
	}

	return cfg, nil
}

// ErrNoGitHubToken reports a GitHub token name the gateway does not have.
var ErrNoGitHubToken = errors.New("no such GitHub token in the gateway")

// GitHubToken returns the gateway's GitHub token called name, the default
// one for "". A missing default is "", for the channel to report; a missing
// named token is an error naming it.
func GitHubToken(ctx context.Context, s *store.Store, name string) (string, error) {
	if name == "" {
		return s.Setting(ctx, store.SettingGitHubToken)
	}

	token, err := s.Setting(ctx, store.SettingGitHubTokenPrefix+name)
	if err == nil && token == "" {
		err = fmt.Errorf("%w: %q", ErrNoGitHubToken, name)
	}

	return token, err
}

// GitHubTokens returns the gateway's named GitHub tokens by name.
func GitHubTokens(ctx context.Context, s *store.Store) (map[string]string, error) {
	return s.SettingsWithPrefix(ctx, store.SettingGitHubTokenPrefix)
}

var tokenName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,39}$`)

// CheckTokenName reports whether name can name a GitHub token: letters,
// digits, '_', '.' and '-', at most 40.
func CheckTokenName(name string) error {
	if !tokenName.MatchString(name) {
		return fmt.Errorf("GitHub token name %q: use up to 40 letters, digits, '_', '.' or '-'", name)
	}

	return nil
}

// storedGitHub is the GitHub channel as the gateway stores it: without the
// token, with the name of the one it uses.
type storedGitHub struct {
	github.Config

	TokenName string `json:"token_name,omitempty"`
}

// ChannelConfig returns a Config with only the channel name set, from its
// stored JSON.
func ChannelConfig(name string, raw json.RawMessage) (Config, error) {
	var c Config

	err := c.decode(store.Channel{Name: name, Config: raw})

	return c, err
}

func (c *Config) decode(ch store.Channel) error {
	var target any

	switch ch.Name {
	case lark.Name:
		c.Lark = new(lark.Config)
		target = c.Lark
	case discord.Name:
		c.Discord = new(discord.Config)
		target = c.Discord
	case telegram.Name:
		c.Telegram = new(telegram.Config)
		target = c.Telegram
	case slack.Name:
		c.Slack = new(slack.Config)
		target = c.Slack
	case github.Name:
		var gh storedGitHub
		if err := json.Unmarshal(ch.Config, &gh); err != nil {
			return fmt.Errorf("channel %s: %w", ch.Name, err)
		}

		c.GitHub, c.GitHubTokenName = &gh.Config, gh.TokenName

		return nil
	case dingtalk.Name:
		c.DingTalk = new(dingtalk.Config)
		target = c.DingTalk
	default:
		return fmt.Errorf("unknown channel %q: upgrade claude-bot", ch.Name)
	}

	if err := json.Unmarshal(ch.Config, target); err != nil {
		return fmt.Errorf("channel %s: %w", ch.Name, err)
	}

	return nil
}

// Channels returns the channels cfg sets up, as the gateway stores them. The
// GitHub token is left out: the gateway keeps it apart, for all the projects
// that use it.
func (c Config) Channels() ([]store.Channel, error) {
	var out []store.Channel

	add := func(name string, cfg any, key string) error {
		raw, err := json.Marshal(cfg)
		if err != nil {
			return fmt.Errorf("marshal %s config: %w", name, err)
		}

		out = append(out, store.Channel{Name: name, Config: raw, AppKey: key})

		return nil
	}

	var errs []error

	if c.Lark != nil {
		errs = append(errs, add(lark.Name, c.Lark, AppKey(lark.Name, c.Lark.AppID)))
	}

	if c.Discord != nil {
		errs = append(errs, add(discord.Name, c.Discord, AppKey(discord.Name, c.Discord.Token)))
	}

	if c.Telegram != nil {
		errs = append(errs, add(telegram.Name, c.Telegram, AppKey(telegram.Name, c.Telegram.Token)))
	}

	if c.Slack != nil {
		errs = append(errs, add(slack.Name, c.Slack, AppKey(slack.Name, c.Slack.AppToken)))
	}

	if c.GitHub != nil {
		gh := storedGitHub{Config: *c.GitHub, TokenName: c.GitHubTokenName}
		gh.Token = ""
		errs = append(errs, add(github.Name, gh, AppKey(github.Name, gh.Repo)))
	}

	if c.DingTalk != nil {
		errs = append(errs, add(dingtalk.Name, c.DingTalk, AppKey(dingtalk.Name, c.DingTalk.ClientID)))
	}

	return out, errors.Join(errs...)
}

// AppKey names the app a channel connects to, from the credential that
// identifies it: the Lark app id, the DingTalk client id, the bot id in a
// Telegram token, the bot user in a Discord token, the app id in a Slack app
// token, the GitHub repository. One app belongs to one project. The key
// never holds a secret; a credential it cannot parse is hashed.
func AppKey(name, id string) string {
	switch name {
	case telegram.Name:
		if bot, _, ok := strings.Cut(id, ":"); ok && bot != "" {
			return name + ":" + bot
		}
	case discord.Name:
		if user, _, ok := strings.Cut(id, "."); ok && user != "" {
			return name + ":" + user
		}
	case slack.Name:
		if parts := strings.Split(id, "-"); len(parts) >= 3 && parts[0] == "xapp" && parts[2] != "" {
			return name + ":" + parts[2]
		}
	case github.Name:
		return name + ":" + strings.ToLower(id)
	case lark.Name, dingtalk.Name:
		return name + ":" + id
	}

	sum := sha256.Sum256([]byte(id))

	return name + ":sha256-" + hex.EncodeToString(sum[:8])
}

// Save registers the project of dir with the gateway, if it is not yet, and
// stores cfg as its configuration; cfg's TypeSafe key becomes the one every
// project shares, and its GitHub token the one it names (the default token
// if it names none). A config.json left from before the
// gateway is then renamed config.json.imported: the gateway holds it now.
func Save(ctx context.Context, dir string, cfg Config) (store.Project, error) {
	s, err := OpenStore(ctx)
	if err != nil {
		return store.Project{}, err
	}
	defer s.Close() //nolint:errcheck // writes are committed

	return save(ctx, s, dir, cfg)
}

func save(ctx context.Context, s *store.Store, dir string, cfg Config) (store.Project, error) {
	chans, err := cfg.Channels()
	if err != nil {
		return store.Project{}, err
	}

	p, err := s.SaveProject(ctx, filepath.Dir(dir), chans)
	if err != nil {
		return p, err
	}

	if cfg.TypeSafe != nil && cfg.TypeSafe.APIKey != "" {
		if err := s.SetSetting(ctx, store.SettingTypeSafeKey, cfg.TypeSafe.APIKey); err != nil {
			return p, err
		}
	}

	if cfg.GitHub != nil && cfg.GitHub.Token != "" {
		key := store.SettingGitHubToken
		if cfg.GitHubTokenName != "" {
			key = store.SettingGitHubTokenPrefix + cfg.GitHubTokenName
		}

		if err := s.SetSetting(ctx, key, cfg.GitHub.Token); err != nil {
			return p, err
		}
	}

	file := filepath.Join(dir, configFile)
	if err := os.Rename(file, file+importedSuffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return p, fmt.Errorf("retire %s: %w", file, err)
	}

	return p, nil
}

// Imported is what Import moved into the gateway.
type Imported struct {
	Project  store.Project `json:"project"`
	Channels []string      `json:"channels"`
	// Kept lists the shared settings the project had its own value for
	// while the gateway already had another: the gateway's stays.
	Kept []string `json:"kept,omitempty"`
	// GitHubToken names the token the project's GitHub channel uses when
	// it is not the default one: its own, added under this name.
	GitHubToken string `json:"github_token,omitempty"`
	Backup      string `json:"backup"` // where config.json went
}

// ErrNothingToImport reports a project without a config.json.
var ErrNothingToImport = errors.New("no config.json to import")

// Import moves the config.json of the project in dir into the gateway and
// renames it config.json.imported. The gateway's TypeSafe key, if set, wins
// over the project's. A GitHub token becomes the default one if the gateway
// has none; one that differs from the default is added under the project's
// name, so that the project keeps its account. Importing again after
// restoring config.json overwrites the project's channels.
func Import(ctx context.Context, dir string) (Imported, error) {
	file := filepath.Join(dir, configFile)
	if _, err := os.Stat(file); errors.Is(err, fs.ErrNotExist) {
		return Imported{}, fmt.Errorf("%w in %s", ErrNothingToImport, dir)
	}

	cfg, err := ReadFile(dir)
	if err != nil {
		return Imported{}, err
	}

	s, err := OpenStore(ctx)
	if err != nil {
		return Imported{}, err
	}
	defer s.Close() //nolint:errcheck // writes are committed

	var kept []string

	if cfg.TypeSafe != nil {
		if shared, err := keepShared(ctx, s, store.SettingTypeSafeKey, cfg.TypeSafe.APIKey); err != nil {
			return Imported{}, err
		} else if shared != "" {
			cfg.TypeSafe.APIKey = shared

			kept = append(kept, "TypeSafe API key")
		}
	}

	if cfg.GitHub != nil {
		if cfg.GitHubTokenName, err = importGitHubToken(ctx, s, filepath.Base(filepath.Dir(dir)), cfg.GitHub.Token); err != nil {
			return Imported{}, err
		}
	}

	p, err := save(ctx, s, dir, cfg)
	if err != nil {
		return Imported{}, err
	}

	chans, _ := cfg.Channels()
	names := make([]string, len(chans))

	for i, c := range chans {
		names[i] = c.Name
	}

	return Imported{Project: p, Channels: names, Kept: kept, GitHubToken: cfg.GitHubTokenName, Backup: file + importedSuffix}, nil
}

// importGitHubToken returns the name to keep an imported project's GitHub
// token under: "" (the default) if the gateway has no default yet or it is
// that one, the name of a named token that is the same, else a new name
// from the project's directory.
func importGitHubToken(ctx context.Context, s *store.Store, project, token string) (string, error) {
	def, err := s.Setting(ctx, store.SettingGitHubToken)
	if err != nil || def == "" || def == token {
		return "", err
	}

	named, err := GitHubTokens(ctx, s)
	if err != nil {
		return "", err
	}

	for name, t := range named {
		if t == token {
			return name, nil
		}
	}

	base := strings.Trim(regexp.MustCompile(`[^A-Za-z0-9_.-]+`).ReplaceAllString(project, "-"), "-.")
	if base == "" || CheckTokenName(base) != nil {
		base = "project"
	}

	name := base
	for i := 2; named[name] != ""; i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}

	return name, nil
}

// keepShared returns the gateway's value of a shared setting when it is set
// and differs from own, "" otherwise.
func keepShared(ctx context.Context, s *store.Store, key, own string) (string, error) {
	shared, err := s.Setting(ctx, key)
	if err != nil || shared == own {
		return "", err
	}

	return shared, nil
}

// GatewayGitHubToken returns the gateway's GitHub token called name (the
// default one for ""), "" if the gateway has no such token.
func GatewayGitHubToken(ctx context.Context, name string) (string, error) {
	s, ok, err := openGateway(ctx)
	if !ok || err != nil {
		return "", err
	}
	defer s.Close() //nolint:errcheck // read-only use

	token, err := GitHubToken(ctx, s, name)
	if errors.Is(err, ErrNoGitHubToken) {
		return "", nil
	}

	return token, err
}
