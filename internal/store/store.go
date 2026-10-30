// Package store is the gateway's configuration database: one SQLite file,
// ~/.claude-bot/gateway.db, holding the settings every project shares (the
// TypeSafe API key, the GitHub token), the registered projects and the
// channels of each. It stores channel configs as opaque JSON; package config
// gives them their types. Conversations stay in each project's inbox.
//
// The file is not encrypted, like the config.json it replaces: it is private
// to the user (0600, in a 0700 directory), and SQLite gives its journal the
// same mode.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure Go: no cgo, so the binary stays a plain go install
)

// FileName is the database inside the gateway home.
const FileName = "gateway.db"

const (
	dirPerm  = 0o700
	filePerm = 0o600
	// busyTimeout lets a command read while the gateway writes, and the
	// other way round, instead of failing with SQLITE_BUSY.
	busyTimeout = 5 * time.Second
)

// Settings shared by every project.
const (
	SettingTypeSafeKey = "typesafe_api_key"
	SettingGitHubToken = "github_token" // the default GitHub token
	SettingUIToken     = "ui_token"
	SettingUIAddr      = "ui_addr"
)

// ErrNotFound reports a project that is not registered.
var ErrNotFound = errors.New("project not registered")

// ConflictError reports an app that another project already uses: one app
// belongs to one project, since two listeners on it would split its messages.
type ConflictError struct {
	AppKey  string
	Project Project // the project using it
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s is already used by project %q (%s): an app belongs to one project", e.AppKey, e.Project.Name, e.Project.Dir)
}

// Project is a registered repository.
type Project struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Dir       string    `json:"dir"` // repository root
	CreatedAt time.Time `json:"created_at"`
}

// Channel is one channel of a project: its config as package config stores
// it, and the key naming the app it connects to.
type Channel struct {
	Project   int64           `json:"project"`
	Name      string          `json:"name"` // "lark", "github", ...
	Config    json.RawMessage `json:"config"`
	AppKey    string          `json:"app_key"` // unique across projects, e.g. "lark:cli_x"
	UpdatedAt time.Time       `json:"updated_at"`
}

// Store is an open gateway database.
type Store struct {
	db   *sql.DB
	path string
}

// schema creates version 1. Every write bumps meta.revision, which the
// gateway watches to pick up changes made by other processes.
var schema = []string{
	`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
	`CREATE TABLE projects (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		dir TEXT NOT NULL UNIQUE,
		created_at TEXT NOT NULL)`,
	`CREATE TABLE channels (
		project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		name TEXT NOT NULL,
		config TEXT NOT NULL,
		app_key TEXT NOT NULL UNIQUE,
		updated_at TEXT NOT NULL,
		PRIMARY KEY (project_id, name))`,
	`CREATE TABLE meta (revision INTEGER NOT NULL)`,
	`INSERT INTO meta (revision) VALUES (0)`,
}

// tracked are the tables whose changes bump the revision.
var tracked = []string{"settings", "projects", "channels"}

const schemaVersion = 1

// Path returns the database file in home.
func Path(home string) string {
	return filepath.Join(home, FileName)
}

// Exists reports whether home has a database.
func Exists(home string) bool {
	_, err := os.Stat(Path(home))

	return err == nil
}

// Open opens the database in home, creating home and the database as needed.
func Open(ctx context.Context, home string) (*Store, error) {
	if err := os.MkdirAll(home, dirPerm); err != nil {
		return nil, fmt.Errorf("create %s: %w", home, err)
	}

	path := Path(home)

	// Create the file first, so that it never exists with wider permissions.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, filePerm) //nolint:gosec // the gateway's own file
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	return open(ctx, path)
}

// OpenExisting opens the database in home if there is one; ok is false if
// there is none, which is not an error: the gateway was never set up.
func OpenExisting(ctx context.Context, home string) (s *Store, ok bool, err error) {
	if _, err := os.Stat(Path(home)); errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}

	s, err = open(ctx, Path(home))

	return s, err == nil, err
}

func open(ctx context.Context, path string) (*Store, error) {
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout("+strconv.FormatInt(busyTimeout.Milliseconds(), 10)+")")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_txlock", "immediate")

	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	s := &Store{db: db, path: path}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()

		return nil, err
	}

	return s, nil
}

// Close closes the database.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close %s: %w", s.path, err)
	}

	return nil
}

func (s *Store) migrate(ctx context.Context) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		var version int
		if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
			return fmt.Errorf("read schema version: %w", err)
		}

		switch {
		case version == schemaVersion:
			return nil
		case version > schemaVersion:
			return fmt.Errorf("%s is from a newer claude-bot (schema %d, this one knows %d): upgrade claude-bot", s.path, version, schemaVersion)
		}

		stmts := slices.Clone(schema)

		for _, table := range tracked {
			for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
				stmts = append(stmts, fmt.Sprintf(
					`CREATE TRIGGER %s_%s AFTER %s ON %s BEGIN UPDATE meta SET revision = revision + 1; END`,
					table, op, op, table))
			}
		}

		for _, stmt := range append(stmts, fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)) {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("create schema: %w", err)
			}
		}

		return nil
	})
}

// tx runs f in a transaction, committed if f succeeds.
func (s *Store) tx(ctx context.Context, f func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}

	if err := f(tx); err != nil {
		_ = tx.Rollback()

		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

// Revision changes whenever anything in the database does.
func (s *Store) Revision(ctx context.Context) (int64, error) {
	var rev int64
	if err := s.db.QueryRowContext(ctx, `SELECT revision FROM meta`).Scan(&rev); err != nil {
		return 0, fmt.Errorf("read revision: %w", err)
	}

	return rev, nil
}

// Setting returns a setting, "" if it is not set.
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string

	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("read setting %s: %w", key, err)
	}

	return v, nil
}

// SettingGitHubTokenPrefix starts the key of each named GitHub token, for
// projects that use another account than the default token's.
const SettingGitHubTokenPrefix = "github_token:" //nolint:gosec // a settings key, not a credential

// SettingsWithPrefix returns the settings whose key starts with prefix, by
// the rest of their key.
func (s *Store) SettingsWithPrefix(ctx context.Context, prefix string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM settings WHERE substr(key, 1, ?) = ? ORDER BY key`, len(prefix), prefix)
	if err != nil {
		return nil, fmt.Errorf("read settings %s*: %w", prefix, err)
	}
	defer rows.Close() //nolint:errcheck // read-only

	out := map[string]string{}

	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("read settings %s*: %w", prefix, err)
		}

		out[k[len(prefix):]] = v
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read settings %s*: %w", prefix, err)
	}

	return out, nil
}

