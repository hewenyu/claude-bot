package lark

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"github.com/hewenyu/claude-bot/internal/channel"
)

// mention is the platform-neutral view of a Lark mention entry.
type mention struct {
	Key    string // placeholder in content, e.g. "@_user_1"
	OpenID string
	Name   string
	IsBot  bool // this mention is our bot
}

// Element tags of a post (rich text) message.
const (
	tagText      = "text"
	tagMarkdown  = "md"
	tagLink      = "a"
	tagAt        = "at"
	tagCodeBlock = "code_block"
	tagImage     = "img"
	tagMedia     = "media"
)

// Locales of a post; the bot writes Chinese.
const (
	localeZhCN = "zh_cn"
	localeEnUS = "en_us"
)

func mentionSyntax(openID string) string {
	return `<at user_id="` + openID + `"></at>`
}

// parseContent turns Lark message content into readable text and attachments.
// Mentions of the bot are removed; other mentions become "@Name".
func parseContent(msgType, content string, mentions []mention) (string, []channel.Attachment) {
	var (
		text string
		atts []channel.Attachment
	)

	switch msgType {
	case larkim.MsgTypeText:
		var c struct {
			Text string `json:"text"`
		}

		text = content
		if json.Unmarshal([]byte(content), &c) == nil {
			text = c.Text
		}
	case larkim.MsgTypePost:
		text, atts = parsePost(content)
	case larkim.MsgTypeImage:
		var c struct {
			ImageKey string `json:"image_key"`
		}

		if json.Unmarshal([]byte(content), &c) == nil && c.ImageKey != "" {
			atts = append(atts, channel.Attachment{Kind: channel.AttachmentImage, Key: c.ImageKey})
		}

		text = "[image]"
	case larkim.MsgTypeFile, larkim.MsgTypeAudio, larkim.MsgTypeMedia:
		var c struct {
			FileKey  string `json:"file_key"`
			FileName string `json:"file_name"`
		}

		if json.Unmarshal([]byte(content), &c) == nil && c.FileKey != "" {
			atts = append(atts, channel.Attachment{Kind: channel.AttachmentFile, Key: c.FileKey, Name: c.FileName})
		}

		text = "[" + msgType + "]"
	default:
		text = "[" + msgType + "]"
	}

	return replaceMentions(text, mentions), atts
}

// postSegment is one element of a post, read from messages and written in
// replies.
type postSegment struct {
	Tag      string `json:"tag"`
	Text     string `json:"text,omitempty"`
	Href     string `json:"href,omitempty"`
	UserID   string `json:"user_id,omitempty"`
	UserName string `json:"user_name,omitempty"`
	ImageKey string `json:"image_key,omitempty"`
	FileKey  string `json:"file_key,omitempty"`
	Language string `json:"language,omitempty"`
}

// post is one language of a rich-text message: paragraphs of segments.
type post struct {
	Title   string          `json:"title,omitempty"`
	Content [][]postSegment `json:"content"`
}

func parsePost(content string) (string, []channel.Attachment) {
	var p post
	if err := json.Unmarshal([]byte(content), &p); err != nil || p.Content == nil {
		// Locale-wrapped form: {"zh_cn": {"title": ..., "content": ...}}.
		var byLocale map[string]post
		if json.Unmarshal([]byte(content), &byLocale) != nil {
			return content, nil
		}

		p = pickLocale(byLocale)
	}

	var (
		lines []string
		atts  []channel.Attachment
	)

	if p.Title != "" {
		lines = append(lines, p.Title)
	}

	for _, para := range p.Content {
		var b strings.Builder

		for _, seg := range para {
			if att := renderSegment(&b, seg); att != nil {
				atts = append(atts, *att)
			}
		}

		lines = append(lines, b.String())
	}

	return strings.Join(lines, "\n"), atts
}

// pickLocale returns one language of a post deterministically, preferring
// Chinese, then English, then the first locale by name.
func pickLocale(byLocale map[string]post) post {
	for _, locale := range []string{localeZhCN, localeEnUS} {
		if p, ok := byLocale[locale]; ok {
			return p
		}
	}

	if locales := slices.Sorted(maps.Keys(byLocale)); len(locales) > 0 {
		return byLocale[locales[0]]
	}

	return post{}
}

// renderSegment writes one post element as text and returns its attachment, if any.
func renderSegment(b *strings.Builder, seg postSegment) *channel.Attachment {
	switch seg.Tag {
	case tagText, tagMarkdown:
		b.WriteString(seg.Text)
	case tagLink:
		b.WriteString(seg.Text)

		if seg.Href != "" && seg.Href != seg.Text {
			b.WriteString(" (" + seg.Href + ")")
		}
	case tagAt:
		b.WriteString(seg.UserID) // placeholder key, resolved by replaceMentions
	case tagCodeBlock:
		b.WriteString("\n```" + seg.Language + "\n" + seg.Text + "\n```\n")
	case tagImage:
		b.WriteString("[image]")

		return &channel.Attachment{Kind: channel.AttachmentImage, Key: seg.ImageKey}
	case tagMedia:
		b.WriteString("[media]")

		return &channel.Attachment{Kind: channel.AttachmentFile, Key: seg.FileKey}
	}

	return nil
}

func replaceMentions(text string, mentions []mention) string {
	// Longest keys first, so "@_user_1" cannot match inside "@_user_10".
	byKeyLen := slices.SortedFunc(slices.Values(mentions), func(a, b mention) int {
		return cmp.Compare(len(b.Key), len(a.Key))
	})

	for _, m := range byKeyLen {
		if m.Key == "" {
			continue
		}

		if m.IsBot {
			text = strings.ReplaceAll(text, m.Key+" ", "")
			text = strings.ReplaceAll(text, m.Key, "")

			continue
		}

		text = strings.ReplaceAll(text, m.Key, "@"+m.Name)
	}

	return strings.TrimSpace(text)
}

func otherMentions(mentions []mention) []channel.User {
	var out []channel.User

	for _, m := range mentions {
		if !m.IsBot && m.OpenID != "" {
			out = append(out, channel.User{ID: m.OpenID, Name: m.Name, Mention: mentionSyntax(m.OpenID)})
		}
	}

	return out
}

func millis(s string) time.Time {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}
	}

	return time.UnixMilli(n).UTC()
}

// buildPost renders Markdown as a Lark post, optionally @-mentioning someone first.
func buildPost(text, mentionOpenID string) (string, error) {
	var p post
	if mentionOpenID != "" {
		p.Content = append(p.Content, []postSegment{{Tag: tagAt, UserID: mentionOpenID}})
	}

	// Lark only accepts an md element alone in its paragraph.
	p.Content = append(p.Content, []postSegment{{Tag: tagMarkdown, Text: text}})

	return marshal(map[string]post{localeZhCN: p})
}

// buildText renders a plain text message, the fallback when post is rejected.
func buildText(text, mentionOpenID string) (string, error) {
	if mentionOpenID != "" {
		text = mentionSyntax(mentionOpenID) + " " + text
	}

	return marshal(map[string]string{"text": text})
}

// marshal encodes v without HTML-escaping, so <at> tags stay readable.
func marshal(v any) (string, error) {
	var b strings.Builder

	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(v); err != nil {
		return "", fmt.Errorf("encode content: %w", err)
	}

	return strings.TrimSuffix(b.String(), "\n"), nil
}
