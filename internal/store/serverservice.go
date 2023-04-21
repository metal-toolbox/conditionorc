package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/app"
	"github.com/metal-toolbox/conditionorc/internal/metrics"
	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	sservice "go.hollow.sh/serverservice/pkg/api/v1"
)

// Serverservice implements the Repository interface to have server condition objects stored as server attributes.
type Serverservice struct {
	config               *app.ServerserviceOptions
	conditionDefinitions ptypes.ConditionDefinitions
	client               *sservice.Client
	logger               *logrus.Logger
}

var (
	// ErrServerserviceConfig is returned when theres an error in loading serverservice configuration.
	ErrServerserviceConfig = errors.New("Serverservice configuration error")

	// ErrServerserviceQuery is returned when a serverservice query error was received.
	ErrServerserviceQuery = errors.New("Serverservice query error")

	// ErrServserviceAttribute is returned when a serverservice attribute does not contain the expected fields.
	ErrServerserviceAttribute = errors.New("error in serverservice attribute")

	// ServerserviceConditionsNSFmtStr attribute namespace format string value for server condition attributes.
	ServerserviceConditionsNSFmtStr = "sh.hollow.condition.%s"
)

func serverServiceError() {
	metrics.DependencyError("serverservice")
}

func newServerserviceStore(config *app.ServerserviceOptions, conditionDefs ptypes.ConditionDefinitions, logger *logrus.Logger) (Repository, error) {
	s := &Serverservice{logger: logger, conditionDefinitions: conditionDefs, config: config}

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
func (s *Serverservice) Ping(_ context.Context) error {
	// TODO: implement
	return nil
}

// Get a condition set on a server.
// @id: required
// @conditionKind: required
func (s *Serverservice) Get(ctx context.Context, serverID uuid.UUID, conditionKind ptypes.ConditionKind) (*ptypes.Condition, error) {
	// list attributes on a server
	attributes, _, err := s.client.GetAttributes(ctx, serverID, s.conditionNS(conditionKind))
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return nil, ErrConditionNotFound
		}
		s.logger.WithFields(logrus.Fields{
			"serverID": serverID.String(),
			"kind":     conditionKind,
			"error":    err,
			"method":   "Get",
		}).Warn("error reaching serverservice")
		serverServiceError()
		return nil, errors.Wrap(ErrServerserviceQuery, err.Error())
	}

	if attributes == nil {
		// XXX: is this a realistic failure case?
		s.logger.WithFields(logrus.Fields{
			"serverID": serverID.String(),
			"kind":     conditionKind,
		}).Warn("malformed condition data")
		serverServiceError()
		return nil, ErrMalformedCondition
	}

	// return condition object from attribute
	return s.conditionFromAttribute(attributes)
}

// List all conditions set on a server.
// @id: required
// @conditionState: optional
func (s *Serverservice) List(ctx context.Context, serverID uuid.UUID, conditionState ptypes.ConditionState) ([]*ptypes.Condition, error) {
	found := []*sservice.Attributes{}

	for _, condition := range s.conditionDefinitions {
		attr, _, err := s.client.GetAttributes(ctx, serverID, s.conditionNS(condition.Kind))
		if err != nil {
			if strings.Contains(err.Error(), "404") {
				continue
			}
			s.logger.WithFields(logrus.Fields{
				"serverID": serverID.String(),
				"kind":     condition.Kind,
				"error":    err,
				"method":   "List",
			}).Warn("error reaching serverservice")
			serverServiceError()
			return nil, errors.Wrap(ErrServerserviceQuery, err.Error())
		}

		found = append(found, attr)
	}

	// list attributes on a server
	return s.findConditionByStateInAttributes(conditionState, found), nil
}

// Create a condition on a server.
// @id: required
// @condition: required
//
// Note: its upto the caller to validate the condition payload and to delete any existing condition before creating.
func (s *Serverservice) Create(ctx context.Context, serverID uuid.UUID, condition *ptypes.Condition) error {
	condition.ResourceVersion = time.Now().UnixNano()

	payload, err := json.Marshal(condition)
	if err != nil {
		return errors.Wrap(ErrServerserviceAttribute, err.Error())
	}

	data := sservice.Attributes{
		Namespace: s.conditionNS(condition.Kind),
		Data:      payload,
	}

	_, err = s.client.CreateAttributes(ctx, serverID, data)
	if err != nil {
		s.logger.WithFields(logrus.Fields{
			"serverID": serverID.String(),
			"kind":     condition.Kind,
			"error":    err,
		}).Warn("error creating condition")
		serverServiceError()
	}
	return err
}

// Update a condition on a server.
// @id: required
// @condition: required
//
// Note: its upto the caller to validate the condition update payload.
func (s *Serverservice) Update(ctx context.Context, serverID uuid.UUID, condition *ptypes.Condition) error {
	condition.ResourceVersion = time.Now().UnixNano()

	payload, err := json.Marshal(condition)
	if err != nil {
		return errors.Wrap(ErrServerserviceAttribute, err.Error())
	}

	_, err = s.client.UpdateAttributes(ctx, serverID, s.conditionNS(condition.Kind), payload)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			s.logger.WithFields(logrus.Fields{
				"serverID": serverID.String(),
				"kind":     condition.Kind,
				"method":   "Update",
			}).Warn("no condition match for this server")
			return ErrConditionNotFound
		}
		s.logger.WithFields(logrus.Fields{
			"serverID": serverID.String(),
			"kind":     condition.Kind,
			"error":    err,
		}).Warn("error updating condition")
		serverServiceError()
	}
	return err
}

// Delete a condition from a server.
// @id: required
// @conditionKind: required
func (s *Serverservice) Delete(ctx context.Context, serverID uuid.UUID, conditionKind ptypes.ConditionKind) error {
	_, err := s.client.DeleteAttributes(ctx, serverID, s.conditionNS(conditionKind))
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			s.logger.WithFields(logrus.Fields{
				"serverID": serverID.String(),
				"kind":     conditionKind,
				"method":   "Delete",
			}).Warn("no condition match for this server")
			return ErrConditionNotFound
		}
		s.logger.WithFields(logrus.Fields{
			"serverID": serverID.String(),
			"kind":     conditionKind,
			"error":    err,
		}).Warn("error deleting condition")
		serverServiceError()
	}
	return err
}

// ListServersWithCondition lists servers with the given condition kind.
func (s *Serverservice) ListServersWithCondition(ctx context.Context, conditionKind ptypes.ConditionKind, conditionState ptypes.ConditionState) ([]*ptypes.ServerConditions, error) {
	params := &sservice.ServerListParams{
		FacilityCode: s.config.FacilityCode,
		AttributeListParams: []sservice.AttributeListParams{
			{
				Namespace: s.conditionNS(conditionKind),
				Keys:      []string{"state"},
				Operator:  sservice.OperatorEqual,
				Value:     string(conditionState),
			},
		},
	}

	_, _, err := s.client.List(ctx, params)
	if err != nil {
		s.logger.WithFields(logrus.Fields{
			"kind":  conditionKind,
			"state": conditionState,
			"error": err,
		}).Warn("error listing servers")
		serverServiceError()
		return nil, err
	}

	return nil, nil
}
