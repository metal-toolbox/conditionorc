package store

import (
	"encoding/json"
	"fmt"

	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
	"github.com/pkg/errors"
	sservice "go.hollow.sh/serverservice/pkg/api/v1"
)

func (s *Serverservice) conditionNS(kind ptypes.ConditionKind) string {
	return fmt.Sprintf(ServerserviceConditionsNSFmtStr, kind)
}

// conditionByKindFromAttributes finds the condition attribute and returns a condition object.
func (s *Serverservice) conditionByKindFromAttributes(attributes []sservice.Attributes, kind ptypes.ConditionKind) (*ptypes.Condition, error) {
	// identify condition attributes
	found := s.findAttributesByNamespace(s.conditionNS(kind), attributes)
	if len(found) == 0 {
		return nil, errors.Wrap(ErrServerserviceAttributes, "no condition attributes not found")
	}

	if len(found) > 1 {
		return nil, errors.Wrap(ErrServerserviceAttributes, "multiple conditions of the same kind found")
	}

	condition := &ptypes.Condition{CreatedAt: found[0].CreatedAt, UpdatedAt: found[0].UpdatedAt}
	if err := json.Unmarshal(found[0].Data, condition); err != nil {
		return nil, errors.Wrap(ErrServerserviceAttributes, err.Error())
	}

	return condition, nil
}

// conditionByStateFromAttributes finds the condition attribute that matches the given state and returns a condition object.
func (s *Serverservice) conditionByStateFromAttributes(attributes []sservice.Attributes, state ptypes.ConditionState) ([]*ptypes.Condition, error) {
	// identify condition attributes
	found := s.findAttributesByNamespace(ServerserviceConditionsNSFmtStr, attributes)
	if len(found) == 0 {
		return nil, errors.Wrap(ErrServerserviceAttributes, "no condition attributes not found")
	}

	return s.findConditionByStateInAttributes(state, found), nil
}

func (s *Serverservice) findAttributesByNamespace(ns string, attributes []sservice.Attributes) []*sservice.Attributes {
	found := []*sservice.Attributes{}
	for idx, _ := range attributes {
		if attributes[idx].Namespace == ns {
			found = append(found, &attributes[idx])
		}
	}

	return found
}

func (s *Serverservice) findConditionByKindInAttributes(conditionKind ptypes.ConditionKind, attributes []*sservice.Attributes) *ptypes.Condition {
	for _, attr := range attributes {
		condition := &ptypes.Condition{}
		if err := json.Unmarshal(attr.Data, condition); err != nil {
			continue
		}

		if condition.Kind == conditionKind {
			condition.CreatedAt = attr.CreatedAt
			condition.UpdatedAt = attr.UpdatedAt

			return condition
		}
	}

	return nil
}

func (s *Serverservice) findConditionByStateInAttributes(conditionState ptypes.ConditionState, attributes []*sservice.Attributes) []*ptypes.Condition {
	found := []*ptypes.Condition{}

	for _, attr := range attributes {
		condition := &ptypes.Condition{}
		if err := json.Unmarshal(attr.Data, condition); err != nil {
			continue
		}

		if condition.State == conditionState {
			condition.CreatedAt = attr.CreatedAt
			condition.UpdatedAt = attr.UpdatedAt

			found = append(found, condition)
		}
	}

	return found
}
