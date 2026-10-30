package screen

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hewenyu/claude-bot/internal/jev"
)

// group joins pieces that Jev says are about the same task: one question per
// pair, and code joins every pair at or above SameTask. Groups keep the
// pieces' order and come out in the order of their first piece.
func group(ctx context.Context, ask Asker, pieces []Piece) ([][]Piece, error) {
	n := len(pieces)

	// Zero-padded ids keep the pieces in order in the JSON state.
	id := func(i int) string { return fmt.Sprintf("p%0*d", len(strconv.Itoa(n)), i+1) }
	pair := func(i, j int) string { return id(i) + "_" + id(j) }

	conv := make(map[string]string, n)
	for i, p := range pieces {
		conv[id(i)] = p.jevText
	}

	questions := make(map[string]jev.Noul, n*(n-1)/2)
	for i := range n {
		for j := i + 1; j < n; j++ {
			questions[pair(i, j)] = jev.Noul{
				Instructions: fmt.Sprintf("Are `conversation.%s` and `conversation.%s` about the same task (the same bug, feature request or question)?", id(i), id(j)),
				True:         "Both are about the same bug, feature or question, e.g. one adds detail, a log, or urgency to the other, or both are items of one list of related work",
				False:        "They are about different bugs, features or questions",
			}
		}
	}

	var answers map[string]float64

	if len(questions) > 0 {
		var err error
		if answers, err = ask.Nouls(ctx, map[string]any{"conversation": conv}, questions); err != nil {
			return nil, fmt.Errorf("group pieces: %w", err)
		}
	}

	return partition(pieces, func(i, j int) bool { return answers[pair(i, j)] >= SameTask }), nil
}

// partition groups pieces, joining i < j when same(i, j) and everything
// joined to either (union-find).
func partition(pieces []Piece, same func(i, j int) bool) [][]Piece {
	parent := make([]int, len(pieces))
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

	for i := range pieces {
		for j := i + 1; j < len(pieces); j++ {
			if same(i, j) {
				parent[root(j)] = root(i)
			}
		}
	}

	var (
		groups [][]Piece
		index  = map[int]int{} // root -> group
	)

	for i, p := range pieces {
		r := root(i)

		g, ok := index[r]
		if !ok {
			g = len(groups)
			index[r] = g

			groups = append(groups, nil)
		}

		groups[g] = append(groups[g], p)
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

// score asks every category question about a task's text.
func score(ctx context.Context, ask Asker, pieces []Piece) (map[string]float64, error) {
	texts := make([]string, len(pieces))
	for i, p := range pieces {
		texts[i] = p.jevText
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
