package store

import (
	"encoding/json"
	"fmt"

	condition "github.com/metal-toolbox/rivets/condition"
	"github.com/pkg/errors"
	sservice "go.hollow.sh/serverservice/pkg/api/v1"
)

func (s *Serverservice) conditionNS(kind condition.Kind) string {
	return fmt.Sprintf(ServerserviceConditionsNSFmtStr, kind)
}

func (s *Serverservice) conditionFromAttribute(attribute *sservice.Attributes) (*condition.Condition, error) {
	conditionFromAttr := &condition.Condition{}

	if err := json.Unmarshal(attribute.Data, conditionFromAttr); err != nil {
		return nil, errors.Wrap(ErrServerserviceAttribute, err.Error())
	}

	conditionFromAttr.CreatedAt = attribute.CreatedAt
	conditionFromAttr.UpdatedAt = attribute.UpdatedAt

	return conditionFromAttr, nil
}

func (s *Serverservice) findConditionByStateInAttributes(conditionState condition.State, attributes []*sservice.Attributes) []*condition.Condition {
	found := []*condition.Condition{}

	for _, attr := range attributes {
		foundCondition := &condition.Condition{}
		if err := json.Unmarshal(attr.Data, foundCondition); err != nil {
			continue
		}

		if foundCondition.State == conditionState {
			foundCondition.CreatedAt = attr.CreatedAt
			foundCondition.UpdatedAt = attr.UpdatedAt

			found = append(found, foundCondition)
		}
	}

	return found
}
