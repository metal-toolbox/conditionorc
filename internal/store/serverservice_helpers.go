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
func (s *Serverservice) conditionByKindFromAttributes(attributes []*sservice.Attributes, kind ptypes.ConditionKind) (*ptypes.Condition, error) {
	// identify condition attributes
	found := s.findAttributesByNamespace(s.conditionNS(kind), attributes)
	if len(found) == 0 {
		return nil, errors.Wrap(ErrServerserviceAttribute, "no condition attributes not found")
	}

	if len(found) > 1 {
		return nil, errors.Wrap(ErrServerserviceAttribute, "multiple conditions of the same kind found")
	}

	condition := &ptypes.Condition{CreatedAt: found[0].CreatedAt, UpdatedAt: found[0].UpdatedAt}
	if err := json.Unmarshal(found[0].Data, condition); err != nil {
		return nil, errors.Wrap(ErrServerserviceAttribute, err.Error())
	}

	return condition, nil
}

func (s *Serverservice) findAttributesByNamespace(ns string, attributes []*sservice.Attributes) []*sservice.Attributes {
	found := []*sservice.Attributes{}

	for idx := range attributes {
		if attributes[idx].Namespace == ns {
			found = append(found, attributes[idx])
		}
	}

	return found
}

// func (s *Serverservice) findConditionByKindInAttributes(conditionKind ptypes.ConditionKind, attributes []*sservice.Attributes) *ptypes.Condition {
//	for _, attr := range attributes {
//		condition := &ptypes.Condition{}
//		if err := json.Unmarshal(attr.Data, condition); err != nil {
//			continue
//		}
//
//		if condition.Kind == conditionKind {
//			condition.CreatedAt = attr.CreatedAt
//			condition.UpdatedAt = attr.UpdatedAt
//
//			return condition
//		}
//	}
//
//	return nil
// }

func (s *Serverservice) conditionFromAttribute(attribute *sservice.Attributes) (*ptypes.Condition, error) {
	condition := &ptypes.Condition{}

	if err := json.Unmarshal(attribute.Data, condition); err != nil {
		return nil, errors.Wrap(ErrServerserviceAttribute, err.Error())
	}

	condition.CreatedAt = attribute.CreatedAt
	condition.UpdatedAt = attribute.UpdatedAt

	return condition, nil
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
