package telegram

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/hewenyu/claude-bot/internal/channel"
)

const (
	idSep         = "/" // between chat and message in a platform id
	imageMIME     = "image/"
	mentionMark   = "@"
	chatPrivate   = "private"
	chatGroup     = "group"
	chatSuper     = "supergroup"
	entityMention = "mention"      // @username
	entityTextRef = "text_mention" // a user without a username, by id
	entityCommand = "bot_command"  // /cmd or /cmd@bot
	maxQuoteLen   = 500            // characters of a quoted message kept
	quoteMark     = "> "
	attachmentTag = "[attachment]" // stands for a quoted message without text
)

type update struct {
	UpdateID int64    `json:"update_id"`
	Message  *message `json:"message"`
}

type user struct {
	ID                      int64  `json:"id"`
	IsBot                   bool   `json:"is_bot"`
	FirstName               string `json:"first_name"`
	LastName                string `json:"last_name"`
	Username                string `json:"username"`
	CanReadAllGroupMessages bool   `json:"can_read_all_group_messages"`
}

func (u *user) name() string {
	return strings.TrimSpace(u.FirstName + " " + u.LastName)
}

type chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type entity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"` // in UTF-16 code units
	Length int    `json:"length"`
	User   *user  `json:"user"`
}

type photoSize struct {
	FileID string `json:"file_id"`
}

type document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
}

type message struct {
	MessageID       int64       `json:"message_id"`
	MessageThreadID int64       `json:"message_thread_id"`
	IsTopicMessage  bool        `json:"is_topic_message"`
	From            *user       `json:"from"`
	Chat            chat        `json:"chat"`
	Date            int64       `json:"date"`
	Text            string      `json:"text"`
	Entities        []entity    `json:"entities"`
	Caption         string      `json:"caption"`
	CaptionEntities []entity    `json:"caption_entities"`
	ReplyToMessage  *message    `json:"reply_to_message"`
	Photo           []photoSize `json:"photo"`
	Document        *document   `json:"document"`
	Video           *document   `json:"video"`
	Audio           *document   `json:"audio"`
	Voice           *document   `json:"voice"`
}

// body is the text of a message, or the caption of a media message.
func (m *message) body() (string, []entity) {
	if m.Text != "" {
		return m.Text, m.Entities
	}

	return m.Caption, m.CaptionEntities
}

// repliedTo is the message m really replies to. In forum topics every
// message points at the topic's first message, which is not a reply.
func (m *message) repliedTo() *message {
	r := m.ReplyToMessage
	if r == nil || m.IsTopicMessage && r.MessageID == m.MessageThreadID {
		return nil
	}

	return r
}

// fromMessage converts a received message; ok is false for messages that are
// not addressed to the bot.
func fromMessage(m *message, bot Bot) (channel.Message, bool) {
	if m == nil || m.From == nil || m.From.IsBot {
		return channel.Message{}, false
	}

	text, entities := m.body()

	var chatType channel.ChatType

	switch m.Chat.Type {
	case chatPrivate:
		chatType = channel.ChatDirect
	case chatGroup, chatSuper:
		chatType = channel.ChatGroup

		r := m.repliedTo()
		if !mentions(text, entities, bot) && (r == nil || r.From == nil || r.From.ID != bot.ID) {
			return channel.Message{}, false // group chatter not addressed to the bot
		}
	default:
		return channel.Message{}, false
	}

	plain, others := plainText(text, entities, bot)
	if quote := quoted(m.repliedTo()); quote != "" {
		plain = strings.TrimSpace(quote + "\n\n" + plain)
	}

	msg := channel.Message{
		ID:          channel.ID(Name, platformID(m.Chat.ID, m.MessageID)),
		Channel:     Name,
		ChatID:      strconv.FormatInt(m.Chat.ID, 10),
		ChatType:    chatType,
		Sender:      toUser(m.From),
		Text:        plain,
		Mentions:    others,
		Attachments: attachments(m),
		CreatedAt:   time.Unix(m.Date, 0).UTC(),
	}

	if m.IsTopicMessage {
		msg.ThreadID = strconv.FormatInt(m.MessageThreadID, 10)
	}

	return msg, true
}

// mentions reports whether the bot is @-mentioned, by username or (for
// commands) as /cmd@bot.
func mentions(text string, entities []entity, bot Bot) bool {
	u := utf16.Encode([]rune(text))

	for _, e := range entities {
		if isBotMention(e, slice(u, e), bot) {
			return true
		}
	}

	return false
}

