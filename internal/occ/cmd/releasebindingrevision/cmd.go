// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package releasebindingrevision

import (
	"github.com/spf13/cobra"

	"github.com/openchoreo/openchoreo/internal/occ/auth"
	"github.com/openchoreo/openchoreo/internal/occ/cmd/bindingrevision"
	"github.com/openchoreo/openchoreo/internal/occ/cmd/config"
	"github.com/openchoreo/openchoreo/internal/occ/flags"
	"github.com/openchoreo/openchoreo/internal/occ/resources/client"
)

func NewReleaseBindingRevisionCmd(f client.NewClientFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "releasebindingrevision",
		Aliases: []string{"releasebindingrevisions", "rbr", "crbr"},
		Short:   "Manage release binding revisions",
		Long:    "Commands for listing, showing, comparing and deleting release binding revisions. To roll back, use occ releasebinding rollback.",
	}
	cmd.AddCommand(
		newListCmd(f),
		newGetCmd(f),
		newDiffCmd(f),
		newDeleteCmd(f),
	)
	return cmd
}

func newDiffCmd(f client.NewClientFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diff FROM_REVISION TO_REVISION",
		Short: "Compare two release binding revisions",
		Long: `Compare the binding specs two revisions of a release binding recorded, and
show each field that was added (+), removed (-) or modified (~) going from
FROM_REVISION to TO_REVISION. This includes the component release the revision
deploys, environment configs, trait configs, workload overrides and state.

Name each revision as get does: its name (for example
product-catalog-development-r3), the display name shown by list, r3 or 3. A name
that belongs to another binding is refused.`,
		Args: bindingrevision.DiffArgs,
		Example: `  # What changed from revision 2 to revision 3
  occ releasebindingrevision diff r2 r3 --namespace acme-corp --binding product-catalog-development

  # What a rollback from revision 5 to revision 3 would change
  occ releasebindingrevision diff r5 r3 --namespace acme-corp --binding product-catalog-development

  # Keep colors when piping to a pager
  occ releasebindingrevision diff r2 r3 --namespace acme-corp --binding product-catalog-development --color always | less -R`,
		PreRunE: auth.RequireLogin(),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := f()
			if err != nil {
				return err
			}
			return New(cl).Diff(DiffParams{
				Namespace: flags.GetNamespace(cmd),
				Binding:   bindingrevision.GetBinding(cmd),
				From:      args[0],
				To:        args[1],
				Color:     bindingrevision.GetColor(cmd),
			})
		},
	}
	flags.AddNamespace(cmd)
	bindingrevision.AddBinding(cmd, "release binding")
	bindingrevision.AddColor(cmd)
	return cmd
}

func newListCmd(f client.NewClientFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List release binding revisions",
		Long: `List revisions, newest first. With --binding, the revisions of that one
binding. Without it, the revisions of every release binding in the namespace, with
the project, binding and environment of each; narrow them with --project,
--component and --environment.`,
		Example: `  # List the revisions of every release binding in the namespace
  occ releasebindingrevision list --namespace acme-corp

  # Every binding in one project
  occ releasebindingrevision list --namespace acme-corp --project online-store

  # Every environment of one component
  occ releasebindingrevision list --namespace acme-corp --component product-catalog

  # Every component in one environment
  occ releasebindingrevision list --namespace acme-corp --environment production

  # One binding
  occ releasebindingrevision list --namespace acme-corp --binding product-catalog-development`,
		// --project, --component and --resource are filters here: filling them
		// from the current context would silently narrow the list, so context
		// defaults are skipped and only the namespace is taken from the context.
		Annotations: map[string]string{config.SkipContextDefaultsAnnotation: ""},
		PreRunE:     auth.RequireLogin(),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := f()
			if err != nil {
				return err
			}
			return New(cl).List(ListParams{
				Namespace:   bindingrevision.NamespaceOrContext(cmd),
				Binding:     bindingrevision.GetBinding(cmd),
				Project:     flags.GetProject(cmd),
				Component:   flags.GetComponent(cmd),
				Environment: bindingrevision.GetEnvironment(cmd),
			})
		},
	}
	flags.AddNamespace(cmd)
	bindingrevision.AddBinding(cmd, "release binding")
	flags.AddProject(cmd)
	flags.AddComponent(cmd)
	bindingrevision.AddEnvironment(cmd)
	return cmd
}

