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
