package types

import (
	"testing"

	"github.com/google/uuid"
	rctypes "github.com/metal-toolbox/rivets/condition"
	"github.com/stretchr/testify/require"
)

func TestValidate(t *testing.T) {
	update := ConditionUpdate{}

	require.Error(t, update.Validate(), "empty update")
	update.ConditionID = uuid.New()
	require.Error(t, update.Validate(), "only ConditionID")
	update.ServerID = uuid.New()
	require.Error(t, update.Validate(), "ConditionID and ServerID")
	update.State = rctypes.Failed
	require.Error(t, update.Validate(), "ConditionID, ServerID, State")
	update.Status = []byte(`{"you":"lose"}`)
	require.NoError(t, update.Validate(), "should be good")
}

func TestConditionUpdate_mergeExisting(t *testing.T) {
	tests := []struct {
		name     string
		update   *ConditionUpdate
		existing *rctypes.Condition
		want     *rctypes.Condition
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
			"transition state invalid error",
			&ConditionUpdate{State: rctypes.Active},
			&rctypes.Condition{State: rctypes.Failed},
			nil,
			errInvalidStateTransition,
		},
		{
			"condition ID mismatch error",
			&ConditionUpdate{
				ConditionID: uuid.New(),
				State:       rctypes.Active,
				Status:      []byte("{'foo': 'bar'}"),
			},
			&rctypes.Condition{
				ID:         uuid.New(),
				Kind:       rctypes.FirmwareInstall,
				Parameters: nil,
				State:      rctypes.Pending,
				Status:     []byte("{'woo': 'alala'}"),
			},
			nil,
			errBadUpdateTarget,
		},
		{
			"existing merged with update",
			&ConditionUpdate{
				ConditionID: uuid.MustParse("48e632e0-d0af-013b-9540-2cde48001122"),
				ServerID:    uuid.MustParse("f2cd1ef8-c759-4049-905e-f6fdf61719a9"),
				State:       rctypes.Active,
				Status:      []byte("{'foo': 'bar'}"),
			},
			&rctypes.Condition{
				Kind:       rctypes.FirmwareInstall,
				ID:         uuid.MustParse("48e632e0-d0af-013b-9540-2cde48001122"),
				Target:     uuid.MustParse("f2cd1ef8-c759-4049-905e-f6fdf61719a9"),
				Parameters: nil,
				State:      rctypes.Pending,
				Status:     []byte("{'woo': 'alala'}"),
			},
			&rctypes.Condition{
				ID:         uuid.MustParse("48e632e0-d0af-013b-9540-2cde48001122"),
				Target:     uuid.MustParse("f2cd1ef8-c759-4049-905e-f6fdf61719a9"),
				Kind:       rctypes.FirmwareInstall,
				Parameters: nil,
				State:      rctypes.Active,
				Status:     []byte("{'foo': 'bar'}"),
			},
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.update.MergeExisting(tt.existing)
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