func isBotMention(e entity, s string, bot Bot) bool {
	switch e.Type {
	case entityMention:
		return strings.EqualFold(s, mentionMark+bot.Username)
	case entityTextRef:
		return e.User != nil && e.User.ID == bot.ID
	case entityCommand:
		_, to, ok := strings.Cut(s, mentionMark)

		return ok && strings.EqualFold(to, bot.Username)
	}

	return false
}

// plainText drops the bot's own mentions (keeping the command of /cmd@bot)
// and lists the other people mentioned.
func plainText(text string, entities []entity, bot Bot) (string, []channel.User) {
	u := utf16.Encode([]rune(text))

	var (
		b      strings.Builder
		others []channel.User
		pos    int
	)

	for _, e := range entities {
		s := slice(u, e)
		if s == "" || e.Offset < pos {
			continue // out of range, or inside a mention already dropped
		}

		switch {
		case isBotMention(e, s, bot):
			b.WriteString(string(utf16.Decode(u[pos:e.Offset])))

			if e.Type == entityCommand {
				cmd, _, _ := strings.Cut(s, mentionMark)
				b.WriteString(cmd)
			}

			pos = e.Offset + e.Length
		case e.Type == entityMention:
			name := strings.TrimPrefix(s, mentionMark)
			others = append(others, channel.User{Name: name, Mention: s})
		case e.Type == entityTextRef && e.User != nil:
			others = append(others, toUser(e.User))
		}
	}

	b.WriteString(string(utf16.Decode(u[pos:])))

	return strings.TrimSpace(b.String()), others
}

// slice is the text an entity covers; "" if the entity is out of range.
func slice(u []uint16, e entity) string {
	if e.Offset < 0 || e.Length < 0 || e.Offset+e.Length > len(u) {
		return ""
	}

	return string(utf16.Decode(u[e.Offset : e.Offset+e.Length]))
}

// quoted renders the message replied to as a Markdown quote, since the Bot
// API offers no way to fetch it later.
func quoted(r *message) string {
	if r == nil {
		return ""
	}

	text, _ := r.body()
	if text == "" && len(attachments(r)) > 0 {
		text = attachmentTag
	}

	if text == "" {
		return ""
	}

	if runes := []rune(text); len(runes) > maxQuoteLen {
		text = string(runes[:maxQuoteLen]) + "…"
	}

	if r.From != nil {
		text = r.From.name() + ": " + text
	}

	return quoteMark + strings.ReplaceAll(text, "\n", "\n"+quoteMark)
}

func attachments(m *message) []channel.Attachment {
	var out []channel.Attachment

	if n := len(m.Photo); n > 0 {
		// Sizes come smallest first.
		out = append(out, channel.Attachment{Kind: channel.AttachmentImage, Key: m.Photo[n-1].FileID})
	}

	for _, d := range []*document{m.Document, m.Video, m.Audio, m.Voice} {
		if d == nil || d.FileID == "" {
			continue
		}

		kind := channel.AttachmentFile
		if strings.HasPrefix(d.MimeType, imageMIME) {
			kind = channel.AttachmentImage
		}

		out = append(out, channel.Attachment{Kind: kind, Key: d.FileID, Name: d.FileName})
	}

	return out
}

func toUser(u *user) channel.User {
	return channel.User{ID: strconv.FormatInt(u.ID, 10), Name: u.name(), Mention: mentionSyntax(u)}
}

// mentionSyntax is @username, or for people without one a Markdown link that
// post renders as a text mention.
func mentionSyntax(u *user) string {
	if u.Username != "" {
		return mentionMark + u.Username
	}

	return fmt.Sprintf("[%s](tg://user?id=%d)", strings.NewReplacer("[", "(", "]", ")").Replace(u.name()), u.ID)
}

func platformID(chatID, messageID int64) string {
	return strconv.FormatInt(chatID, 10) + idSep + strconv.FormatInt(messageID, 10)
}

// locate returns the chat and message id of a protocol message id.
func locate(id string) (chatID string, messageID int64, err error) {
	_, platform, err := channel.SplitID(id)
	if err != nil {
		return "", 0, err
	}

	chatID, msg, ok := strings.Cut(platform, idSep)
	if !ok || chatID == "" {
		return "", 0, fmt.Errorf("%w %q: want <chat id>%s<message id>", channel.ErrInvalidID, platform, idSep)
	}

	messageID, err = strconv.ParseInt(msg, 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("%w %q: want <chat id>%s<message id>", channel.ErrInvalidID, platform, idSep)
	}

	return chatID, messageID, nil
}
