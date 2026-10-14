package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// telegramAPI is Telegram's Bot API, swapped in tests.
var telegramAPI = "https://api.telegram.org"

// Message is one notification, before a kind shapes it.
type Message struct {
	Event string
	Title string
	Body  string
	// URL is a link into Cineva; "" when the admin has set no public
	// address.
	URL string
	// High marks something that needs someone's attention now: storage
	// down, Home Assistant down, a failed scan.
	High bool
}

// sendError is a failed delivery, and whether trying again could help: a
// network error, 429 or 5xx might pass; any other 4xx means the settings
// are wrong.
type sendError struct {
	msg   string
	retry bool
}

func (e sendError) Error() string { return e.msg }

func send(ctx context.Context, client *http.Client, kind string, config map[string]string, m Message) error {
	switch kind {
	case "ntfy":
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, config["server"]+"/"+config["topic"], strings.NewReader(m.Body))
		if err != nil {
			return err
		}
		req.Header.Set("Title", m.Title)
		req.Header.Set("Tags", "clapper")
		if m.High {
			req.Header.Set("Priority", "high")
		}
		if m.URL != "" {
			req.Header.Set("Click", m.URL)
		}
		if t := config["token"]; t != "" {
			req.Header.Set("Authorization", "Bearer "+t)
		}
		return do(client, req, "ntfy")
	case "gotify":
		priority := 5
		if m.High {
			priority = 8
		}
		body := map[string]any{"title": m.Title, "message": m.Body, "priority": priority}
		if m.URL != "" {
			body["extras"] = map[string]any{"client::notification": map[string]any{"click": map[string]any{"url": m.URL}}}
		}
		return postJSON(ctx, client, config["server"]+"/message", map[string]string{"X-Gotify-Key": config["token"]}, body, "Gotify")
	case "discord":
		embed := map[string]any{"title": m.Title, "description": m.Body, "color": 0x7c5cff}
		if m.High {
			embed["color"] = 0xff6b6b
		}
		if m.URL != "" {
			embed["url"] = m.URL
		}
		return postJSON(ctx, client, config["webhookUrl"], nil,
			map[string]any{"username": "Cineva", "embeds": []any{embed}}, "Discord")
	case "telegram":
		text := m.Title
		if m.Body != "" {
			text += "\n" + m.Body
		}
		if m.URL != "" {
			text += "\n" + m.URL
		}
		return postJSON(ctx, client, telegramAPI+"/bot"+config["botToken"]+"/sendMessage", nil,
			map[string]any{"chat_id": config["chatId"], "text": text, "disable_web_page_preview": true}, "Telegram")
	case "webhook":
		var headers map[string]string
		if s := config["secret"]; s != "" {
			headers = map[string]string{"X-Cineva-Secret": s}
		}
		return postJSON(ctx, client, config["url"], headers, map[string]any{
			"event": m.Event, "title": m.Title, "body": m.Body, "url": m.URL,
			"priority": map[bool]string{true: "high", false: "normal"}[m.High],
			"sentAt":   time.Now().UTC().Format(time.RFC3339),
		}, "The webhook")
	}
	return sendError{msg: "unknown kind " + kind}
}

func postJSON(ctx context.Context, client *http.Client, url string, headers map[string]string, body any, who string) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return sendError{msg: who + ": " + err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return do(client, req, who)
}

func do(client *http.Client, req *http.Request, who string) error {
	req.Header.Set("User-Agent", "Cineva")
	res, err := client.Do(req)
	if err != nil {
		// The URL can carry a secret (a Telegram bot token, a Discord
		// webhook), so the error names the service, not the address.
		return sendError{msg: who + " could not be reached", retry: true}
	}
	defer res.Body.Close()
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
		return nil
	}
	detail, _ := io.ReadAll(io.LimitReader(res.Body, 512))
	msg := fmt.Sprintf("%s answered %s", who, res.Status)
	if d := strings.TrimSpace(string(detail)); d != "" && len(d) < 300 && !strings.ContainsAny(d, "<{") {
		msg += ": " + d
	}
	return sendError{msg: msg, retry: res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500}
}
