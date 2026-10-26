// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package releasebindingrevision

// ListParams defines parameters for listing a release binding's revisions
type ListParams struct {
	Namespace string
	// Binding lists one binding's revisions. Without it, the revisions of every
	// binding in the namespace are listed, narrowed by Project, Component and
	// Environment.
	Binding     string
	Project     string
	Component   string
	Environment string
}

func (p ListParams) GetNamespace() string { return p.Namespace }

// GetParams defines parameters for getting one release binding revision
type GetParams struct {
	Namespace string
	Binding   string
	Revision  int64
	Output    string
}

func (p GetParams) GetNamespace() string { return p.Namespace }

// DeleteParams defines parameters for deleting one release binding revision
type DeleteParams struct {
	Namespace string
	Binding   string
	Revision  int64
	DryRun    bool
	Yes       bool
}

func (p DeleteParams) GetNamespace() string { return p.Namespace }

// DiffParams defines parameters for comparing two release binding revisions
type DiffParams struct {
	Namespace string
	Binding   string
	// From and To name the revisions as REVISION_NAME arguments do: r3, 3, or a
	// revision's object or display name.
	From string
	To   string
	// Color is the --color mode: auto, always or never.
	Color string
}

func (p DiffParams) GetNamespace() string { return p.Namespace }
