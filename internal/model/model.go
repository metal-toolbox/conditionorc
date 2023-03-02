package model

// AppKind defines the types of application this can be.
type AppKind string

// StoreKind defines the types of condition storage kinds.
type StoreKind string

// LogLevel is the logging level string.
type LogLevel string

type EventBrokerKind string

const (
	AppName string = "conditionorc"

	// AppKindOrchestrator identifies a condition orchestrator application.
	AppKindOrchestrator AppKind = "orchestrator"

	// AppKindServer identifies a condition API service.
	AppKindServer AppKind = "server"

	// ServerserviceStore identifies a serverservice store.
	ServerserviceStore StoreKind = "serverservice"

	// NatsEventsBroker identifies a NATS events broker.
	NatsEventBroker EventBrokerKind = "nats"

	LogLevelInfo  LogLevel = "info"
	LogLevelDebug LogLevel = "debug"
	LogLevelTrace LogLevel = "trace"
)
