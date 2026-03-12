package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

// Notifier defines the interface for sending alerts
type Notifier interface {
	Notify(message string)
}

// TelegramNotifier sends messages via a Telegram Bot
type TelegramNotifier struct {
	Token  string
	ChatID string
	client *http.Client
}

func NewTelegramNotifier(token, chatID string) *TelegramNotifier {
	return &TelegramNotifier{
		Token:  token,
		ChatID: chatID,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

// Notify sends a message asynchronously to not block the trading thread
func (t *TelegramNotifier) Notify(message string) {
	if t.Token == "" || t.ChatID == "" {
		return
	}

	go func() {
		url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.Token)
		payload := map[string]string{
			"chat_id":    t.ChatID,
			"text":       message,
			"parse_mode": "Markdown",
		}

		body, _ := json.Marshal(payload)
		resp, err := t.client.Post(url, "application/json", bytes.NewBuffer(body))
		if err != nil {
			log.Error().Err(err).Msg("Failed to send Telegram notification")
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			log.Warn().Int("status", resp.StatusCode).Msg("Telegram API returned non-OK status")
		}
	}()
}
