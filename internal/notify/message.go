package notify

import (
	"strings"
	"unicode/utf8"

	"github.com/back-to-code/sante/internal/config"
	"github.com/back-to-code/sante/internal/store"
)

const (
	downColor       = "#dc3b3b"
	maxMessageRunes = 500
)

type message struct {
	Text        string       `json:"text"`
	Channel     string       `json:"channel,omitempty"`
	Username    string       `json:"username,omitempty"`
	Attachments []attachment `json:"attachments,omitempty"`
}

type attachment struct {
	Fallback string   `json:"fallback"`
	Color    string   `json:"color"`
	Text     string   `json:"text"`
	Fields   []field  `json:"fields,omitempty"`
	Footer   string   `json:"footer,omitempty"`
	MrkdwnIn []string `json:"mrkdwn_in"`
}

type field struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short"`
}

func downMessage(w config.Webhook, title string, m config.Monitor, r store.Result) message {
	f := formatterFor(w.Type)
	msg := message{
		Text:     ":red_circle: " + f.bold(m.Name) + " is down",
		Channel:  w.Channel,
		Username: w.Username,
	}
	reason := truncate(r.Message, maxMessageRunes)
	if w.Type == config.Mattermost {
		// Mattermost's search skips attachments.
		details := "**Type:** " + m.Type.Label()
		if m.Group != "" {
			details = "**Group:** " + f.text(m.Group) + " · " + details
		}
		msg.Text = strings.Join([]string{msg.Text, f.code(reason), details, "_" + f.text(title) + "_"}, "\n")
		return msg
	}
	a := attachment{
		Fallback: m.Name + " is down: " + reason,
		Color:    downColor,
		Text:     f.code(reason),
		Footer:   f.plain(title),
		MrkdwnIn: []string{"text"},
	}
	if m.Group != "" {
		a.Fields = append(a.Fields, field{Title: "Group", Value: f.text(m.Group), Short: true})
	}
	a.Fields = append(a.Fields, field{Title: "Type", Value: m.Type.Label(), Short: true})
	msg.Attachments = []attachment{a}
	return msg
}

type formatter struct {
	slack bool
}

func formatterFor(t config.WebhookType) formatter {
	return formatter{slack: t == config.Slack}
}

// Slack reads <...> as links and mentions, and asks for exactly these three
// to be escaped, anywhere in the text.
var slackEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// Mattermost renders Markdown and turns Slack-style <url|text> into links.
var markdownEscaper = strings.NewReplacer(
	`\`, `\\`, "*", `\*`, "_", `\_`, "~", `\~`, "`", "\\`", "[", `\[`, "]", `\]`, "<", `\<`, ">", `\>`)

func (f formatter) plain(s string) string {
	if f.slack {
		return slackEscaper.Replace(s)
	}
	return s
}

func (f formatter) text(s string) string {
	if f.slack {
		return slackEscaper.Replace(s)
	}
	return markdownEscaper.Replace(s)
}

func (f formatter) bold(s string) string {
	if f.slack {
		return "*" + f.text(s) + "*"
	}
	return "**" + f.text(s) + "**"
}

// code keeps text that a checked target controls, such as an HTTP status
// line, from being read as formatting or as an @channel mention. Markdown
// shows entities in code spans verbatim, so only Slack's are escaped.
func (f formatter) code(s string) string {
	s = strings.NewReplacer("`", "'", "\r", "", "\n", " ").Replace(s)
	return "`" + f.plain(s) + "`"
}

func truncate(s string, runes int) string {
	if utf8.RuneCountInString(s) <= runes {
		return s
	}
	return string([]rune(s)[:runes-1]) + "…"
}
