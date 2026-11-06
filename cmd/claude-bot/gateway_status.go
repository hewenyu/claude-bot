package main

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/hewenyu/claude-bot/internal/store"
)

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
	State     projectState      `json:"state"`
	Error     string            `json:"error,omitempty"`
	Channels  []string          `json:"channels"`            // configured
	Connected []string          `json:"connected,omitempty"` // listening now
	Stopped   []string          `json:"stopped,omitempty"`   // failed while the others kept running
	Errors    map[string]string `json:"errors,omitempty"`    // why they stopped
	Pending   int               `json:"pending"`             // tasks waiting for Claude
	Screening int               `json:"screening"`           // messages waiting for Jev
}

func gatewayStatusCmd(ctx context.Context, args []string) error {
	if _, err := parseCommand(nil, args, exactly(0), "gateway status"); err != nil {
		return err
	}

	home, err := currentHome()
	if err != nil {
		return err
	}

	st, err := collectGatewayStatus(ctx, home)
	if err != nil {
		return err
	}

	return printJSON(st)
}

// errNoGateway reports that no project was ever registered.
var errNoGateway = errors.New("no gateway set up yet: run " + hintInit + " in a project, or `claude-bot gateway import` in one set up before the gateway")

// collectGatewayStatus reads the database, the gateway's heartbeat and each
// project's heartbeat and inbox.
func collectGatewayStatus(ctx context.Context, home gatewayHome) (gatewayStatus, error) {
	s, ok, err := store.OpenExisting(ctx, string(home))
	if err != nil {
		return gatewayStatus{}, err
	}

	if !ok {
		return gatewayStatus{}, errNoGateway
	}

	defer s.Close() //nolint:errcheck // read-only use

	hb, alive := home.heartbeat()

	return gatherStatus(ctx, string(home), s, hb, alive)
}

// gatherStatus is the gateway in home, whose heartbeat is hb, alive or not,
// and every project registered in s.
func gatherStatus(ctx context.Context, home string, s *store.Store, hb gatewayHeartbeat, alive bool) (gatewayStatus, error) {
	st := gatewayStatus{Home: home, Projects: []projectStatus{}}

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

	if hb, alive := readHeartbeat(botDir(p)); alive && (hb.PID == gw.PID || !gwAlive) {
		ps.Connected, ps.Stopped, ps.Errors = hb.Channels, hb.Stopped, hb.Errors
	}

	box := projectInbox(p)

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
func printProjects(w io.Writer, st gatewayStatus) {
	out := printer{w}

	if len(st.Projects) == 0 {
		out.println("no projects registered yet: run " + hintInit + " in a project (or `claude-bot gateway import` in one set up before the gateway)")

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

		out.printf("  %-20s %-9s %s\n", p.Name, p.State, detail)

		if p.Error != "" {
			out.printf("  %-20s           %s\n", "", p.Error)
		}
	}
}
