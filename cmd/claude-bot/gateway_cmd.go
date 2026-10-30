package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/hewenyu/claude-bot/internal/config"
	"github.com/hewenyu/claude-bot/internal/inbox"
	"github.com/hewenyu/claude-bot/internal/store"
)

// The gateway, like a project's listener, is started and stopped by the
// user from their own terminal, so that it keeps their environment.
func gatewayStart(ctx context.Context, args []string) error {
	if _, err := parseArgs("gateway start", args); err != nil {
		return err
	}

	if pid, err := gatewayPID(); pid != 0 || err != nil {
		if err == nil {
			fmt.Printf("gateway already running (pid %d); `claude-bot gateway restart` replaces it\n", pid)
		}

		return err
	}

	home, err := config.Home()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(home, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", home, err)
	}

	logPath := filepath.Join(home, gatewayLog)

	cmd, err := spawn(home, logPath, "gateway", "serve")
	if err != nil {
		return err
	}

	pid := cmd.Process.Pid

	err = waitStarted(ctx, cmd, logPath, "gateway did not start", func() bool {
		hb, alive := readGatewayHeartbeat()

		return alive && hb.PID == pid
	})
	if err != nil {
		return fmt.Errorf("gateway %w", err)
	}

	fmt.Printf("started gateway (pid %d), log: %s\n", pid, logPath)

	if err := reportSettled(ctx); err != nil {
		return err
	}

	if hb, _ := readGatewayHeartbeat(); hb.UI != "" {
		fmt.Printf("page: %s (open it with `claude-bot gateway open`, which adds the access token)\n", hb.UI)
	} else {
		fmt.Printf("! the page is not served; see %s\n", logPath)
	}

	return nil
}

// gatewayOpen opens the page with its token, which the page keeps.
func gatewayOpen(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("gateway open", flag.ContinueOnError)
	printOnly := fs.Bool("print", false, "only print the address")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w: %w", errUsage, err)
	}

	hb, alive := readGatewayHeartbeat()
	if !alive {
		return errors.New("gateway not running: run `claude-bot gateway start` first")
	}

	if hb.UI == "" {
		return fmt.Errorf("the gateway does not serve its page; see %s", gatewayLogPath())
	}

	s, err := config.OpenStore(ctx)
	if err != nil {
		return err
	}
	defer s.Close() //nolint:errcheck // read-only use

	token, err := s.Setting(ctx, store.SettingUIToken)
	if err != nil {
		return err
	}

	link := hb.UI + "#token=" + token
	fmt.Println(link)

	if !*printOnly {
		openBrowser(ctx, link)
	}

	return nil
}

// reportSettled gives the projects a moment to connect, then says how they
// are doing.
func reportSettled(ctx context.Context) error {
	deadline := time.Now().Add(settleWait)
	for time.Now().Before(deadline) && !settled() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(startupPoll):
		}
	}

	st, err := collectGatewayStatus(ctx)
	if err != nil {
		return err
	}

	printProjects(st)

	return nil
}

// settled reports whether no project is still connecting.
func settled() bool {
	hb, alive := readGatewayHeartbeat()
	if !alive {
		return false
	}

	return !slices.ContainsFunc(hb.Projects, func(o runOutcome) bool { return o.State == stateStarting })
}

func gatewayStop(ctx context.Context, args []string) error {
	if _, err := parseArgs("gateway stop", args); err != nil {
		return err
	}

	pid, err := gatewayPID()
	if pid == 0 || err != nil {
		if err == nil {
			fmt.Println("gateway not running")
		}

		return err
	}

	lock, err := gatewayFile(gatewayLock)
	if err != nil {
		return err
	}

	if err := stopHolder(ctx, lock, pid, "gateway"); err != nil {
		return err
	}

	fmt.Printf("stopped gateway (pid %d)\n", pid)

	return nil
}

// gatewayStatus is the output of `claude-bot gateway status`.
type gatewayStatus struct {
	Home      string          `json:"home"`
	Running   bool            `json:"running"`
	PID       int             `json:"pid,omitempty"`
	Version   string          `json:"version,omitempty"` // of the running gateway
	StartedAt *time.Time      `json:"started_at,omitempty"`
	Jev       bool            `json:"jev"`          // the shared TypeSafe API key is set
	GitHub    bool            `json:"github_token"` // the shared GitHub token is set
	Projects  []projectStatus `json:"projects"`
}