func newGetCmd(f client.NewClientFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get [REVISION_NAME]",
		Short: "Get a release binding revision",
		Long: `Get one revision of a release binding, as a table row or in full as YAML.

Name the revision with REVISION_NAME (its name, for example
product-catalog-development-r3 or the display name shown by list, or r3) or with --revision.`,
		Args: cobra.MaximumNArgs(1),
		Example: `  # Show revision 3 of a release binding
  occ releasebindingrevision get --namespace acme-corp --binding product-catalog-development --revision 3

  # The same revision, by name
  occ releasebindingrevision get product-catalog-development-r3 --namespace acme-corp --binding product-catalog-development

  # Show the full revision, including the binding spec it recorded
  occ releasebindingrevision get --namespace acme-corp --binding product-catalog-development --revision 3 -o yaml`,
		PreRunE: auth.RequireLogin(),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := f()
			if err != nil {
				return err
			}
			revisionNumber, err := bindingrevision.RevisionFromArgs(cmd, args)
			if err != nil {
				return err
			}
			return New(cl).Get(GetParams{
				Namespace: flags.GetNamespace(cmd),
				Binding:   bindingrevision.GetBinding(cmd),
				Revision:  revisionNumber,
				Output:    bindingrevision.GetOutput(cmd),
			})
		},
	}
	flags.AddNamespace(cmd)
	bindingrevision.AddBinding(cmd, "release binding")
	bindingrevision.AddRevision(cmd)
	bindingrevision.AddOutput(cmd)
	return cmd
}

func newDeleteCmd(f client.NewClientFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete [REVISION_NAME]",
		Short: "Delete a release binding revision",
		Long: `Delete one revision from a release binding's history. It can no longer be
rolled back to. The other revisions keep their numbers, and the deleted number is
never given to a new revision.

Name the revision with REVISION_NAME (its name, its display name, or r3) or with
--revision. The revision is shown and you are asked to confirm. The server refuses
to delete the active revision, the newest known-good revision, and revisions
annotated openchoreo.dev/keep: "true".`,
		Args: cobra.MaximumNArgs(1),
		Example: `  # Check that revision 2 can be deleted, without deleting it
  occ releasebindingrevision delete --namespace acme-corp --binding product-catalog-development --revision 2 --dry-run

  # Delete revision 2, confirming interactively
  occ releasebindingrevision delete r2 --namespace acme-corp --binding product-catalog-development

  # Delete without a prompt (for scripts)
  occ releasebindingrevision delete --namespace acme-corp --binding product-catalog-development --revision 2 --yes`,
		PreRunE: auth.RequireLogin(),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := f()
			if err != nil {
				return err
			}
			revisionNumber, err := bindingrevision.RevisionFromArgs(cmd, args)
			if err != nil {
				return err
			}
			return New(cl).Delete(DeleteParams{
				Namespace: flags.GetNamespace(cmd),
				Binding:   bindingrevision.GetBinding(cmd),
				Revision:  revisionNumber,
				DryRun:    bindingrevision.GetDryRun(cmd),
				Yes:       bindingrevision.GetYes(cmd),
			})
		},
	}
	flags.AddNamespace(cmd)
	bindingrevision.AddBinding(cmd, "release binding")
	bindingrevision.AddRevision(cmd)
	bindingrevision.AddDryRun(cmd, "Check that the revision can be deleted without deleting it")
	bindingrevision.AddYes(cmd, "Delete without asking for confirmation")
	return cmd
}
