package slack

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
)

const (
	imageMIME     = "image/"
	directPrefix  = "D" // channel ids of direct messages
	channelTypeIM = "im"
	eventMention  = "app_mention"
	eventMessage  = "message"
)

// Message subtypes that still carry something a person wrote.
var userSubtypes = map[string]bool{"": true, "file_share": true, "thread_broadcast": true}

// userMention matches <@U123> and the <@U123|name> form.
var userMention = regexp.MustCompile(`<@([UW][A-Z0-9]+)(?:\|[^>]*)?>`)

// Slack escapes these three characters in message text.
var unescape = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&")

// event is the part of a message event, or a message from the history, the
// adapter reads.
type event struct {
	Type        string `json:"type"`
	Subtype     string `json:"subtype"`
	Channel     string `json:"channel"`
	ChannelType string `json:"channel_type"`
	User        string `json:"user"`
	BotID       string `json:"bot_id"`
	Text        string `json:"text"`
	TS          string `json:"ts"`
	ThreadTS    string `json:"thread_ts"`
	Files       []file `json:"files"`
}

type file struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Mimetype           string `json:"mimetype"`
	URLPrivateDownload string `json:"url_private_download"`
}

// fromEvent converts a received event; ok is false for events that are not
// a person's message addressed to the bot: an @-mention in a channel, or a
// direct message.
func fromEvent(ev event, botUserID string) (channel.Message, bool) {
	if ev.BotID != "" || ev.User == "" || ev.User == botUserID || !userSubtypes[ev.Subtype] {
		return channel.Message{}, false
	}

	switch {
	case ev.Type == eventMention:
	case ev.Type == eventMessage && ev.ChannelType == channelTypeIM:
	default:
		return channel.Message{}, false
	}

	return toMessage(ev, botUserID), true
}

// toMessage converts the fields every message has.
func toMessage(ev event, botUserID string) channel.Message {
	chatType := channel.ChatGroup
	if ev.ChannelType == channelTypeIM || strings.HasPrefix(ev.Channel, directPrefix) {
		chatType = channel.ChatDirect
	}

	text, others := plainText(ev.Text, botUserID)

	msg := channel.Message{
		ID:          channel.ID(Name, channel.ScopedID(ev.Channel, ev.TS)),
		Channel:     Name,
		ChatID:      ev.Channel,
		ChatType:    chatType,
		ThreadID:    ev.ThreadTS,
		Sender:      channel.User{ID: ev.User, Mention: mentionSyntax(ev.User)},
		Text:        text,
		Mentions:    others,
		Attachments: attachments(ev.Files),
		CreatedAt:   parseTS(ev.TS),
	}

	// A message in a thread answers the thread's first message.
	if ev.ThreadTS != "" && ev.ThreadTS != ev.TS {
		msg.ReplyTo = channel.ID(Name, channel.ScopedID(ev.Channel, ev.ThreadTS))
	}

	return msg
}

// plainText drops the bot's own mention, lists the other people mentioned
// (Slack sends ids only) and undoes Slack's escaping.
func plainText(text, botUserID string) (string, []channel.User) {
	var others []channel.User

	seen := map[string]bool{}

	text = userMention.ReplaceAllStringFunc(text, func(s string) string {
		id := userMention.FindStringSubmatch(s)[1]
		if id == botUserID {
			return ""
		}

		if !seen[id] {
			seen[id] = true
			others = append(others, channel.User{ID: id, Mention: mentionSyntax(id)})
		}

		return mentionSyntax(id)
	})

	return strings.TrimSpace(unescape.Replace(text)), others
}

func attachments(files []file) []channel.Attachment {
	var out []channel.Attachment

	for _, f := range files {
		if f.ID == "" {
			continue
		}

		kind := channel.AttachmentFile
		if strings.HasPrefix(f.Mimetype, imageMIME) {
			kind = channel.AttachmentImage
		}

		out = append(out, channel.Attachment{Kind: kind, Key: f.ID, Name: f.Name})
	}

	return out
}

// parseTS reads a Slack timestamp, "<unix seconds>.<micro>".
func parseTS(ts string) time.Time {
	secs, micro, _ := strings.Cut(ts, ".")

	s, err := strconv.ParseInt(secs, 10, 64)
	if err != nil {
		return time.Time{}
	}

	us, _ := strconv.ParseInt(micro, 10, 64)

	return time.Unix(s, us*int64(time.Microsecond)).UTC()
}

func mentionSyntax(userID string) string {
	return "<@" + userID + ">"
}
