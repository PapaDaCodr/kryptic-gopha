package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// CommandHandler is a callback for a parsed Telegram command
type CommandHandler func(command string, args []string) string

// Notifier defines the interface for sending alerts
type Notifier interface {
	Notify(message string)
	StartListening(handler CommandHandler)
}

// TelegramNotifier sends messages via a Telegram Bot
type TelegramNotifier struct {
	Token    string
	ChatID   string
	client   *http.Client
	updateID int
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

// StartListening starts a background goroutine to long-poll Telegram for commands
func (t *TelegramNotifier) StartListening(handler CommandHandler) {
	if t.Token == "" {
		return
	}

	go func() {
		for {
			url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=30", t.Token, t.updateID+1)
			
			resp, err := t.client.Get(url)
			if err != nil {
				time.Sleep(5 * time.Second)
				continue
			}

			var result struct {
				Ok     bool `json:"ok"`
				Result []struct {
					UpdateID int `json:"update_id"`
					Message  struct {
						Chat struct {
							ID float64 `json:"id"`
						} `json:"chat"`
						Text string `json:"text"`
					} `json:"message"`
				} `json:"result"`
			}
			
			if err := json.NewDecoder(resp.Body).Decode(&result); err == nil && result.Ok {
				for _, update := range result.Result {
					t.updateID = update.UpdateID
					
					// Convert ChatID to string for comparison
					msgChatID := fmt.Sprintf("%.0f", update.Message.Chat.ID)
					
					// Only process messages from the authorized ChatID starting with /
					if msgChatID == t.ChatID && len(update.Message.Text) > 0 && update.Message.Text[0] == '/' {
						parts := strings.Fields(update.Message.Text)
						command := parts[0]
						args := parts[1:]
						
						response := handler(command, args)
						if response != "" {
							t.Notify(response)
						}
					}
				}
			}
			resp.Body.Close()
		}
	}()
}
