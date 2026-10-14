// Package notify sends notifications (see docs/notifications.md): to the
// household destinations an admin sets up and to users' own, by ntfy,
// Gotify, Discord, Telegram or a webhook, from a queue so nothing that
// raises one waits for it.
package notify

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// Scope is whose a destination is: the household's (the admin's) or one
// user's.
type Scope string

const (
	Household Scope = "household"
	Personal  Scope = "personal"
)

// Event is something worth telling someone about.
type Event struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Scope Scope  `json:"scope"`
}

// Events is every event, in the order the settings pages list them.
var Events = []Event{
	{"request.waiting", "A request needs approval", Household},
	{"request.failed", "A request could not be sent to Sonarr or Radarr", Household},
	{"library.new", "New in the library", Household},
	{"storage.down", "Library storage unreachable", Household},
	{"storage.up", "Library storage back", Household},
	{"scan.failed", "A library scan failed", Household},
	{"homeassistant.down", "Home Assistant unreachable", Household},
	{"homeassistant.up", "Home Assistant back", Household},
	{"request.approved", "Your request was approved", Personal},
	{"request.declined", "Your request was declined", Personal},
	{"request.available", "Your request is ready to watch", Personal},
	{"episode.new", "New episodes of series you are watching", Personal},
}

// EventsFor lists the events of one scope.
func EventsFor(scope Scope) []Event {
	out := []Event{}
	for _, e := range Events {
		if e.Scope == scope {
			out = append(out, e)
		}
	}
	return out
}

// Field is one setting of a kind.
type Field struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Secret   bool   `json:"secret,omitempty"`
	Required bool   `json:"required,omitempty"`
	Hint     string `json:"hint,omitempty"`
}

// Kind is one way of sending: its settings, and the scopes it is open to.
// A personal destination may only reach fixed services or the ntfy server
// the admin allows, so a user cannot point the server at the network.
type Kind struct {
	ID       string  `json:"id"`
	Label    string  `json:"label"`
	Fields   []Field `json:"fields"`
	Personal bool    `json:"personal"`
}

// Kinds is every kind, for the settings pages.
var Kinds = []Kind{
	{ID: "ntfy", Label: "ntfy", Personal: true, Fields: []Field{
		{Name: "server", Label: "Server", Required: true, Hint: "https://ntfy.sh or your own"},
		{Name: "topic", Label: "Topic", Required: true, Hint: "Letters, digits, - and _; hard to guess"},
		{Name: "token", Label: "Access token", Secret: true, Hint: "Only for a protected topic"},
	}},
	{ID: "gotify", Label: "Gotify", Fields: []Field{
		{Name: "server", Label: "Server", Required: true},
		{Name: "token", Label: "Application token", Secret: true, Required: true},
	}},
	{ID: "discord", Label: "Discord", Personal: true, Fields: []Field{
		{Name: "webhookUrl", Label: "Webhook URL", Secret: true, Required: true,
			Hint: "Channel settings, Integrations, Webhooks, Copy Webhook URL"},
	}},
	{ID: "telegram", Label: "Telegram", Personal: true, Fields: []Field{
		{Name: "botToken", Label: "Bot token", Secret: true, Required: true, Hint: "From @BotFather"},
		{Name: "chatId", Label: "Chat id", Required: true, Hint: "The chat, group or @channel the bot posts to"},
	}},
	{ID: "webhook", Label: "Webhook", Fields: []Field{
		{Name: "url", Label: "URL", Required: true},
		{Name: "secret", Label: "Shared secret", Secret: true, Hint: "Sent as X-Cineva-Secret"},
	}},
}

func kindByID(id string) (Kind, bool) {
	for _, k := range Kinds {
		if k.ID == id {
			return k, true
		}
	}
	return Kind{}, false
}

// ErrInvalid is a destination the rules do not allow; its message is meant
// for the person setting it up.
type ErrInvalid struct{ Msg string }

func (e ErrInvalid) Error() string { return e.Msg }

func invalid(format string, args ...any) error { return ErrInvalid{fmt.Sprintf(format, args...)} }