// SetSetting sets a setting; "" removes it.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	var err error
	if value == "" {
		_, err = s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
	} else {
		_, err = s.db.ExecContext(ctx,
			`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value
			 WHERE value <> excluded.value`, key, value)
	}

	if err != nil {
		return fmt.Errorf("save setting %s: %w", key, err)
	}

	return nil
}

const projectColumns = `id, name, dir, created_at`

type scanner interface{ Scan(dest ...any) error }

func scanProject(row scanner) (Project, error) {
	var (
		p       Project
		created string
	)

	if err := row.Scan(&p.ID, &p.Name, &p.Dir, &created); err != nil {
		return p, err
	}

	p.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)

	return p, nil
}

// Projects lists the registered projects by name.
func (s *Store) Projects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+projectColumns+` FROM projects ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only

	var out []Project

	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, fmt.Errorf("list projects: %w", err)
		}

		out = append(out, p)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}

	return out, nil
}

// Project returns a project by id.
func (s *Store) Project(ctx context.Context, id int64) (Project, error) {
	return s.projectWhere(ctx, `id = ?`, id)
}

// ProjectByDir returns the project registered for a repository root.
func (s *Store) ProjectByDir(ctx context.Context, dir string) (Project, error) {
	return s.projectWhere(ctx, `dir = ?`, dir)
}

// ProjectByName returns a project by name.
func (s *Store) ProjectByName(ctx context.Context, name string) (Project, error) {
	return s.projectWhere(ctx, `name = ?`, name)
}

func (s *Store) projectWhere(ctx context.Context, where string, arg any) (Project, error) {
	p, err := scanProject(s.db.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM projects WHERE `+where, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return p, fmt.Errorf("%w: %v", ErrNotFound, arg)
	}

	if err != nil {
		return p, fmt.Errorf("read project: %w", err)
	}

	return p, nil
}

// AddProject registers the repository at dir, named after its directory
// (with a number added if another project has that name). Registering a
// registered directory returns its project.
func (s *Store) AddProject(ctx context.Context, dir string) (Project, error) {
	var p Project

	err := s.tx(ctx, func(tx *sql.Tx) error {
		var err error

		p, err = addProject(ctx, tx, dir)

		return err
	})

	return p, err
}

// SaveProject registers the repository at dir, if it is not yet, and
// replaces its channels with chans, all or nothing: an app another project
// uses (a *ConflictError) leaves the database as it was.
func (s *Store) SaveProject(ctx context.Context, dir string, chans []Channel) (Project, error) {
	var p Project

	err := s.tx(ctx, func(tx *sql.Tx) error {
		var err error
		if p, err = addProject(ctx, tx, dir); err != nil {
			return err
		}

		return setChannels(ctx, tx, p.ID, chans)
	})

	return p, err
}

