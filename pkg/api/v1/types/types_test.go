package types

import (
	"testing"

	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
	"github.com/stretchr/testify/assert"
)

func TestConditionUpdate_mergeExisting(t *testing.T) {
	tests := []struct {
		name            string
		update          *ConditionUpdate
		existing        *ptypes.Condition
		want            *ptypes.Condition
		wantErrContains string
	}{
		{
			"no existing condition returns error",
			&ConditionUpdate{},
			nil,
			nil,
			"no existing condition found for update",
		},
		{
			"resource version mismatch returns error",
			&ConditionUpdate{ResourceVersion: 0},
			&ptypes.Condition{ResourceVersion: 1},
			nil,
			"resource version mismatch",
		},
		{
			"transition state invalid error",
			&ConditionUpdate{ResourceVersion: 1, State: ptypes.Active},
			&ptypes.Condition{ResourceVersion: 1, State: ptypes.Failed},
			nil,
			"is not allowed",
		},
		{
			"existing merged with update",
			&ConditionUpdate{
				ResourceVersion: 1,
				State:           ptypes.Active,
				Status:          []byte("{'foo': 'bar'}"),
			},
			&ptypes.Condition{
				Kind:            ptypes.FirmwareInstallOutofband,
				Parameters:      nil,
				ResourceVersion: 1,
				State:           ptypes.Pending,
				Status:          []byte("{'woo': 'alala'}"),
			},
			&ptypes.Condition{
				Kind:            ptypes.FirmwareInstallOutofband,
				Parameters:      nil,
				ResourceVersion: 0, // resource version is set by the store
				State:           ptypes.Active,
				Status:          []byte("{'foo': 'bar'}"),
			},
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.update.MergeExisting(tt.existing)
			if err != nil {
				assert.Contains(t, err.Error(), tt.wantErrContains)
				return
			}

			if tt.wantErrContains != "" {
				t.Error("expected error, got none")
				return
			}

			assert.Equal(t, tt.want, got)
		})
	}
}
