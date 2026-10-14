// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package resourcereleasebindingrevision

import (
	"github.com/spf13/cobra"

	"github.com/openchoreo/openchoreo/internal/occ/auth"
	"github.com/openchoreo/openchoreo/internal/occ/cmd/bindingrevision"
	"github.com/openchoreo/openchoreo/internal/occ/flags"
	"github.com/openchoreo/openchoreo/internal/occ/resources/client"
)

func NewResourceReleaseBindingRevisionCmd(f client.NewClientFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "resourcereleasebindingrevision",
		Aliases: []string{"resourcereleasebindingrevisions", "rrbr"},
		Short:   "Manage resource release binding revisions",
		Long:    "Commands for listing resource release binding revisions and rolling a resource release binding back to one.",
	}
	cmd.AddCommand(
		newListCmd(f),
		newGetCmd(f),
		newRollbackCmd(f),
	)
	return cmd
}

func newListCmd(f client.NewClientFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List resource release binding revisions",
		Long:  `List the revisions of a resource release binding, newest first.`,
		Example: `  # List the revisions of a resource release binding
  occ resourcereleasebindingrevision list --namespace acme-corp --binding orders-db-development`,
		PreRunE: auth.RequireLogin(),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := f()
			if err != nil {
				return err
			}
			return New(cl).List(ListParams{
				Namespace: flags.GetNamespace(cmd),
				Binding:   bindingrevision.GetBinding(cmd),
			})
		},
	}
	flags.AddNamespace(cmd)
	bindingrevision.AddBinding(cmd, "resource release binding")
	return cmd
}

func newGetCmd(f client.NewClientFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Get a resource release binding revision",
		Long:  `Get one revision of a resource release binding, as a table row or in full as YAML.`,
		Example: `  # Show revision 3 of a resource release binding
  occ resourcereleasebindingrevision get --namespace acme-corp --binding orders-db-development --revision 3

  # Show the full revision, including the binding spec it recorded
  occ resourcereleasebindingrevision get --namespace acme-corp --binding orders-db-development --revision 3 -o yaml`,
		PreRunE: auth.RequireLogin(),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := f()
			if err != nil {
				return err
			}
			return New(cl).Get(GetParams{
				Namespace: flags.GetNamespace(cmd),
				Binding:   bindingrevision.GetBinding(cmd),
				Revision:  bindingrevision.GetRevision(cmd),
				Output:    bindingrevision.GetOutput(cmd),
			})
		},
	}
	flags.AddNamespace(cmd)
	bindingrevision.AddBinding(cmd, "resource release binding")
	bindingrevision.AddRevision(cmd)
	bindingrevision.AddOutput(cmd)
	return cmd
}

func newRollbackCmd(f client.NewClientFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rollback",
		Short: "Roll a resource release binding back to a revision",
		Long: `Restore the binding spec recorded by an earlier revision.

The rollback is previewed first: the spec fields it changes and any pre-flight
warnings are printed, and you are asked to confirm. The binding is pinned to the
restored release so it is not advanced automatically. No revision is
created: the controller makes the target revision active again, under its own
name and number.`,
		Example: `  # Preview a rollback to revision 3 without changing anything
  occ resourcereleasebindingrevision rollback --namespace acme-corp --binding orders-db-development --revision 3 --dry-run

  # Roll back to revision 3, confirming interactively
  occ resourcereleasebindingrevision rollback --namespace acme-corp --binding orders-db-development --revision 3

  # Roll back without a prompt (for scripts)
  occ resourcereleasebindingrevision rollback --namespace acme-corp --binding orders-db-development --revision 3 --yes`,
		PreRunE: auth.RequireLogin(),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := f()
			if err != nil {
				return err
			}
			return New(cl).Rollback(RollbackParams{
				Namespace: flags.GetNamespace(cmd),
				Binding:   bindingrevision.GetBinding(cmd),
				Revision:  bindingrevision.GetRevision(cmd),
				DryRun:    bindingrevision.GetDryRun(cmd),
				Yes:       bindingrevision.GetYes(cmd),
			})
		},
	}
	flags.AddNamespace(cmd)
	bindingrevision.AddBinding(cmd, "resource release binding")
	bindingrevision.AddRevision(cmd)
	bindingrevision.AddDryRun(cmd)
	bindingrevision.AddYes(cmd)
	return cmd
}
