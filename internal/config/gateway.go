package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/hewenyu/claude-bot/internal/atomicfile"
	"github.com/hewenyu/claude-bot/internal/channel/github"
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

// EnsureHome returns the gateway home, creating it, private to the user,
// if needed.
func EnsureHome() (string, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(home, atomicfile.PrivateDir); err != nil {
		return "", fmt.Errorf("create %s: %w", home, err)
	}

	return home, nil
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
	var cfg Config

	chans, err := s.Channels(ctx, p.ID)
	if err != nil {
		return cfg, err
	}

	for _, c := range chans {
		if err := cfg.decodeChannel(c.Name, c.Config); err != nil {
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

// githubTokenKey is the setting that holds the GitHub token called name,
// the default one for "".
func githubTokenKey(name string) string {
	if name == "" {
		return store.SettingGitHubToken
	}

	return store.SettingGitHubTokenPrefix + name
}

// GitHubToken returns the gateway's GitHub token called name, the default
// one for "". A missing default is "", for the channel to report; a missing
// named token is an error naming it.
func GitHubToken(ctx context.Context, s *store.Store, name string) (string, error) {
	token, err := s.Setting(ctx, githubTokenKey(name))
	if err == nil && token == "" && name != "" {
		err = fmt.Errorf("%w: %q", ErrNoGitHubToken, name)
	}

	return token, err
}

// GitHubTokens returns the gateway's named GitHub tokens by name.
func GitHubTokens(ctx context.Context, s *store.Store) (map[string]string, error) {
	return s.SettingsWithPrefix(ctx, store.SettingGitHubTokenPrefix)
}

// SetGitHubToken saves the gateway's GitHub token called name, the default
// one for "".
func SetGitHubToken(ctx context.Context, s *store.Store, name, token string) error {
	if token == "" {
		return errors.New("empty GitHub token: use DeleteGitHubToken")
	}

	return s.SetSetting(ctx, githubTokenKey(name), token)
}

// DeleteGitHubToken removes the gateway's GitHub token called name.
func DeleteGitHubToken(ctx context.Context, s *store.Store, name string) error {
	return s.SetSetting(ctx, githubTokenKey(name), "")
}

// UseGitHubToken has the GitHub channel of a project use the gateway's
// token called name, the default one for "".
func UseGitHubToken(ctx context.Context, s *store.Store, project int64, name string) error {
	chans, err := s.Channels(ctx, project)
	if err != nil {
		return err
	}

	i := slices.IndexFunc(chans, func(c store.Channel) bool { return c.Name == github.Name })
	if i < 0 {
		return fmt.Errorf("%w: %d has no GitHub channel", store.ErrNotFound, project)
	}

	cfg, err := ChannelConfig(github.Name, chans[i].Config)
	if err != nil {
		return err
	}

	cfg.GitHubTokenName = name

	rows, err := cfg.Channels()
	if err != nil {
		return err
	}

	return s.PutChannel(ctx, project, rows[0])
}

var tokenName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,39}$`)

// notInTokenName is what a token name made from a project's directory
// leaves out.
var notInTokenName = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

// CheckTokenName reports whether name can name a GitHub token: letters,
// digits, '_', '.' and '-', at most 40.
func CheckTokenName(name string) error {
	if !tokenName.MatchString(name) {
		return fmt.Errorf("GitHub token name %q: use up to 40 letters, digits, '_', '.' or '-'", name)
	}

	return nil
}

// ChannelConfig returns a Config with only the channel name set, from its
// stored JSON.
func ChannelConfig(name string, raw json.RawMessage) (Config, error) {
	var c Config

	err := c.decodeChannel(name, raw)

	return c, err
}

// Channels returns the channels cfg sets up, as the gateway stores them. The
// GitHub token is left out: the gateway keeps it apart, for all the projects
// that use it.
func (c Config) Channels() ([]store.Channel, error) {
	var (
		out  []store.Channel
		errs []error
	)

	for _, k := range kinds {
		if !k.has(c) {
			continue
		}

		raw, key, err := k.encode(c)
		if err != nil {
			errs = append(errs, fmt.Errorf("marshal %s config: %w", k.Name, err))

			continue
		}

		out = append(out, store.Channel{Name: k.Name, Config: raw, AppKey: key})
	}

	return out, errors.Join(errs...)
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
		if err := SetGitHubToken(ctx, s, cfg.GitHubTokenName, cfg.GitHub.Token); err != nil {
			return p, err
		}
	}

	file := FilePath(dir)
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
	file := FilePath(dir)
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

	base := strings.Trim(notInTokenName.ReplaceAllString(project, "-"), "-.")
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

// Unregister removes a project from the gateway after writing its
// configuration back to its config.json, so that it can still run on its
// own, and returns that file; "" if the project's directory is gone, with
// nothing to write back to.
func Unregister(ctx context.Context, s *store.Store, p store.Project) (string, error) {
	cfg, err := FromStore(ctx, s, p)
	if err != nil {
		return "", err
	}

	dir := BotDir(p.Dir)
	path := FilePath(dir)

	_, err = os.Stat(p.Dir)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		path = ""
	case err != nil:
		return "", fmt.Errorf("unregister %s: %w", p.Name, err)
	default:
		if err := WriteFile(dir, cfg); err != nil {
			return "", err
		}
	}

	if err := s.RemoveProject(ctx, p.ID); err != nil {
		return "", err
	}

	return path, nil
}
