package screen

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hewenyu/claude-bot/internal/jev"
)

// pieceGroup is pieces about one task, and the thread they continue ("" for
// a new task).
type pieceGroup struct {
	thread string
	pieces []Piece
}

// group joins pieces that Jev says are about the same task, then joins each
// group to the thread it continues, if any: one question per pair of pieces
// and per piece and thread, and code joins every pair at or above SameTask.
// A group continues the thread its pieces match best; groups continuing the
// same thread become one. Groups keep the pieces' order and come out in the
// order of their first piece.
func group(ctx context.Context, ask Asker, pieces []Piece, threads []Thread) ([]pieceGroup, error) {
	n := len(pieces)
	if n == 0 {
		return nil, nil
	}

	q := newQuestions(pieces, threads)

	var answers map[string]float64

	if len(q.nouls) > 0 {
		var err error
		if answers, err = ask.Nouls(ctx, q.state, q.nouls); err != nil {
			return nil, fmt.Errorf("group pieces: %w", err)
		}
	}

	var (
		out      []pieceGroup
		byThread = map[string]int{} // thread -> group in out
	)

	for _, members := range partition(n, func(i, j int) bool { return answers[q.pair(i, j)] >= SameTask }) {
		thread := bestThread(members, threads, func(i, k int) float64 { return answers[q.cont(i, k)] })

		g, ok := byThread[thread]
		if !ok || thread == "" {
			g = len(out)
			out = append(out, pieceGroup{thread: thread})

			if thread != "" {
				byThread[thread] = g
			}
		}

		for _, i := range members {
			out[g].pieces = append(out[g].pieces, pieces[i])
		}
	}

	return out, nil
}

// bestThread is the thread the members match best at or above SameTask, ""
// if none. Threads come latest first, so on a tie the latest wins.
func bestThread(members []int, threads []Thread, p func(i, k int) float64) string {
	thread, best := "", 0.0

	for _, i := range members {
		for k, th := range threads {
			if s := p(i, k); s >= SameTask && s > best {
				thread, best = th.ID, s
			}
		}
	}

	return thread
}

// questions are the grouping questions about some pieces and threads: one
// per pair of pieces and per piece and thread, and the state they are about.
type questions struct {
	n     int
	state map[string]any
	nouls map[string]jev.Noul
}

// id names piece i in the state; zero-padded, ids keep the pieces in order
// in the JSON.
func (q questions) id(i int) string { return fmt.Sprintf("p%0*d", len(strconv.Itoa(q.n)), i+1) }

func (questions) tid(k int) string { return "t" + strconv.Itoa(k+1) }

func (q questions) pair(i, j int) string { return q.id(i) + "_" + q.id(j) }

func (q questions) cont(i, k int) string { return q.id(i) + "_" + q.tid(k) }

func newQuestions(pieces []Piece, threads []Thread) questions {
	n := len(pieces)
	q := questions{n: n, nouls: make(map[string]jev.Noul, n*(n-1)/2+n*len(threads))}

	conv := make(map[string]string, n)
	for i, p := range pieces {
		conv[q.id(i)] = p.jevText
	}

	q.state = map[string]any{"conversation": conv}

	if len(threads) > 0 {
		tasks := make(map[string]string, len(threads))
		for k, th := range threads {
			tasks[q.tid(k)] = th.Transcript
		}

		q.state["tasks"] = tasks
	}

	for i := range n {
		for j := i + 1; j < n; j++ {
			q.nouls[q.pair(i, j)] = jev.Noul{
				Instructions: fmt.Sprintf("Are `conversation.%s` and `conversation.%s` about the same task (the same bug, feature request or question)?", q.id(i), q.id(j)),
				True:         "Both are about the same bug, feature or question, e.g. one adds detail, a log, or urgency to the other, or both are items of one list of related work",
				False:        "They are about different bugs, features or questions",
			}
		}

		for k := range threads {
			q.nouls[q.cont(i, k)] = jev.Noul{
				Instructions: fmt.Sprintf("Does `conversation.%s` continue the earlier conversation `tasks.%s`?", q.id(i), q.tid(k)),
				True: "It is about the same bug, feature or question; adds detail, a file, urgency or a decision to it; " +
					"or answers, confirms or follows up on what the bot said or asked there",
				False: "It starts a different bug, feature or question, even if the same person asks",
			}
		}
	}

	return q
}

// partition groups 0..n-1, joining i < j when same(i, j) and everything
// joined to either (union-find). Groups list their members in order and come
// out in the order of their first member.
func partition(n int, same func(i, j int) bool) [][]int {
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}

	var root func(int) int

	root = func(i int) int {
		if parent[i] != i {
			parent[i] = root(parent[i])
		}

		return parent[i]
	}

	for i := range n {
		for j := i + 1; j < n; j++ {
			if same(i, j) {
				parent[root(j)] = root(i)
			}
		}
	}

	var (
		groups [][]int
		index  = map[int]int{} // root -> group
	)

	for i := range n {
		r := root(i)

		g, ok := index[r]
		if !ok {
			g = len(groups)
			index[r] = g

			groups = append(groups, nil)
		}

		groups[g] = append(groups[g], i)
	}

	return groups
}

// category is one kind of illegal request. Each is its own question: Jev
// answers narrow questions better than one broad "is this illegal?".
type category struct {
	name, question string
}

// Categories follow the content PRC Cybersecurity Law art. 12 forbids, plus
// asking the bot for its own credentials.
var categories = []category{
	{"intrusion", "Does the message ask for help breaking into, attacking or disrupting computer systems or accounts the sender does not own, such as stealing accounts, credential stuffing, DDoS, or writing malware or ransomware?"},
	{"fraud", "Does the message ask for help with fraud, scams, phishing, money laundering or forged documents?"},
	{"personal_data", "Does the message ask to find, collect, expose or sell other people's personal information, such as doxxing or buying leaked data?"},
	{"secrets", "Does the message ask the bot to reveal its tokens, API keys, passwords, configuration files or environment variables?"},
	{"piracy", "Does the message ask for help cracking paid software, bypassing license checks or copy protection, or spreading pirated content?"},
	{"contraband", "Does the message ask for help with illegal drugs, weapons or gambling?"},
	{"sexual_violent", "Does the message ask for pornography, sexual content involving minors, or content promoting violence or terrorism?"},
	{"state_security", "Does the message call for subverting the state, splitting the country, terrorism or extremism, or inciting ethnic hatred?"},
}

// Shared answers of the category questions. The "no" side names the work a
// project bot gets, so that it is not mistaken for a "yes".
const (
	illegalYes = "The message asks for this, directly or thinly disguised"
	illegalNo  = "An ordinary request about a software project: bugs, features, code review, audits, docs, releases or questions, " +
		"including work on the project's own security; or anything else that does not ask for this. Ads and spam are not this"
)

// score asks every category question about a task's text: what was asked
// before in its thread, if any, and its pieces.
func score(ctx context.Context, ask Asker, asked string, pieces []Piece) (map[string]float64, error) {
	var texts []string
	if asked != "" {
		texts = append(texts, asked)
	}

	for _, p := range pieces {
		texts = append(texts, p.jevText)
	}

	questions := make(map[string]jev.Noul, len(categories))
	for _, c := range categories {
		questions[c.name] = jev.Noul{Instructions: c.question, True: illegalYes, False: illegalNo}
	}

	scores, err := ask.Nouls(ctx, strings.Join(texts, "\n"), questions)
	if err != nil {
		return nil, fmt.Errorf("check legality: %w", err)
	}

	return scores, nil
}