// projectStatus is how one registered project is doing.
type projectStatus struct {
	ID        int64             `json:"id"`
	Name      string            `json:"name"`
	Dir       string            `json:"dir"`
	State     string            `json:"state"`
	Error     string            `json:"error,omitempty"`
	Channels  []string          `json:"channels"`            // configured
	Connected []string          `json:"connected,omitempty"` // listening now
	Stopped   []string          `json:"stopped,omitempty"`   // failed while the others kept running
	Errors    map[string]string `json:"errors,omitempty"`    // why they stopped
	Pending   int               `json:"pending"`             // tasks waiting for Claude
	Screening int               `json:"screening"`           // messages waiting for Jev
}

func gatewayStatusCmd(ctx context.Context, args []string) error {
	if _, err := parseArgs("gateway status", args); err != nil {
		return err
	}

	st, err := collectGatewayStatus(ctx)
	if err != nil {
		return err
	}

	return printJSON(st)
}

// errNoGateway reports that no project was ever registered.
var errNoGateway = errors.New("no gateway set up yet: run `claude-bot init` in a project, or `claude-bot gateway import` in one set up before the gateway")

// collectGatewayStatus reads the database, the gateway's heartbeat and each
// project's heartbeat and inbox.
func collectGatewayStatus(ctx context.Context) (gatewayStatus, error) {
	home, err := config.Home()
	if err != nil {
		return gatewayStatus{}, err
	}

	s, ok, err := store.OpenExisting(ctx, home)
	if err != nil {
		return gatewayStatus{}, err
	}

	if !ok {
		return gatewayStatus{}, errNoGateway
	}

	defer s.Close() //nolint:errcheck // read-only use

	return gatherStatus(ctx, home, s)
}

func gatherStatus(ctx context.Context, home string, s *store.Store) (gatewayStatus, error) {
	st := gatewayStatus{Home: home, Projects: []projectStatus{}}

	hb, alive := readGatewayHeartbeat()
	if alive {
		st.Running, st.PID, st.Version, st.StartedAt = true, hb.PID, hb.Version, &hb.StartedAt
	}

	for key, set := range map[string]*bool{store.SettingTypeSafeKey: &st.Jev, store.SettingGitHubToken: &st.GitHub} {
		v, err := s.Setting(ctx, key)
		if err != nil {
			return st, err
		}

		*set = v != ""
	}

	projects, err := s.Projects(ctx)
	if err != nil {
		return st, err
	}

	for _, p := range projects {
		ps, err := projectStatusOf(ctx, s, p, hb, alive)
		if err != nil {
			return st, err
		}

		st.Projects = append(st.Projects, ps)
	}

	return st, nil
}

func projectStatusOf(ctx context.Context, s *store.Store, p store.Project, gw gatewayHeartbeat, gwAlive bool) (projectStatus, error) {
	ps := projectStatus{ID: p.ID, Name: p.Name, Dir: p.Dir, State: stateStopped, Channels: []string{}}

	chans, err := s.Channels(ctx, p.ID)
	if err != nil {
		return ps, err
	}

	for _, c := range chans {
		ps.Channels = append(ps.Channels, c.Name)
	}

	if gwAlive {
		ps.State = stateStarting
		if i := slices.IndexFunc(gw.Projects, func(o runOutcome) bool { return o.ID == p.ID }); i >= 0 {
			ps.State, ps.Error = gw.Projects[i].State, gw.Projects[i].Error
		}
	}

	dir := filepath.Join(p.Dir, config.DirName)
	if hb, alive := readHeartbeat(dir); alive && (hb.PID == gw.PID || !gwAlive) {
		ps.Connected, ps.Stopped, ps.Errors = hb.Channels, hb.Stopped, hb.Errors
	}

	box := inbox.New(filepath.Join(dir, inboxDir))

	pending, err := box.PendingTasks()
	if err != nil {
		return ps, err
	}

	incoming, err := box.Incoming()
	if err != nil {
		return ps, err
	}

	ps.Pending, ps.Screening = len(pending), len(incoming)

	return ps, nil
}

