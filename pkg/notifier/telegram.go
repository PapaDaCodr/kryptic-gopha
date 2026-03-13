package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// CommandHandler is a callback for a parsed Telegram command.
type CommandHandler func(command string, args []string) string

// Notifier defines the interface for sending alerts.
type Notifier interface {
	Notify(message string)
	// StartListening polls for incoming commands until ctx is cancelled.
	StartListening(ctx context.Context, handler CommandHandler)
}

// longPollTimeout is the server-side wait duration passed to Telegram getUpdates.
// The pollClient timeout must exceed this value.
const longPollTimeout = 30

// TelegramNotifier sends messages via a Telegram Bot.
type TelegramNotifier struct {
	Token   string
	ChatID  string
	apiBase string // base URL; defaults to https://api.telegram.org
	// client is used for short outbound requests (sendMessage, etc.).
	client *http.Client
	// pollClient is used exclusively for long-poll getUpdates calls.
	// Its timeout must exceed longPollTimeout to avoid cutting responses short.
	pollClient *http.Client
	updateID   int
}

func NewTelegramNotifier(token, chatID string) *TelegramNotifier {
	return &TelegramNotifier{
		Token:      token,
		ChatID:     chatID,
		apiBase:    "https://api.telegram.org",
		client:     &http.Client{Timeout: 5 * time.Second},
		pollClient: &http.Client{Timeout: (longPollTimeout + 5) * time.Second},
	}
}

// Notify sends a message asynchronously to avoid blocking the trading goroutine.
func (t *TelegramNotifier) Notify(message string) {
	if t.Token == "" || t.ChatID == "" {
		return
	}

	go func() {
		url := fmt.Sprintf("%s/bot%s/sendMessage", t.apiBase, t.Token)
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

// StartListening polls Telegram for commands until ctx is cancelled.
func (t *TelegramNotifier) StartListening(ctx context.Context, handler CommandHandler) {
	if t.Token == "" {
		return
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				log.Info().Msg("Telegram listener shutting down")
				return
			default:
			}

			url := fmt.Sprintf(
				"%s/bot%s/getUpdates?offset=%d&timeout=%d",
				t.apiBase, t.Token, t.updateID+1, longPollTimeout,
			)

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return // ctx was cancelled while building the request
			}

			resp, err := t.pollClient.Do(req)
			if err != nil {
				if ctx.Err() != nil {
					return // shutdown in progress
				}
				log.Warn().Err(err).Msg("Telegram poll error, retrying in 5s")
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Second):
				}
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

			decodeErr := json.NewDecoder(resp.Body).Decode(&result)
			resp.Body.Close()

			if decodeErr != nil {
				log.Warn().Err(decodeErr).Msg("Telegram poll decode error")
				continue
			}
			if !result.Ok {
				continue
			}

			for _, update := range result.Result {
				t.updateID = update.UpdateID

				msgChatID := fmt.Sprintf("%.0f", update.Message.Chat.ID)

				// Only process commands from the authorised chat ID.
				if msgChatID == t.ChatID && len(update.Message.Text) > 0 && update.Message.Text[0] == '/' {
					parts := strings.Fields(update.Message.Text)
					command := parts[0]
					args := parts[1:]

					if response := handler(command, args); response != "" {
						t.Notify(response)
					}
				}
			}
		}
	}()
}
