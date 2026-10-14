// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"errors"

	"github.com/openchoreo/openchoreo/internal/openchoreo-api/api/gen"
	"github.com/openchoreo/openchoreo/internal/openchoreo-api/services"
	"github.com/openchoreo/openchoreo/internal/openchoreo-api/services/bindingrevision"
)

// rollbackOutcome classifies a rollback service error for both binding kinds.
type rollbackOutcome int

const (
	rollbackInternalError rollbackOutcome = iota
	rollbackBadRequest
	rollbackForbidden
	rollbackNotFound
	rollbackConflict
)

// classifyRollbackError maps a rollback service error to its HTTP outcome and,
// for 404 and 409, the error body. bindingNotFound is the service's binding
// not-found error.
func classifyRollbackError(err, bindingNotFound error) (rollbackOutcome, gen.ErrorResponse, string) {
	switch {
	case errors.Is(err, services.ErrForbidden):
		return rollbackForbidden, gen.ErrorResponse{}, ""
	case errors.Is(err, bindingNotFound):
		return rollbackNotFound, gen.ErrorResponse{Code: gen.NOTFOUND, Error: "Binding not found"}, ""
	case errors.Is(err, bindingrevision.ErrRevisionNotFound):
		return rollbackNotFound, gen.ErrorResponse{Code: gen.NOTFOUND, Error: err.Error()}, ""
	case errors.Is(err, bindingrevision.ErrReleaseDeleted):
		return rollbackConflict, gen.ErrorResponse{Code: gen.RELEASEDELETED, Error: err.Error()}, ""
	case errors.Is(err, bindingrevision.ErrConcurrentModification):
		return rollbackConflict, gen.ErrorResponse{Code: gen.CONCURRENTMODIFICATION,
			Error: err.Error() + "; retry the rollback"}, ""
	}
	if validationErr, ok := errors.AsType[*services.ValidationError](err); ok {
		return rollbackBadRequest, gen.ErrorResponse{}, validationErr.Msg
	}
	return rollbackInternalError, gen.ErrorResponse{}, ""
}

// toRollbackResponse converts a service rollback result to the API response.
func toRollbackResponse(result *bindingrevision.RollbackResult) gen.RollbackResponse {
	resp := gen.RollbackResponse{
		RevisionName:   result.RevisionName,
		Revision:       result.Revision,
		TargetRevision: result.TargetRevision,
		RevertedFrom:   result.RevertedFrom,
		DryRun:         result.DryRun,
		Changes:        make([]gen.RollbackSpecChange, 0, len(result.Changes)),
		Warnings:       make([]gen.RollbackWarning, 0, len(result.Warnings)),
	}
	if result.RevisionDisplayName != "" {
		resp.RevisionDisplayName = &result.RevisionDisplayName
	}
	for _, c := range result.Changes {
		resp.Changes = append(resp.Changes, gen.RollbackSpecChange{Field: c.Field, Current: c.Current, Target: c.Target})
	}
	for _, w := range result.Warnings {
		resp.Warnings = append(resp.Warnings, gen.RollbackWarning{Code: gen.RollbackWarningCode(w.Code), Message: w.Message})
	}
	return resp
}
