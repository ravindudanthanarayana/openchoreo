// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package revision

import "sort"

// EvictionCandidate is the part of a revision the history limit looks at.
type EvictionCandidate struct {
	Name      string
	Number    int64
	Active    bool
	KnownGood bool
	// Keep is true when the revision is annotated openchoreo.dev/keep: "true".
	Keep bool
}

// SelectEvictions returns the revisions to delete so that at most limit remain,
// oldest by spec.revision first. A limit of zero or less means unlimited.
//
// Three kinds of revision are never selected: the active revision, the newest
// known-good revision (the rollback target of last resort), and revisions marked
// Keep. When those alone outnumber the limit, every unprotected revision is
// selected, the protected ones stay, and overLimit reports that the history is
// still above the limit.
func SelectEvictions(candidates []EvictionCandidate, limit int) (evict []EvictionCandidate, overLimit bool) {
	if limit <= 0 || len(candidates) <= limit {
		return nil, false
	}

	sorted := append([]EvictionCandidate(nil), candidates...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Number < sorted[j].Number })

	newestKnownGood := newestKnownGood(sorted)
	excess := len(sorted) - limit
	for _, c := range sorted {
		if excess == 0 {
			break
		}
		if c.Active || c.Keep || c.Number == newestKnownGood {
			continue
		}
		evict = append(evict, c)
		excess--
	}
	return evict, excess > 0
}

// Protection is why a revision may not be deleted, by the history limit or by
// hand. It reads as the end of "cannot delete revision N because ...".
type Protection string

const (
	// ProtectionActive is the revision the binding runs.
	ProtectionActive Protection = "it is currently active"
	// ProtectionNewestKnownGood is the binding's rollback target of last resort.
	ProtectionNewestKnownGood Protection = "it is the newest known-good revision, kept as the rollback target of last resort"
	// ProtectionKeep is a revision annotated openchoreo.dev/keep: "true".
	ProtectionKeep Protection = `it is annotated openchoreo.dev/keep: "true"; remove the annotation to delete it`
)

// Protected returns why target may not be deleted, or "" when it may. These are
// the revisions SelectEvictions never selects, so deleting a revision by hand
// is held to the same rules as the history limit. candidates are all of the
// binding's revisions, target among them.
func Protected(candidates []EvictionCandidate, target EvictionCandidate) Protection {
	switch {
	case target.Active:
		return ProtectionActive
	case target.Keep:
		return ProtectionKeep
	case target.KnownGood && target.Number == newestKnownGood(candidates):
		return ProtectionNewestKnownGood
	}
	return ""
}

// newestKnownGood returns the highest number among the known-good candidates,
// or -1 when none is known good.
func newestKnownGood(candidates []EvictionCandidate) int64 {
	newest := int64(-1)
	for _, c := range candidates {
		if c.KnownGood && c.Number > newest {
			newest = c.Number
		}
	}
	return newest
}
