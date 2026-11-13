package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"diskpulse/internal/config"
	"diskpulse/internal/model"
)

// ToastFunc displays a native desktop notification (provided by the system tray on Windows).
type ToastFunc func(title, message, severity string)

// Dispatcher sends generated alerts to Windows notifications and optional webhooks.
type Dispatcher struct {
	toast ToastFunc
	http  *http.Client
}

// NewDispatcher creates a Dispatcher using toastFn for local desktop alerts.
func NewDispatcher(toastFn ToastFunc) *Dispatcher {
	return NewDispatcherWithClient(toastFn, nil)
}

// NewDispatcherWithClient creates a Dispatcher with an injectable HTTP client (useful for tests).
func NewDispatcherWithClient(toastFn ToastFunc, client *http.Client) *Dispatcher {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Dispatcher{
		toast: toastFn,
		http:  client,
	}
}

// SendAlerts delivers alerts according to cfg.
func (d *Dispatcher) SendAlerts(alerts []model.Alert, cfg config.AlertConfig) {
	if len(alerts) == 0 {
		return
	}

	if cfg.EnableToast && d.toast != nil {
		if len(alerts) == 1 {
			a := alerts[0]
			d.toast(titleForSeverity(a.Severity), a.Message, a.Severity)
		} else {
			sev := "warning"
			for _, a := range alerts {
				if a.Severity == "critical" {
					sev = "critical"
					break
				}
			}
			d.toast(
				titleForSeverity(sev),
				fmt.Sprintf("Phát hiện %d cảnh báo dung lượng mới. Bấm để xem chi tiết trên Dashboard.", len(alerts)),
				sev,
			)
		}
	}

	if strings.TrimSpace(cfg.WebhookURL) != "" {
		d.sendWebhook(cfg.WebhookURL, alerts)
	}
	if strings.TrimSpace(cfg.TelegramBotToken) != "" && strings.TrimSpace(cfg.TelegramChatID) != "" {
		d.sendTelegram(cfg.TelegramBotToken, cfg.TelegramChatID, alerts)
	}
}

func titleForSeverity(severity string) string {
	if severity == "critical" {
		return "DiskPulse — Cảnh báo khẩn cấp"
	}
	return "DiskPulse — Cảnh báo dung lượng"
}

func (d *Dispatcher) sendWebhook(webhookURL string, alerts []model.Alert) {
	var lines []string
	for _, a := range alerts {
		lines = append(lines, fmt.Sprintf("[%s] %s", strings.ToUpper(a.Severity), a.Message))
	}
	payload := map[string]any{
		"content": "DiskPulse Alert:\n" + strings.Join(lines, "\n"),
		"text":    "DiskPulse Alert:\n" + strings.Join(lines, "\n"),
		"alerts":  alerts,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.http.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
}

func (d *Dispatcher) sendTelegram(botToken, chatID string, alerts []model.Alert) {
	var lines []string
	lines = append(lines, "DiskPulse — Cảnh báo dung lượng:")
	for _, a := range alerts {
		lines = append(lines, fmt.Sprintf("• [%s] %s", strings.ToUpper(a.Severity), a.Message))
	}
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", strings.TrimSpace(botToken))
	payload := map[string]string{
		"chat_id": strings.TrimSpace(chatID),
		"text":    strings.Join(lines, "\n"),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.http.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
}
