// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

// Package bindingrevision holds what the ReleaseBinding and ResourceReleaseBinding
// services share for revision history and rollback: request and result types,
// errors, the spec diff and sorted-list pagination.
package bindingrevision

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	openchoreov1alpha1 "github.com/openchoreo/openchoreo/api/v1alpha1"
	"github.com/openchoreo/openchoreo/internal/openchoreo-api/services"
	"github.com/openchoreo/openchoreo/internal/revision"
)

var (
	// ErrRevisionNotFound is returned when the binding has no revision with the
	// requested number.
	ErrRevisionNotFound = errors.New("revision not found")
	// ErrReleaseDeleted is returned when the release a revision binds no longer exists.
	ErrReleaseDeleted = errors.New("release bound by the revision has been deleted")
	// ErrConcurrentModification is returned when the binding changed between being
	// read and being written.
	ErrConcurrentModification = errors.New("binding was modified concurrently")
)

// RollbackRequest asks to restore the binding spec of an earlier revision.
type RollbackRequest struct {
	// Revision is the number of the revision to roll back to.
	Revision int64
	// DryRun runs every check and computes the result without writing anything.
	DryRun bool
}

// RollbackResult describes a rollback, applied or dry-run.
type RollbackResult struct {
	// RevisionName and Revision identify the revision active after the rollback.
	// A rollback creates no revision: the target revision becomes active again
	// under its own name and number, so these always name the target.
	RevisionName string
	Revision     int64
	// RevisionDisplayName is how the target is shown to people,
	// {release}-r{N} (e.g. frontend-v7-r1).
	RevisionDisplayName string
	// TargetRevision is the revision whose spec is restored.
	TargetRevision int64
	// RevertedFrom is the revision that was active when the rollback was made.
	RevertedFrom *int64
	DryRun       bool
	Changes      []SpecChange
	Warnings     []Warning
}

// SpecChange is one top-level binding spec field a rollback changes. Current and
// Target are nil when the field is unset.
type SpecChange struct {
	Field   string
	Current any
	Target  any
}

// WarningCode identifies a pre-flight finding. The values match the API.
type WarningCode string

const (
	WarningTargetNotKnownGood   WarningCode = "TargetNotKnownGood"
	WarningTargetUnhealthy      WarningCode = "TargetUnhealthy"
	WarningTargetUndeploys      WarningCode = "TargetUndeploys"
	WarningNoSpecChange         WarningCode = "NoSpecChange"
	WarningBindingWillBePinned  WarningCode = "BindingWillBePinned"
	WarningUnrecordedChange     WarningCode = "UnrecordedChange"
	WarningProjectCellDrift     WarningCode = "ProjectCellDrift"
	WarningSecretReferenceDrift WarningCode = "SecretReferenceDrift"
)

// Warning is a pre-flight finding that does not block the rollback.
type Warning struct {
	Code    WarningCode
	Message string
}

// SpecChanges compares two binding specs field by field and returns the
// top-level fields whose values differ, in field-name order. Values are
// compared with revision.CanonicalHash, so key order inside free-form config and
// nil-versus-empty do not count as changes -- the same rules that decide whether
// the controller records a new revision.
func SpecChanges(current, target any) ([]SpecChange, error) {
	cur, err := asFields(current)
	if err != nil {
		return nil, err
	}
	tgt, err := asFields(target)
	if err != nil {
		return nil, err
	}

	names := make(map[string]struct{}, len(cur)+len(tgt))
	for k := range cur {
		names[k] = struct{}{}
	}
	for k := range tgt {
		names[k] = struct{}{}
	}
	sorted := make([]string, 0, len(names))
	for k := range names {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	changes := []SpecChange{}
	for _, field := range sorted {
		curHash, err := revision.CanonicalHash(cur[field])
		if err != nil {
			return nil, err
		}
		tgtHash, err := revision.CanonicalHash(tgt[field])
		if err != nil {
			return nil, err
		}
		if curHash != tgtHash {
			changes = append(changes, SpecChange{Field: field, Current: cur[field], Target: tgt[field]})
		}
	}
	return changes, nil
}

// asFields decodes a spec into its top-level JSON fields.
func asFields(spec any) (map[string]any, error) {
	raw, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}
	fields := map[string]any{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("decode spec: %w", err)
	}
	return fields, nil
}

