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

	newestKnownGood := int64(-1)
	for _, c := range sorted {
		if c.KnownGood {
			newestKnownGood = c.Number
		}
	}

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
