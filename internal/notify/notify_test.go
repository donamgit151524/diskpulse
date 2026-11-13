package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"diskpulse/internal/config"
	"diskpulse/internal/model"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestDispatcherToastWebhookAndTelegram(t *testing.T) {
	captured := map[string]map[string]any{}
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			var body map[string]any
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			captured[r.URL.String()] = body
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	var gotTitle, gotMsg, gotSev string
	d := NewDispatcherWithClient(func(title, msg, sev string) {
		gotTitle = title
		gotMsg = msg
		gotSev = sev
	}, client)

	cfg := config.DefaultConfig().Alerts
	cfg.EnableToast = true
	cfg.WebhookURL = "https://example.test/webhook"
	cfg.TelegramBotToken = "123:TOKEN"
	cfg.TelegramChatID = "999"

	alerts := []model.Alert{
		{
			ID:        1,
			CreatedAt: time.Now(),
			DriveRoot: `C:\`,
			RuleType:  "sudden_spike",
			Severity:  "warning",
			Message:   "Tăng đột biến +5.0 GB tại C:\\Docker",
		},
	}

	d.SendAlerts(alerts, cfg)

	if gotTitle == "" || gotMsg != alerts[0].Message || gotSev != "warning" {
		t.Errorf("unexpected toast payload: title=%q msg=%q sev=%q", gotTitle, gotMsg, gotSev)
	}
	wh := captured["https://example.test/webhook"]
	if wh == nil || wh["text"] == "" {
		t.Errorf("expected webhook JSON payload, got %+v", wh)
	}
	tg := captured["https://api.telegram.org/bot123:TOKEN/sendMessage"]
	if tg == nil || tg["chat_id"] != "999" {
		t.Errorf("expected telegram JSON payload, got %+v", tg)
	}
}
