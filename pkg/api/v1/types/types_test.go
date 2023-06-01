package types

import (
	"testing"

	"github.com/google/uuid"
	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
	"github.com/stretchr/testify/require"
)

func TestValidate(t *testing.T) {
	update := ConditionUpdate{}

	require.Error(t, update.Validate(), "empty update")
	update.ConditionID = uuid.New()
	require.Error(t, update.Validate(), "only ConditionID")
	update.ServerID = uuid.New()
	require.Error(t, update.Validate(), "ConditionID and ServerID")
	update.State = ptypes.Failed
	require.Error(t, update.Validate(), "ConditionID, ServerID, State")
	update.Status = []byte(`{"you":"lose"}`)
	require.Error(t, update.Validate(), "ConditionID, ServerID, State, Status")
	update.ResourceVersion = int64(5)
	require.NoError(t, update.Validate(), "should be good")
}

func TestConditionUpdate_mergeExisting(t *testing.T) {
	tests := []struct {
		name     string
		update   *ConditionUpdate
		existing *ptypes.Condition
		want     *ptypes.Condition
		wantErr  error
	}{
		{
			"no existing condition returns error",
			&ConditionUpdate{},
			nil,
			nil,
			errBadUpdateTarget,
		},
		{
			"resource version mismatch returns error",
			&ConditionUpdate{ResourceVersion: 0},
			&ptypes.Condition{ResourceVersion: 1},
			nil,
			errResourceVersionMismatch,
		},
		{
			"transition state invalid error",
			&ConditionUpdate{ResourceVersion: 1, State: ptypes.Active},
			&ptypes.Condition{ResourceVersion: 1, State: ptypes.Failed},
			nil,
			errInvalidStateTransition,
		},
		{
			"condition ID mismatch error",
			&ConditionUpdate{
				ConditionID:     uuid.New(),
				ResourceVersion: 1,
				State:           ptypes.Active,
				Status:          []byte("{'foo': 'bar'}"),
			},
			&ptypes.Condition{
				ID:              uuid.New(),
				Kind:            ptypes.FirmwareInstall,
				Parameters:      nil,
				ResourceVersion: 1,
				State:           ptypes.Pending,
				Status:          []byte("{'woo': 'alala'}"),
			},
			nil,
			errBadUpdateTarget,
		},
		{
			"existing merged with update",
			&ConditionUpdate{
				ConditionID:     uuid.MustParse("48e632e0-d0af-013b-9540-2cde48001122"),
				ServerID:        uuid.MustParse("f2cd1ef8-c759-4049-905e-f6fdf61719a9"),
				ResourceVersion: 1,
				State:           ptypes.Active,
				Status:          []byte("{'foo': 'bar'}"),
			},
			&ptypes.Condition{
				Kind:            ptypes.FirmwareInstall,
				ID:              uuid.MustParse("48e632e0-d0af-013b-9540-2cde48001122"),
				Parameters:      nil,
				ResourceVersion: 1,
				State:           ptypes.Pending,
				Status:          []byte("{'woo': 'alala'}"),
			},
			&ptypes.Condition{
				ID:              uuid.MustParse("48e632e0-d0af-013b-9540-2cde48001122"),
				Kind:            ptypes.FirmwareInstall,
				Parameters:      nil,
				ResourceVersion: 1,
				State:           ptypes.Active,
				Status:          []byte("{'foo': 'bar'}"),
			},
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.update.MergeExisting(tt.existing, true)
			if tt.wantErr != nil {
				require.Error(t, err, "no error when one is expected")
				require.Equal(t, tt.wantErr, err, "error does not match expectation")
			} else {
				require.NoError(t, err, "unexpected error")
				require.Equal(t, tt.want, got, "received does not match expected")
			}
		})
	}
}
