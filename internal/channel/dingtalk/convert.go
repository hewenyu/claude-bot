package dingtalk

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hewenyu/claude-bot/internal/channel"
)

// conversationGroup is the conversationType of a group; "1" is a direct chat.
const conversationGroup = "2"

// Message types (msgtype).
const (
	msgText     = "text"
	msgRichText = "richText"
	msgPicture  = "picture"
	msgFile     = "file"
	msgAudio    = "audio"
	msgVideo    = "video"
)

const (
	richPicture = "picture" // type of an image in rich text
	mentionMark = "@"
)

// robotMessage is a message pushed to the robot over Stream mode.
type robotMessage struct {
	MsgID            string          `json:"msgId"`
	MsgType          string          `json:"msgtype"`
	ConversationID   string          `json:"conversationId"`
	ConversationType string          `json:"conversationType"`
	SenderID         string          `json:"senderId"`
	SenderStaffID    string          `json:"senderStaffId"` // empty for people outside the organization
	SenderNick       string          `json:"senderNick"`
	ChatbotUserID    string          `json:"chatbotUserId"`
	IsInAtList       bool            `json:"isInAtList"`
	AtUsers          []atUser        `json:"atUsers"`
	CreateAt         int64           `json:"createAt"` // Unix milliseconds
	Text             textContent     `json:"text"`
	Content          json.RawMessage `json:"content"`
}

type atUser struct {
	DingtalkID string `json:"dingtalkId"`
	StaffID    string `json:"staffId"`
}

type textContent struct {
	Content string `json:"content"`
}

// content is what non-text messages carry.
type content struct {
	DownloadCode string     `json:"downloadCode"`
	FileName     string     `json:"fileName"`
	Recognition  string     `json:"recognition"` // speech to text of a voice message
	RichText     []richPart `json:"richText"`
}

type richPart struct {
	Text         string `json:"text"`
	Type         string `json:"type"`
	DownloadCode string `json:"downloadCode"`
}

// fromRobotMessage converts a pushed message; ok is false for messages that
// are not for the bot.
func fromRobotMessage(m robotMessage) (channel.Message, bool) {
	if m.MsgID == "" || m.ConversationID == "" {
		return channel.Message{}, false
	}

	chatType := channel.ChatDirect

	if m.ConversationType == conversationGroup {
		if !m.IsInAtList {
			return channel.Message{}, false
		}

		chatType = channel.ChatGroup
	}

	text, atts := body(m)

	sender := m.SenderStaffID
	if sender == "" {
		sender = m.SenderID
	}

	msg := channel.Message{
		ID:          channel.ID(Name, channel.ScopedID(m.ConversationID, m.MsgID)),
		Channel:     Name,
		ChatID:      m.ConversationID,
		ChatType:    chatType,
		Sender:      channel.User{ID: sender, Name: m.SenderNick, Mention: mentionOf(m.SenderNick)},
		Text:        text,
		Attachments: atts,
		CreatedAt:   time.UnixMilli(m.CreateAt).UTC(),
	}

	for _, u := range m.AtUsers {
		if u.DingtalkID != m.ChatbotUserID && u.StaffID != "" {
			msg.Mentions = append(msg.Mentions, channel.User{ID: u.StaffID})
		}
	}

	return msg, true
}

// mentionOf names a person in a reply. The OpenAPI cannot @-mention, so this
// is plain text that does not notify.
func mentionOf(nick string) string {
	if nick == "" {
		return ""
	}

	return mentionMark + nick
}

// body reads the text and attachments of a message by its type.
func body(m robotMessage) (string, []channel.Attachment) {
	if m.MsgType == msgText || m.MsgType == "" {
		return strings.TrimSpace(m.Text.Content), nil
	}

	var ct content

	_ = json.Unmarshal(m.Content, &ct) // an unreadable content leaves an empty message

	var atts []channel.Attachment

	// Attachments without a file name are numbered, so the files of one
	// message do not overwrite each other; Download adds the extension.
	attach := func(kind channel.AttachmentKind, code, name string) {
		if code == "" {
			return
		}

		if name = channel.BaseName(name); name == "" {
			name = fmt.Sprintf("%s-%d", kind, len(atts)+1)
		}

		atts = append(atts, channel.Attachment{Kind: kind, Key: code, Name: name})
	}

	var text string

	switch m.MsgType {
	case msgRichText:
		var b strings.Builder

		for _, p := range ct.RichText {
			b.WriteString(p.Text)

			if p.Type == richPicture {
				attach(channel.AttachmentImage, p.DownloadCode, "")
			}
		}

		text = b.String()
	case msgPicture:
		attach(channel.AttachmentImage, ct.DownloadCode, "")
	case msgFile:
		attach(channel.AttachmentFile, ct.DownloadCode, ct.FileName)
	case msgAudio:
		text = ct.Recognition
		attach(channel.AttachmentFile, ct.DownloadCode, "")
	case msgVideo:
		attach(channel.AttachmentFile, ct.DownloadCode, "")
	default:
		text = "[" + m.MsgType + " message]"
	}

	return strings.TrimSpace(text), atts
}
