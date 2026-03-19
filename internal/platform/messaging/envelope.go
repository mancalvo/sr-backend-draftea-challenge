// Package messaging provides common message envelope types and routing key
// constants for RabbitMQ-based async communication between services.
package messaging

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Envelope is the standard wrapper for all async messages published to RabbitMQ.
type Envelope struct {
	// MessageID is a unique identifier for this message instance.
	MessageID string `json:"message_id"`

	// CorrelationID links this message to a broader request flow.
	CorrelationID string `json:"correlation_id"`

	// Type identifies the kind of message (e.g. routing key).
	Type string `json:"type"`

	// Timestamp is when the message was created.
	Timestamp time.Time `json:"timestamp"`

	// Payload is the JSON-encoded business data.
	Payload json.RawMessage `json:"payload"`
}

// NewEnvelope creates a new message envelope with a generated message ID
// and the current timestamp.
func NewEnvelope(msgType, correlationID string, payload any) (Envelope, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal payload: %w", err)
	}
	return Envelope{
		MessageID:     uuid.New().String(),
		CorrelationID: correlationID,
		Type:          msgType,
		Timestamp:     time.Now().UTC(),
		Payload:       data,
	}, nil
}

// DecodePayload unmarshals the envelope payload into the given destination.
func (e Envelope) DecodePayload(dst any) error {
	return json.Unmarshal(e.Payload, dst)
}

// Marshal serializes the envelope to JSON bytes for publishing.
func (e Envelope) Marshal() ([]byte, error) {
	return json.Marshal(e)
}

// UnmarshalEnvelope deserializes JSON bytes into an Envelope.
func UnmarshalEnvelope(data []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Envelope{}, fmt.Errorf("unmarshal envelope: %w", err)
	}
	return env, nil
}
