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
	"github.com/hewenyu/claude-bot/internal/store"
)

// The gateway, like a project's listener, is started and stopped by the
// user from their own terminal, so that it keeps their environment.
func gatewayStart(ctx context.Context, args []string) error {
	if _, err := parseCommand(nil, args, exactly(0), "gateway start"); err != nil {
		return err
	}

	dir, err := config.EnsureHome()
	if err != nil {
		return err
	}

	home := gatewayHome(dir)

	if pid, err := home.pid(); pid != 0 || err != nil {
		if err == nil {
			fmt.Printf("gateway already running (pid %d); %s replaces it\n", pid, hintGatewayRestart)
		}

		return err
	}

	logPath := home.path(gatewayLog)

	cmd, err := spawn(dir, logPath, "gateway", "serve")
	if err != nil {
		return err
	}

	pid := cmd.Process.Pid

	err = waitStarted(ctx, cmd, logPath, "gateway", func() bool {
		hb, alive := home.heartbeat()

		return alive && hb.PID == pid
	})
	if err != nil {
		return err
	}

	fmt.Printf("started gateway (pid %d), log: %s\n", pid, logPath)

	if err := reportSettled(ctx, home); err != nil {
		return err
	}

	if hb, _ := home.heartbeat(); hb.UI != "" {
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

	if _, err := parseCommand(fs, args, exactly(0), "gateway open [--print]"); err != nil {
		return err
	}

	home, err := currentHome()
	if err != nil {
		return err
	}

	hb, alive := home.heartbeat()
	if !alive {
		return errors.New("gateway not running: run " + hintGatewayStart + " first")
	}

	if hb.UI == "" {
		return fmt.Errorf("the gateway does not serve its page; see %s", home.path(gatewayLog))
	}

	s, err := store.Open(ctx, string(home))
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
func reportSettled(ctx context.Context, home gatewayHome) error {
	deadline := time.Now().Add(settleWait)
	for time.Now().Before(deadline) && !settled(home) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(startupPoll):
		}
	}

	st, err := collectGatewayStatus(ctx, home)
	if err != nil {
		return err
	}

	printProjects(os.Stdout, st)

	return nil
}

// settled reports whether no project is still connecting.
func settled(home gatewayHome) bool {
	hb, alive := home.heartbeat()
	if !alive {
		return false
	}

	return !slices.ContainsFunc(hb.Projects, func(o runOutcome) bool { return o.State == stateStarting })
}

func gatewayStop(ctx context.Context, args []string) error {
	if _, err := parseCommand(nil, args, exactly(0), "gateway stop"); err != nil {
		return err
	}

	home, err := currentHome()
	if err != nil {
		return err
	}

	pid, err := home.pid()
	if pid == 0 || err != nil {
		if err == nil {
			fmt.Println("gateway not running")
		}

		return err
	}

	if err := stopHolder(ctx, home.path(gatewayLock), pid, "gateway"); err != nil {
		return err
	}

	fmt.Printf("stopped gateway (pid %d)\n", pid)

	return nil
}

// gatewayImport moves projects set up before the gateway into it.
func gatewayImport(ctx context.Context, project string, args []string) error {
	dirs, err := parseCommand(nil, args, atLeast(0), "gateway import [<repo>...]")
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
		fmt.Println("\n下一步：在终端里运行  " + lineGatewayStart)
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

	if imp.GitHubToken != "" {
		fmt.Printf("  这个项目的 GitHub token 和 gateway 的默认 token 不同，存为「%s」，只给它用（网页的共用设置里可以改）\n", imp.GitHubToken)
	}

	// A listener of the project's own still holds it; the gateway takes over
	// once it stops.
	if pid := ownListener(dir); pid != 0 {
		fmt.Printf("  ! 这个项目自己的监听进程（pid %d）还在运行，gateway 要等它停掉才接手：claude-bot -C %s stop\n", pid, imp.Project.Dir)
	}

	return nil
}

// ownListener is the pid of the project's own listener, which keeps the
// gateway out of the project in the bot directory dir; 0 if there is none.
func ownListener(dir string) int {
	pid, err := assemble(dir, quiet).listenerPID()
	if err != nil || pid == 0 {
		return 0
	}

	if gw, _ := gatewayPID(); gw == pid {
		return 0
	}

	return pid
}

// gatewayRemove unregisters a project. Its configuration goes back to its
// config.json, so that it can still run on its own.
func gatewayRemove(ctx context.Context, args []string) error {
	args, err := parseCommand(nil, args, exactly(1), "gateway remove <project name or directory>")
	if err != nil {
		return err
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

	path, err := config.Unregister(ctx, s, p)
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