var (
	topicRe    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	botTokenRe = regexp.MustCompile(`^\d+:[A-Za-z0-9_-]+$`)
	chatIDRe   = regexp.MustCompile(`^(-?\d+|@[A-Za-z0-9_]{5,})$`)
)

// discordHooks are where a Discord webhook may point.
var discordHooks = []string{"https://discord.com/api/webhooks/", "https://discordapp.com/api/webhooks/"}

func httpURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// Validate checks a destination's kind, settings and events for its scope.
// ntfyServer is the server personal ntfy destinations must use; "" means
// personal ntfy is not offered. A personal ntfy destination has its server
// set to it here.
func Validate(scope Scope, kind string, config map[string]string, events []string, ntfyServer string) error {
	k, ok := kindByID(kind)
	if !ok {
		return invalid("unknown kind %q", kind)
	}
	if scope == Personal && !k.Personal {
		return invalid("%s can only be set up by an admin", k.Label)
	}
	if scope == Personal && kind == "ntfy" {
		if ntfyServer == "" {
			return invalid("ntfy is not offered here: an admin has not chosen a server for it")
		}
		config["server"] = ntfyServer
	}
	for name := range config {
		if !slices.ContainsFunc(k.Fields, func(f Field) bool { return f.Name == name }) {
			delete(config, name)
		}
	}
	for _, f := range k.Fields {
		config[f.Name] = strings.TrimSpace(config[f.Name])
		if f.Required && config[f.Name] == "" {
			return invalid("%s needs %s", k.Label, strings.ToLower(f.Label))
		}
	}
	switch kind {
	case "ntfy":
		if !httpURL(config["server"]) {
			return invalid("the ntfy server must be an http(s) URL")
		}
		config["server"] = strings.TrimRight(config["server"], "/")
		if !topicRe.MatchString(config["topic"]) {
			return invalid("an ntfy topic is 1 to 64 letters, digits, - and _")
		}
	case "gotify":
		if !httpURL(config["server"]) {
			return invalid("the Gotify server must be an http(s) URL")
		}
		config["server"] = strings.TrimRight(config["server"], "/")
	case "discord":
		if !slices.ContainsFunc(discordHooks, func(p string) bool { return strings.HasPrefix(config["webhookUrl"], p) }) {
			return invalid("a Discord webhook URL starts with https://discord.com/api/webhooks/")
		}
	case "telegram":
		if !botTokenRe.MatchString(config["botToken"]) {
			return invalid("that does not look like a Telegram bot token (digits, a colon, then letters)")
		}
		if !chatIDRe.MatchString(config["chatId"]) {
			return invalid("a Telegram chat id is a number, or @ and a channel's name")
		}
	case "webhook":
		if !httpURL(config["url"]) {
			return invalid("the webhook URL must be http(s)")
		}
	}
	allowed := EventsFor(scope)
	for _, ev := range events {
		if !slices.ContainsFunc(allowed, func(e Event) bool { return e.ID == ev }) {
			return invalid("%q is not an event a %s destination can receive", ev, scope)
		}
	}
	return nil
}

// Mask hides a destination's secrets for the API: the last four
// characters only, so the owner can tell which one is set.
func Mask(kind string, config map[string]string) map[string]string {
	k, _ := kindByID(kind)
	out := make(map[string]string, len(config))
	for name, v := range config {
		out[name] = v
		for _, f := range k.Fields {
			if f.Name == name && f.Secret && v != "" {
				out[name] = maskValue(v)
			}
		}
	}
	return out
}

const maskPrefix = "••••"

func maskValue(v string) string {
	if len(v) <= 4 {
		return maskPrefix
	}
	return maskPrefix + v[len(v)-4:]
}

// KeepSecrets fills secrets an update left blank, or sent back masked,
// from the saved settings: a form never has the secret to send again.
func KeepSecrets(kind string, update, saved map[string]string) {
	k, _ := kindByID(kind)
	for _, f := range k.Fields {
		if !f.Secret {
			continue
		}
		if v := update[f.Name]; v == "" || strings.HasPrefix(v, maskPrefix) {
			update[f.Name] = saved[f.Name]
		}
	}
}