// Paginate returns one page of an already sorted slice. The cursor is an opaque
// offset into the slice, so pages stay in the slice's order -- which a
// Kubernetes continue token, returning objects in name order, cannot give.
func Paginate[T any](items []T, opts services.ListOptions) (*services.ListResult[T], error) {
	offset := 0
	if opts.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(opts.Cursor)
		if err == nil {
			offset, err = strconv.Atoi(string(raw))
		}
		if err != nil || offset < 0 {
			return nil, &services.ValidationError{Msg: "invalid cursor"}
		}
	}
	if offset > len(items) {
		offset = len(items)
	}

	end := len(items)
	if opts.Limit > 0 && offset+opts.Limit < end {
		end = offset + opts.Limit
	}

	result := &services.ListResult[T]{Items: items[offset:end]}
	if end < len(items) {
		result.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
		remaining := int64(len(items) - end)
		result.RemainingCount = &remaining
	}
	return result, nil
}

// CommonWarnings returns the pre-flight warnings every binding kind shares,
// from the target revision's status and how the target compares with the
// binding's current spec.
func CommonWarnings(target int64, status openchoreov1alpha1.BindingRevisionStatus,
	noSpecChange, unrecordedChange, pinned bool) []Warning {
	var warnings []Warning
	if !status.KnownGood {
		warnings = append(warnings, Warning{Code: WarningTargetNotKnownGood,
			Message: fmt.Sprintf("revision %d never stayed Healthy for the known-good window", target)})
	}
	if status.Health == openchoreov1alpha1.RevisionHealthDegraded || status.Health == openchoreov1alpha1.RevisionHealthFailed {
		warnings = append(warnings, Warning{Code: WarningTargetUnhealthy,
			Message: fmt.Sprintf("revision %d was %s when it stopped being active", target, status.Health)})
	}
	if noSpecChange {
		warnings = append(warnings, Warning{Code: WarningNoSpecChange,
			Message: fmt.Sprintf("revision %d records the current spec; nothing changes", target)})
	}
	if unrecordedChange {
		warnings = append(warnings, Warning{Code: WarningUnrecordedChange,
			Message: "the binding has a spec change that is not recorded as a revision yet; the rollback discards it"})
	}
	if !pinned {
		warnings = append(warnings, Warning{Code: WarningBindingWillBePinned,
			Message: "releasePin changes from Auto to Pinned; auto-deploy will no longer move this binding"})
	}
	return warnings
}

// ProjectCellDrift reports when the project's cell in the environment -- its
// ProjectReleaseBinding -- is bound to a ProjectRelease created after the target
// revision, so the restored spec would run in a cell it was never deployed with.
// It returns nil when there is no drift or no ProjectReleaseBinding.
func ProjectCellDrift(ctx context.Context, c client.Reader, namespace, project, environment string,
	target int64, recordedAt metav1.Time) (*Warning, error) {
	var bindings openchoreov1alpha1.ProjectReleaseBindingList
	if err := c.List(ctx, &bindings, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list project release bindings: %w", err)
	}
	for i := range bindings.Items {
		prb := &bindings.Items[i]
		if prb.Spec.Owner.ProjectName != project || prb.Spec.Environment != environment || prb.Spec.ProjectRelease == "" {
			continue
		}
		release := &openchoreov1alpha1.ProjectRelease{}
		if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: prb.Spec.ProjectRelease}, release); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("get project release %q: %w", prb.Spec.ProjectRelease, err)
		}
		if release.CreationTimestamp.After(recordedAt.Time) {
			return &Warning{Code: WarningProjectCellDrift, Message: fmt.Sprintf(
				"project %q in environment %q is now bound to ProjectRelease %q, created after revision %d",
				project, environment, release.Name, target)}, nil
		}
		return nil, nil
	}
	return nil, nil
}
