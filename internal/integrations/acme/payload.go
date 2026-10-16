// Package acme is the reference vendor integration: the payload types, the
// HMAC-SHA256 webhook verifier, and an outbound client. It is an example and
// can be removed with "make strip-examples CONFIRM=yes".
package acme

import (
	"encoding/json"
	"errors"
)

const maxEventIDLength = 255

// ErrInvalidEvent is returned for a webhook body that is not a usable event.
var ErrInvalidEvent = errors.New("acme: invalid event")

// Event is the webhook payload Acme sends.
type Event struct {
	ID   string          `json:"id"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// ParseEvent decodes and validates a webhook body.
func ParseEvent(body []byte) (Event, error) {
	var event Event
	if err := json.Unmarshal(body, &event); err != nil {
		return Event{}, ErrInvalidEvent
	}
	if event.ID == "" || len(event.ID) > maxEventIDLength || event.Type == "" {
		return Event{}, ErrInvalidEvent
	}
	return event, nil
}

// ExternalID returns the event's unique ID, the deduplication key.
func ExternalID(body []byte) (string, error) {
	event, err := ParseEvent(body)
	if err != nil {
		return "", err
	}
	return event.ID, nil
}
