// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package resourcereleasebindingrevision

// ListParams defines parameters for listing a resource release binding's revisions
type ListParams struct {
	Namespace string
	Binding   string
}

func (p ListParams) GetNamespace() string { return p.Namespace }

// GetParams defines parameters for getting one resource release binding revision
type GetParams struct {
	Namespace string
	Binding   string
	Revision  int64
	Output    string
}

func (p GetParams) GetNamespace() string { return p.Namespace }

// RollbackParams defines parameters for rolling a resource release binding back to a revision
type RollbackParams struct {
	Namespace string
	Binding   string
	Revision  int64
	DryRun    bool
	Yes       bool
}

func (p RollbackParams) GetNamespace() string { return p.Namespace }
