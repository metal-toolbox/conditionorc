package model

import "github.com/google/uuid"

// AppKind defines the types of application this can be.
type AppKind string

// StoreKind defines the types of condition storage kinds.
type StoreKind string

// LogLevel is the logging level string.
type LogLevel string

type EventBrokerKind string

// Server is a server asset queried from the store.
type Server struct {
	ID           uuid.UUID
	FacilityCode string
}

const (
	AppName string = "conditionorc"

	// AppKindOrchestrator identifies a condition orchestrator application.
	AppKindOrchestrator AppKind = "orchestrator"

	// AppKindServer identifies a condition API service.
	AppKindServer AppKind = "server"

	// NATS is a NATS kv-based store
	NATS StoreKind = "nats"

	// NatsEventsBroker identifies a NATS events broker.
	NatsEventBroker EventBrokerKind = "nats"

	LogLevelInfo  LogLevel = "info"
	LogLevelDebug LogLevel = "debug"
	LogLevelTrace LogLevel = "trace"
)
