package discord

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/bwmarrin/discordgo"

	"github.com/hewenyu/claude-bot/internal/channel"
)

const (
	idSep       = "/"      // between channel and message in a platform id
	imageMIME   = "image/" // content type prefix of images
	mentionMark = "@"      // shown before a mentioned person's name in text
)

// userMention matches <@id> and the legacy nickname form <@!id>.
var userMention = regexp.MustCompile(`<@!?(\d+)>`)

// fromEvent converts a received message; ok is false for messages that are
// not addressed to the bot.
func fromEvent(m *discordgo.Message, botID string, inThread bool) (channel.Message, bool) {
	if m == nil || m.Author == nil || m.Author.Bot || m.WebhookID != "" {
		return channel.Message{}, false
	}

	if m.Type != discordgo.MessageTypeDefault && m.Type != discordgo.MessageTypeReply {
		return channel.Message{}, false // joins, pins and other system messages
	}

	chatType := channel.ChatDirect
	if m.GuildID != "" {
		chatType = channel.ChatGroup

		if !mentions(m, botID) {
			return channel.Message{}, false // server chatter not addressed to the bot
		}
	}

	msg := fromMessage(m, botID)
	msg.ChatType = chatType

	if inThread {
		msg.ThreadID = m.ChannelID
	}

	return msg, true
}

func mentions(m *discordgo.Message, botID string) bool {
	for _, u := range m.Mentions {
		if u != nil && u.ID == botID {
			return true
		}
	}

	return false
}

// fromMessage converts the fields every message has. Chat and thread type are
// only known from Gateway events, so they are left to fromEvent.
func fromMessage(m *discordgo.Message, botID string) channel.Message {
	var others []channel.User

	names := map[string]string{}

	for _, u := range m.Mentions {
		if u == nil || u.ID == botID {
			continue
		}

		names[u.ID] = displayName(u)
		others = append(others, user(u))
	}

	var sender channel.User
	if m.Author != nil {
		sender = user(m.Author)
	}

	return channel.Message{
		ID:          channel.ID(Name, platformID(m.ChannelID, m.ID)),
		Channel:     Name,
		ChatID:      m.ChannelID,
		ReplyTo:     replyTo(m),
		Sender:      sender,
		Text:        plainText(m.Content, botID, names),
		Mentions:    others,
		Attachments: attachments(m.Attachments),
		CreatedAt:   m.Timestamp.UTC(),
	}
}

// plainText drops the bot's own mention and shows other known mentions as
// @name, leaving unknown ones as Discord wrote them.
func plainText(content, botID string, names map[string]string) string {
	text := userMention.ReplaceAllStringFunc(content, func(s string) string {
		id := userMention.FindStringSubmatch(s)[1]
		if id == botID {
			return ""
		}

		if name, ok := names[id]; ok {
			return mentionMark + name
		}

		return s
	})

	return strings.TrimSpace(text)
}

func attachments(in []*discordgo.MessageAttachment) []channel.Attachment {
	var out []channel.Attachment

	for _, a := range in {
		if a == nil {
			continue
		}

		kind := channel.AttachmentFile
		if strings.HasPrefix(a.ContentType, imageMIME) {
			kind = channel.AttachmentImage
		}

		out = append(out, channel.Attachment{Kind: kind, Key: a.ID, Name: a.Filename})
	}

	return out
}

// replyTo is the id of the message m answers, if it is a reply.
func replyTo(m *discordgo.Message) string {
	ref := m.MessageReference
	if ref == nil || ref.MessageID == "" || m.Type != discordgo.MessageTypeReply {
		return ""
	}

	ch := ref.ChannelID
	if ch == "" {
		ch = m.ChannelID
	}

	return channel.ID(Name, platformID(ch, ref.MessageID))
}

func user(u *discordgo.User) channel.User {
	return channel.User{ID: u.ID, Name: displayName(u), Mention: mentionSyntax(u.ID)}
}

func displayName(u *discordgo.User) string {
	if u.GlobalName != "" {
		return u.GlobalName
	}

	return u.Username
}

func mentionSyntax(userID string) string {
	return "<@" + userID + ">"
}

func platformID(channelID, messageID string) string {
	return channelID + idSep + messageID
}

// splitPlatformID returns the channel and message id of a platform id.
func splitPlatformID(id string) (channelID, messageID string, err error) {
	channelID, messageID, ok := strings.Cut(id, idSep)
	if !ok || channelID == "" || messageID == "" {
		return "", "", fmt.Errorf("%w %q: want <channel id>%s<message id>", channel.ErrInvalidID, id, idSep)
	}

	return channelID, messageID, nil
}

// locate returns the channel and message id of a protocol message id.
func locate(id string) (channelID, messageID string, err error) {
	_, platform, err := channel.SplitID(id)
	if err != nil {
		return "", "", err
	}

	return splitPlatformID(platform)
}
