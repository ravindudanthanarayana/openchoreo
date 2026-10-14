// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package releasebindingrevision

import (
	"context"
	"fmt"
	"os"

	"github.com/openchoreo/openchoreo/internal/occ/cmd/bindingrevision"
	"github.com/openchoreo/openchoreo/internal/occ/cmd/pagination"
	"github.com/openchoreo/openchoreo/internal/occ/cmdutil"
	"github.com/openchoreo/openchoreo/internal/occ/resources/client"
	"github.com/openchoreo/openchoreo/internal/openchoreo-api/api/gen"
)

const kind = "release binding"

// ReleaseBindingRevision implements release binding revision operations
type ReleaseBindingRevision struct {
	client  client.Interface
	confirm bindingrevision.Confirmer
}

// New creates a new ReleaseBindingRevision
func New(c client.Interface) *ReleaseBindingRevision {
	return &ReleaseBindingRevision{client: c, confirm: bindingrevision.ConfirmOnTerminal}
}

// List lists a release binding's revisions, newest first
func (r *ReleaseBindingRevision) List(params ListParams) error {
	if err := cmdutil.RequireFields("list", "releasebindingrevision",
		map[string]string{"namespace": params.Namespace, "binding": params.Binding}); err != nil {
		return err
	}
	revs, err := r.revisions(context.Background(), params.Namespace, params.Binding)
	if err != nil {
		return err
	}
	rows := make([]bindingrevision.Row, 0, len(revs))
	for i := range revs {
		rows = append(rows, toRow(&revs[i]))
	}
	return bindingrevision.PrintTable(os.Stdout, rows)
}

// Get shows one revision of a release binding
func (r *ReleaseBindingRevision) Get(params GetParams) error {
	if err := cmdutil.RequireFields("get", "releasebindingrevision",
		map[string]string{"namespace": params.Namespace, "binding": params.Binding}); err != nil {
		return err
	}
	if params.Revision < 1 {
		return fmt.Errorf("--revision is required and must be at least 1")
	}
	if err := bindingrevision.ValidateOutput(params.Output); err != nil {
		return err
	}

	revs, err := r.revisions(context.Background(), params.Namespace, params.Binding)
	if err != nil {
		return err
	}
	for i := range revs {
		if revs[i].Spec.Revision != params.Revision {
			continue
		}
		if params.Output == bindingrevision.OutputYAML {
			return bindingrevision.PrintYAML(os.Stdout, revs[i])
		}
		return bindingrevision.PrintTable(os.Stdout, []bindingrevision.Row{toRow(&revs[i])})
	}
	return fmt.Errorf("revision %d of release binding %q not found", params.Revision, params.Binding)
}

// Rollback restores the binding spec recorded by an earlier revision
func (r *ReleaseBindingRevision) Rollback(params RollbackParams) error {
	if err := cmdutil.RequireFields("rollback", "releasebindingrevision",
		map[string]string{"namespace": params.Namespace, "binding": params.Binding}); err != nil {
		return err
	}
	return bindingrevision.RunRollback(context.Background(), bindingrevision.RollbackOptions{
		Kind:     kind,
		Binding:  params.Binding,
		Revision: params.Revision,
		DryRun:   params.DryRun,
		Yes:      params.Yes,
		Out:      os.Stdout,
		Confirm:  r.confirm,
	}, func(ctx context.Context, dryRun bool) (*gen.RollbackResponse, error) {
		return r.client.RollbackReleaseBinding(ctx, params.Namespace, params.Binding,
			gen.RollbackRequest{Revision: params.Revision, DryRun: &dryRun})
	})
}

// revisions returns a binding's revisions, newest first. A binding is the one
// binding of its component in its environment, so its history is that
// component's revisions in that environment.
func (r *ReleaseBindingRevision) revisions(ctx context.Context, namespace, binding string) ([]gen.ReleaseBindingRevision, error) {
	rb, err := r.client.GetReleaseBinding(ctx, namespace, binding)
	if err != nil {
		return nil, err
	}
	if rb == nil || rb.Spec == nil {
		return nil, fmt.Errorf("release binding %q not found in namespace %q", binding, namespace)
	}
	component, environment := rb.Spec.Owner.ComponentName, rb.Spec.Environment

	return pagination.FetchAll(func(limit int, cursor string) ([]gen.ReleaseBindingRevision, string, error) {
		p := &gen.ListReleaseBindingRevisionsParams{Component: component, Environment: &environment, Limit: &limit}
		if cursor != "" {
			p.Cursor = &cursor
		}
		result, err := r.client.ListReleaseBindingRevisions(ctx, namespace, p)
		if err != nil {
			return nil, "", err
		}
		next := ""
		if result.Pagination.NextCursor != nil {
			next = *result.Pagination.NextCursor
		}
		return result.Items, next, nil
	})
}

func toRow(rev *gen.ReleaseBindingRevision) bindingrevision.Row {
	return bindingrevision.StatusRow(bindingrevision.Row{
		Name:      bindingrevision.DisplayName(rev.Spec.ComponentReleaseName, rev.Spec.Revision),
		Revision:  rev.Spec.Revision,
		Release:   rev.Spec.ComponentReleaseName,
		Cause:     string(rev.Spec.Cause),
		CreatedAt: rev.Metadata.CreationTimestamp,
	}, rev.Status)
}