// printProjects shows the projects as a table for people.
func printProjects(st gatewayStatus) {
	if len(st.Projects) == 0 {
		fmt.Println("no projects registered yet: run `claude-bot init` in a project (or `claude-bot gateway import` in one set up before the gateway)")

		return
	}

	for _, p := range st.Projects {
		detail := strings.Join(p.Connected, ", ")
		if detail == "" {
			detail = strings.Join(p.Channels, ", ")
		}

		if len(p.Stopped) > 0 {
			detail += "; stopped: " + strings.Join(p.Stopped, ", ")
		}

		fmt.Printf("  %-20s %-9s %s\n", p.Name, p.State, detail)

		if p.Error != "" {
			fmt.Printf("  %-20s           %s\n", "", p.Error)
		}
	}
}

// gatewayImport moves projects set up before the gateway into it.
func gatewayImport(ctx context.Context, project string, args []string) error {
	dirs, err := parseArgs("gateway import", args)
	if err != nil {
		return err
	}

	if len(dirs) == 0 {
		dirs = []string{project} // "" is this repository
	}

	var errs []error

	for _, d := range dirs {
		if err := importProject(ctx, d); err != nil {
			errs = append(errs, err)
		}
	}

	if gw, _ := gatewayPID(); gw == 0 && len(errs) < len(dirs) {
		fmt.Println("\n下一步：在终端里运行  claude-bot gateway start")
	}

	return errors.Join(errs...)
}

func importProject(ctx context.Context, project string) error {
	dir, err := config.Dir(ctx, project)
	if err != nil {
		return err
	}

	imp, err := config.Import(ctx, dir)
	if err != nil {
		return fmt.Errorf("import %s: %w", filepath.Dir(dir), err)
	}

	fmt.Printf("✓ 已导入 %s（%s）：%s；原文件改名为 %s\n", imp.Project.Name, imp.Project.Dir, strings.Join(imp.Channels, ", "), imp.Backup)

	for _, k := range imp.Kept {
		fmt.Printf("  这个项目自己的 %s 和 gateway 里已有的不同，沿用 gateway 的\n", k)
	}

	// A listener of the project's own still holds it; the gateway takes over
	// once it stops.
	a := assemble(dir, newLogger())
	if pid, err := a.listenerPID(); err == nil && pid != 0 {
		if gw, _ := gatewayPID(); gw != pid {
			fmt.Printf("  ! 这个项目自己的监听进程（pid %d）还在运行，gateway 要等它停掉才接手：claude-bot -C %s stop\n", pid, imp.Project.Dir)
		}
	}

	return nil
}

// gatewayRemove unregisters a project. Its configuration goes back to its
// config.json, so that it can still run on its own.
func gatewayRemove(ctx context.Context, args []string) error {
	args, err := parseArgs("gateway remove", args)
	if err != nil {
		return err
	}

	if len(args) != 1 {
		return fmt.Errorf("%w: gateway remove <project name or directory>", errUsage)
	}

	s, err := config.OpenStore(ctx)
	if err != nil {
		return err
	}
	defer s.Close() //nolint:errcheck // writes are committed

	p, err := s.ProjectByName(ctx, args[0])
	if errors.Is(err, store.ErrNotFound) {
		if abs, aerr := filepath.Abs(args[0]); aerr == nil {
			p, err = s.ProjectByDir(ctx, abs)
		}
	}

	if err != nil {
		return err
	}

	path, err := unregister(ctx, s, p)
	if err != nil {
		return err
	}

	if path == "" {
		fmt.Printf("✓ removed %s from the gateway (its directory %s is gone)\n", p.Name, p.Dir)
	} else {
		fmt.Printf("✓ removed %s from the gateway; its configuration is back in %s\n", p.Name, path)
	}

	return nil
}

// unregister removes a project from the gateway after writing its
// configuration back to its config.json, and returns that file.
func unregister(ctx context.Context, s *store.Store, p store.Project) (string, error) {
	cfg, err := config.FromStore(ctx, s, p)
	if err != nil {
		return "", err
	}

	cfg.Gateway = nil
	dir := filepath.Join(p.Dir, config.DirName)
	path := filepath.Join(dir, "config.json")

	if _, err := os.Stat(p.Dir); err != nil {
		path = "" // the project is gone; nothing to write back
	} else if err := config.WriteFile(dir, cfg); err != nil {
		return "", err
	}

	if err := s.RemoveProject(ctx, p.ID); err != nil {
		return "", err
	}

	return path, nil
}
