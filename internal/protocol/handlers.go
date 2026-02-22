package protocol

import (
	"fmt"

	"github.com/steveyegge/gastown/internal/mail"
)

// Handler processes a protocol message and returns an error if processing failed.
type Handler func(msg *mail.Message) error

// HandlerRegistry maps message types to their handlers.
type HandlerRegistry struct {
	handlers map[MessageType]Handler
}

// NewHandlerRegistry creates a new handler registry.
func NewHandlerRegistry() *HandlerRegistry {
	return &HandlerRegistry{
		handlers: make(map[MessageType]Handler),
	}
}

// Register adds a handler for a specific message type.
func (r *HandlerRegistry) Register(msgType MessageType, handler Handler) {
	r.handlers[msgType] = handler
}

// Handle dispatches a message to the appropriate handler.
// Returns an error if no handler is registered for the message type.
func (r *HandlerRegistry) Handle(msg *mail.Message) error {
	msgType := ParseMessageType(msg.Subject)
	if msgType == "" {
		return fmt.Errorf("unknown message type for subject: %s", msg.Subject)
	}

	handler, ok := r.handlers[msgType]
	if !ok {
		return fmt.Errorf("no handler registered for message type: %s", msgType)
	}

	return handler(msg)
}

// CanHandle returns true if a handler is registered for the message's type.
func (r *HandlerRegistry) CanHandle(msg *mail.Message) bool {
	msgType := ParseMessageType(msg.Subject)
	if msgType == "" {
		return false
	}

	_, ok := r.handlers[msgType]
	return ok
}

// RefineryHandler defines the interface for Refinery protocol handlers.
// The Refinery receives messages about ready branches.
type RefineryHandler interface {
	// HandleMergeReady is called when a polecat's work is verified and ready.
	HandleMergeReady(payload *MergeReadyPayload) error
}

// WrapRefineryHandlers creates mail handlers from a RefineryHandler.
func WrapRefineryHandlers(h RefineryHandler) *HandlerRegistry {
	registry := NewHandlerRegistry()

	registry.Register(TypeMergeReady, func(msg *mail.Message) error {
		payload := ParseMergeReadyPayload(msg.Body)
		return h.HandleMergeReady(payload)
	})

	return registry
}

// ProcessProtocolMessage processes a protocol message using the registry.
// It returns (true, nil) if the message was handled successfully,
// (true, error) if handling failed, or (false, nil) if not a protocol message.
func (r *HandlerRegistry) ProcessProtocolMessage(msg *mail.Message) (bool, error) {
	if !IsProtocolMessage(msg.Subject) {
		return false, nil
	}

	if !r.CanHandle(msg) {
		return false, nil
	}

	err := r.Handle(msg)
	return true, err
}
