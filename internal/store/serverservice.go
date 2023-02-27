package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/app"
	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	sservice "go.hollow.sh/serverservice/pkg/api/v1"
)

// Serverservice implements the Repository interface to have server condition objects stored as server attributes.
type Serverservice struct {
	config *app.ServerserviceOptions
	client *sservice.Client
	logger *logrus.Logger
}

var (
	// ErrServerserviceConfig is returned when theres an error in loading serverservice configuration.
	ErrServerserviceConfig = errors.New("Serverservice configuration error")

	ErrServerserviceQuery = errors.New("Serverservice query error")

	ErrServerserviceAttributes = errors.New("error in serverservice attribute")

	// ServerserviceConditionsNSFmtStr attribute namespace format string value for server condition attributes.
	ServerserviceConditionsNSFmtStr = "sh.hollow.condition.%s"
)

func newServerserviceStore(config *app.ServerserviceOptions, logger *logrus.Logger) (Repository, error) {
	s := &Serverservice{logger: logger, config: config}

	// Setup client
	// TODO: add helper method for OIDC auth
	client, err := sservice.NewClientWithToken("fake", config.Endpoint, nil)
	if err != nil {
		return nil, err
	}

	s.client = client

	return s, nil
}

// Ping tests the repository is available.
func (s *Serverservice) Ping(ctx context.Context) error {
	// TODO: implement
	return nil
}

// Get a condition set on a server.
// @id: required
// @conditionKind: required
func (s *Serverservice) Get(ctx context.Context, id uuid.UUID, conditionKind ptypes.ConditionKind) (*ptypes.Condition, error) {
	// list attributes on a server
	attributes, _, err := s.client.ListAttributes(ctx, id, nil)
	if err != nil {
		return nil, errors.Wrap(ErrServerserviceQuery, err.Error())
	}

	// return condition object from attribute
	return s.conditionByKindFromAttributes(attributes, conditionKind)
}

// List all conditions set on a server.
// @id: required
// @conditionState: optional
func (s *Serverservice) List(ctx context.Context, id uuid.UUID, conditionState ptypes.ConditionState) ([]*ptypes.Condition, error) {
	// list attributes on a server
	attributes, _, err := s.client.ListAttributes(ctx, id, nil)
	if err != nil {
		return nil, errors.Wrap(ErrServerserviceQuery, err.Error())
	}

	return s.conditionByStateFromAttributes(attributes, conditionState)
}

// Create a condition on a server.
// @id: required
// @condition: required
//
// Note: its upto the caller to validate the condition payload.
func (s *Serverservice) Create(ctx context.Context, id uuid.UUID, condition *ptypes.Condition) error {
	condition.ResourceVersion = time.Now().UnixNano()
	payload, err := json.Marshal(condition)
	if err != nil {
		return errors.Wrap(ErrServerserviceAttributes, err.Error())
	}

	data := sservice.Attributes{
		Namespace: s.conditionNS(condition.Kind),
		Data:      payload,
	}

	_, err = s.client.CreateAttributes(ctx, id, data)

	return nil
}

// Update a condition on a server.
// @id: required
// @condition: required
//
// Note: its upto the caller to validate the condition update payload.
func (s *Serverservice) Update(ctx context.Context, id uuid.UUID, condition *ptypes.Condition) error {
	payload, err := json.Marshal(condition)
	if err != nil {
		return errors.Wrap(ErrServerserviceAttributes, err.Error())
	}

	_, err = s.client.UpdateAttributes(ctx, id, s.conditionNS(condition.Kind), payload)

	return nil
}

// Delete a condition from a server.
// @id: required
// @conditionKind: required
func (s *Serverservice) Delete(ctx context.Context, id uuid.UUID, conditionKind ptypes.ConditionKind) error {
	_, err := s.client.DeleteAttributes(ctx, id, s.conditionNS(conditionKind))

	return err
}
