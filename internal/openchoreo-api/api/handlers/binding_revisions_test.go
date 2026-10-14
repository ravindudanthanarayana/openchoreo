// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/ptr"

	"github.com/openchoreo/openchoreo/internal/openchoreo-api/api/gen"
	"github.com/openchoreo/openchoreo/internal/openchoreo-api/services"
	"github.com/openchoreo/openchoreo/internal/openchoreo-api/services/bindingrevision"
)

func TestClassifyRollbackError(t *testing.T) {
	bindingNotFound := errors.New("binding not found")

	tests := []struct {
		name        string
		err         error
		wantOutcome rollbackOutcome
		wantCode    gen.ErrorResponseCode
	}{
		{name: "forbidden", err: fmt.Errorf("check: %w", services.ErrForbidden), wantOutcome: rollbackForbidden},
		{name: "binding not found", err: bindingNotFound, wantOutcome: rollbackNotFound, wantCode: gen.NOTFOUND},
		{name: "revision not found", err: fmt.Errorf("%w: revision 7", bindingrevision.ErrRevisionNotFound),
			wantOutcome: rollbackNotFound, wantCode: gen.NOTFOUND},
		{name: "release deleted", err: fmt.Errorf("%w: ComponentRelease x", bindingrevision.ErrReleaseDeleted),
			wantOutcome: rollbackConflict, wantCode: gen.RELEASEDELETED},
		{name: "concurrent modification", err: fmt.Errorf("%w: binding b", bindingrevision.ErrConcurrentModification),
			wantOutcome: rollbackConflict, wantCode: gen.CONCURRENTMODIFICATION},
		{name: "validation", err: &services.ValidationError{Msg: "revision must be at least 1"}, wantOutcome: rollbackBadRequest},
		{name: "anything else", err: errors.New("etcd is on fire"), wantOutcome: rollbackInternalError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outcome, body, msg := classifyRollbackError(tt.err, bindingNotFound)
			assert.Equal(t, tt.wantOutcome, outcome)
			assert.Equal(t, tt.wantCode, body.Code)
			if tt.wantOutcome == rollbackBadRequest {
				assert.Equal(t, "revision must be at least 1", msg)
			}
			if tt.wantOutcome == rollbackInternalError {
				assert.Empty(t, body.Error, "internal errors must not leak their message")
			}
		})
	}
}

func TestToRollbackResponse(t *testing.T) {
	resp := toRollbackResponse(&bindingrevision.RollbackResult{
		RevisionName: "api-dev-r6", Revision: 6, TargetRevision: 3, RevertedFrom: ptr.To(int64(5)), DryRun: true,
		Changes:  []bindingrevision.SpecChange{{Field: "releaseName", Current: "api-b", Target: "api-a"}},
		Warnings: []bindingrevision.Warning{{Code: bindingrevision.WarningProjectCellDrift, Message: "cell moved"}},
	})

	assert.Equal(t, "api-dev-r6", resp.RevisionName)
	assert.Equal(t, int64(6), resp.Revision)
	assert.Equal(t, int64(3), resp.TargetRevision)
	assert.Equal(t, ptr.To(int64(5)), resp.RevertedFrom)
	assert.True(t, resp.DryRun)
	assert.Equal(t, []gen.RollbackSpecChange{{Field: "releaseName", Current: "api-b", Target: "api-a"}}, resp.Changes)
	assert.Equal(t, []gen.RollbackWarning{{Code: gen.ProjectCellDrift, Message: "cell moved"}}, resp.Warnings)

	empty := toRollbackResponse(&bindingrevision.RollbackResult{})
	assert.NotNil(t, empty.Changes, "changes is required in the API, so it is [] rather than null")
	assert.NotNil(t, empty.Warnings)
}