func addProject(ctx context.Context, tx *sql.Tx, dir string) (Project, error) {
	p, err := scanProject(tx.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM projects WHERE dir = ?`, dir))
	if err == nil {
		return p, nil
	}

	if !errors.Is(err, sql.ErrNoRows) {
		return p, fmt.Errorf("register %s: %w", dir, err)
	}

	base := filepath.Base(dir)
	now := time.Now().UTC()

	for n := 1; ; n++ {
		name := base
		if n > 1 {
			name = base + "-" + strconv.Itoa(n)
		}

		var taken bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM projects WHERE name = ?)`, name).Scan(&taken); err != nil {
			return p, fmt.Errorf("register %s: %w", dir, err)
		}

		if taken {
			continue
		}

		res, err := tx.ExecContext(ctx, `INSERT INTO projects (name, dir, created_at) VALUES (?, ?, ?)`, name, dir, now.Format(time.RFC3339Nano))
		if err != nil {
			return p, fmt.Errorf("register %s: %w", dir, err)
		}

		p = Project{Name: name, Dir: dir, CreatedAt: now}
		if p.ID, err = res.LastInsertId(); err != nil {
			return p, fmt.Errorf("register %s: %w", dir, err)
		}

		return p, nil
	}
}

// RemoveProject unregisters a project and forgets its channels. Its files
// are left alone.
func (s *Store) RemoveProject(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("remove project: %w", err)
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %d", ErrNotFound, id)
	}

	return nil
}

// Channels returns a project's channels by name.
func (s *Store) Channels(ctx context.Context, project int64) ([]Channel, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT project_id, name, config, app_key, updated_at FROM channels WHERE project_id = ? ORDER BY name`, project)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only

	var out []Channel

	for rows.Next() {
		var (
			c       Channel
			cfg     string
			updated string
		)

		if err := rows.Scan(&c.Project, &c.Name, &cfg, &c.AppKey, &updated); err != nil {
			return nil, fmt.Errorf("list channels: %w", err)
		}

		c.Config = json.RawMessage(cfg)
		c.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		out = append(out, c)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}

	return out, nil
}

// SetChannels replaces a project's channels with chans, all or nothing. An
// app another project uses is a *ConflictError.
func (s *Store) SetChannels(ctx context.Context, project int64, chans []Channel) error {
	return s.tx(ctx, func(tx *sql.Tx) error { return setChannels(ctx, tx, project, chans) })
}

func setChannels(ctx context.Context, tx *sql.Tx, project int64, chans []Channel) error {
	names := make([]any, 0, len(chans)+1)
	names = append(names, project)

	for _, c := range chans {
		if err := putChannel(ctx, tx, project, c); err != nil {
			return err
		}

		names = append(names, c.Name)
	}

	// Only placeholders are added to the statement, one per name.
	del := `DELETE FROM channels WHERE project_id = ?`
	if len(chans) > 0 {
		del += ` AND name NOT IN (?` + strings.Repeat(",?", len(chans)-1) + `)` //nolint:gosec // placeholders only
	}

	if _, err := tx.ExecContext(ctx, del, names...); err != nil {
		return fmt.Errorf("save channels: %w", err)
	}

	return nil
}

// PutChannel adds or replaces one channel of a project.
func (s *Store) PutChannel(ctx context.Context, project int64, c Channel) error {
	return s.tx(ctx, func(tx *sql.Tx) error { return putChannel(ctx, tx, project, c) })
}

// RemoveChannel removes one channel of a project.
func (s *Store) RemoveChannel(ctx context.Context, project int64, name string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM channels WHERE project_id = ? AND name = ?`, project, name); err != nil {
		return fmt.Errorf("remove channel %s: %w", name, err)
	}

	return nil
}

func putChannel(ctx context.Context, tx *sql.Tx, project int64, c Channel) error {
	var owner int64

	err := tx.QueryRowContext(ctx, `SELECT project_id FROM channels WHERE app_key = ? AND NOT (project_id = ? AND name = ?)`,
		c.AppKey, project, c.Name).Scan(&owner)

	switch {
	case err == nil:
		p, err := scanProject(tx.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM projects WHERE id = ?`, owner))
		if err != nil {
			return fmt.Errorf("save channel %s: %w", c.Name, err)
		}

		return &ConflictError{AppKey: c.AppKey, Project: p}
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("save channel %s: %w", c.Name, err)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO channels (project_id, name, config, app_key, updated_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (project_id, name) DO UPDATE SET config = excluded.config, app_key = excluded.app_key, updated_at = excluded.updated_at
		 WHERE config <> excluded.config OR app_key <> excluded.app_key`,
		project, c.Name, string(c.Config), c.AppKey, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("save channel %s: %w", c.Name, err)
	}

	return nil
}
