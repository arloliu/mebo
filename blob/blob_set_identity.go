package blob

import (
	"cmp"
	"slices"
	"strings"

	"github.com/arloliu/mebo/internal/hash"
)

// This file implements the blob-set logical identity: it groups a blob set's
// metrics by logical name/id, so metrics that share a member's data are treated
// as one metric no matter which underlying blob they came from.
//
// Set logical identity = the metric name when the set is names-bearing, else the
// MetricID. The same name across members is one merged set metric (today's
// cross-window merge, preserved); two DIFFERENT names colliding on one ID are two
// set metrics. On every ID-based set accessor a collided ID resolves to the FIRST
// colliding name's logical metric in canonical order — never an A+B frankenseries.
//
// Canonical logical-metric order = first appearance of the name in canonical member
// order (members sorted chronologically by StartTime, caller slice order breaking
// equal-StartTime ties via a stable sort), then index order within that member.
//
// Construction is LAZY: a collision probe runs at set construction and the identity
// table is materialised ONLY when a collision is observed, so a no-collision set keeps
// today's zero-alloc direct per-member ID iteration on every accessor. The probe itself
// is O(members) and zero-alloc unless the set has two or more names-bearing members, in
// which case detecting a separated cross-member collision costs a pass over their entries:
// zero-alloc for two members, one exactly-sized scratch slice beyond two — see
// setHasCollision for the exact bound per shape and why that is the floor.

// setLogicalIdentity is the lazily-built identity table for a blob set — built
// only when a collision is actually observed. It is nil whenever no collision is
// present; a nil receiver means "no collision", and all ID-based accessors then
// fall through to today's direct per-member iteration.
//
// Only collided MetricIDs (those bound to >=2 distinct names across the set, in
// canonical order) appear in firstName, mapped to their FIRST colliding name. A
// non-collided ID is absent, so resolveID short-circuits to the fast path.
type setLogicalIdentity struct {
	firstName map[uint64]string
}

// resolveID returns the target logical name for an ID and whether the ID is collided.
// On a nil identity (no collision observed at construction) it always reports
// ("", false), so the caller uses its direct per-member iteration.
func (idt *setLogicalIdentity) resolveID(id uint64) (string, bool) {
	if idt == nil {
		return "", false
	}
	name, ok := idt.firstName[id]

	return name, ok
}

// excludesStripped reports whether a NAME-keyed set access for metricName must skip
// stripped (names-free) members.
//
// A stripped member has no name of its own, so its data for hash.ID(metricName) matches
// every name colliding on that id. §5.4.2 attaches it to the FIRST colliding name only,
// so it is excluded exactly when metricName's id is collided and metricName is not that
// first name. Zero-alloc, and always false on a nil identity (no collision observed).
func (idt *setLogicalIdentity) excludesStripped(metricName string) bool {
	if idt == nil {
		return false
	}
	targetName, collided := idt.resolveID(hash.ID(metricName))

	return collided && targetName != metricName
}

// resolveEntry returns the index entry an ID-keyed set accessor must read from this
// member, and whether the member contributes at all to the logical metric the id
// resolved to. targetName/collided come from setLogicalIdentity.resolveID.
//
// A non-collided id keeps today's direct id lookup. A collided id resolves BY NAME, which
// is what gives each kind of member the right answer:
//
//   - internally collided member → byName lookup → the entry actually named targetName,
//     even when that is not the member's first entry for the id;
//   - names-bearing member → hash plus exact string compare, so a member whose id binds
//     the OTHER colliding name is correctly excluded;
//   - stripped member (names == nil) → falls back to GetByID(hash.ID(targetName)), which
//     is §5.4.2's "the stripped member attaches to the first colliding name".
func (m indexMaps[T]) resolveEntry(metricID uint64, targetName string, collided bool) (T, bool) {
	if !collided {
		return m.GetByID(metricID)
	}

	return m.GetByName(targetName)
}

// resolveEntryByName returns the index entry a NAME-keyed set accessor must read from
// this member, and whether the member contributes to that name's logical metric.
//
// A names-bearing member matches the name exactly. A stripped member is gated by
// skipStripped, computed once per query by setLogicalIdentity.excludesStripped.
func (m indexMaps[T]) resolveEntryByName(metricName string, skipStripped bool) (T, bool) {
	if skipStripped && m.names == nil {
		var zero T

		return zero, false
	}

	return m.GetByName(metricName)
}

// logicalPlan is the ordered logical-identity enumeration of a blob set in canonical
// order. Named logical metrics come first in first-appearance order, then any
// id-only metrics (stripped/names-free members whose id is never named).
//
//   - ids[k]/names[k] describe logical metric k; names[k] is "" for an id-only metric.
//   - byName maps a name to its slot (exact; absent name → not a set metric).
//   - byID maps an ID to its FIRST slot (first-wins canonical) so a collided ID
//     resolves to the first colliding name's slot.
type logicalPlan struct {
	ids    []uint64
	names  []string
	byName map[string]int
	byID   map[uint64]int
}

// slotForName resolves a metric name to its logical slot in a plan's
// names/byName/byID tables. A named slot matches exactly. Otherwise the name
// can only refer to data from names-free members, whose slots are keyed by ID
// alone, so the query is hashed and accepted when it lands on an id-only slot —
// the same hash fallback the raw set and a single-blob Materialize() use when no
// names payload exists.
func slotForName(names []string, byName map[string]int, byID map[uint64]int, metricName string) (int, bool) {
	if slot, ok := byName[metricName]; ok {
		return slot, true
	}

	slot, ok := byID[hash.ID(metricName)]
	if !ok || names[slot] != "" {
		return -1, false
	}

	return slot, true
}

// setHasCollision is the collision probe run at set construction: it answers "does this
// set need an identity table?" without building one.
//
// Cost, stated exactly (n/m are the two members' entry counts, E the total entries of the
// names-bearing members, k the number of names-bearing members):
//
//   - A member reporting an internal collision (byName != nil) short-circuits to true in
//     O(members), zero alloc.
//   - Fewer than TWO names-bearing members can never host a separated cross-member
//     collision — a stripped member carries no name, so it cannot bind an id to a second
//     name — so the probe also returns in O(members), zero alloc. This covers every
//     names-free set and every mixed set with a single retained-names member.
//   - k == 2: zero alloc. Each id of the smaller member is looked up in the larger
//     member's index: O(min(n,m)·log max(n,m)) when the larger member is V2 (binary
//     search), O(min(n,m)) expected when it is V1/text (hash index).
//   - k >= 3: every binding is first probed against the WIDEST member's index — zero
//     alloc, O(E) lookups. Only ids that member does not declare need memory of their
//     own, and they get exactly ONE right-sized scratch slice. See bindingsDisagree.
//
// A SEPARATED collision (one id bound to different names in different members, no member
// internally collided) has no cheaper witness: members are not mutually sorted against
// each other, and text/V1 members carry no sorted id slice, so every entry must be
// compared against the bindings the other members declare.
func setHasCollision[T indexEntry](get func(i int) *indexMaps[T], n int) bool {
	namesBearing := 0
	widest := -1
	firstNamed, secondNamed := -1, -1

	for i := range n {
		m := get(i)
		if m.byName != nil {
			// A member reports an internal collision — detected allocation-free.
			return true
		}
		if m.names == nil {
			continue
		}
		namesBearing++
		if widest < 0 || len(m.sorted) > len(get(widest).sorted) {
			widest = i
		}
		if firstNamed < 0 {
			firstNamed = i
		} else if secondNamed < 0 {
			secondNamed = i
		}
	}

	switch {
	case namesBearing < 2:
		// Names-free sets use ID identity (no collision resolution is possible); one
		// names-bearing member cannot host a cross-member collision on its own.
		return false
	case namesBearing == 2:
		return membersDisagreeOnName(get(firstNamed), get(secondNamed))
	default:
		return bindingsDisagree(get, n, widest)
	}
}

// membersDisagreeOnName reports whether two names-bearing members bind the same id to
// different names. Both members are internally collision-free (setHasCollision returns
// early otherwise), so each id occurs at most once per member and the larger member's own
// index is sufficient memory — the two-member case, the common multi-window shape,
// allocates nothing.
//
// It looks each id of the SMALLER member up in the larger member's index, so the cost is
// min(n,m) index lookups: ~log2(max(n,m)) steps each when the larger member is V2 (binary
// search over sortedIDs), expected O(1) when it is V1/text (hash index). Probing the
// smaller side makes a lopsided pair cheap — one 1-metric member against a 100k-metric one
// costs a single lookup.
//
// Two V2 members could instead merge-walk their ascending sortedIDs in n+m steps; measured
// on 200×200 up to 20000×20000 pairs that is within noise of the probe (the merge's
// unpredictable three-way branch offsets the binary search's extra comparisons, and equal
// ids pay a string compare either way), so the simpler single strategy stands.
func membersDisagreeOnName[T indexEntry](a, b *indexMaps[T]) bool {
	if len(b.sorted) > len(a.sorted) {
		a, b = b, a
	}

	for ord := range b.sorted {
		if o, ok := a.getOrdinal(b.sorted[ord].GetMetricID()); ok && a.names[o] != b.names[ord] {
			return true
		}
	}

	return false
}

// probeBinding is one id → name binding declared by a names-bearing member, recorded as
// the member index plus the entry's ordinal within it rather than the name itself. Keeping
// it POINTER-FREE lets the probe's scratch slice live in a noscan span, so it costs the GC
// nothing to trace. int32 is ample: a blob holds at most MaxMetricCount (65536) entries and
// a set at most math.MaxInt32 members long before other limits bite.
type probeBinding struct {
	id     uint64
	member int32
	ord    int32
}

// bindingsDisagree reports whether THREE OR MORE names-bearing members bind one id to two
// different names. ref is the index of the WIDEST names-bearing member, used as the probe's
// reference index.
//
// Phase 1 — reference probe, ZERO alloc. Every other names-bearing member's entries are
// looked up in the reference member's own index. An id the reference declares is fully
// settled here: if two members both bind it and both agree with the reference, they agree
// with each other, so no separated collision on that id can escape. Sets whose members
// carry the same metrics across time windows — the common multi-window shape — miss
// nothing, so the probe finishes allocation-free.
//
// Phase 2 — unmatched bindings only. An id the reference does NOT declare still has to be
// checked against the other members that declare it, and only there does the probe need
// memory of its own. Phase 1 counted those bindings exactly, so ONE scratch slice is
// allocated at its final size and can never grow; sorting it by id makes every pair sharing
// an id adjacent, and a run is uniform iff every adjacent pair inside it agrees.
//
// That exact sizing is what a first-seen-name map cannot offer: its occupancy is the id
// UNION across members, which for sparse or disjoint schemas approaches members × widest,
// so a pre-size taken from a single member regrows — and a pre-size taken from the union
// bound would itself allocate a bucket directory proportional to it. Here the scratch
// allocation count is 0 or 1 for every set shape, member count, and degree of overlap.
//
// Cost with E total entries and U unmatched bindings: O(E) index lookups plus O(U log U).
// U is 0 whenever the widest member is a superset of the others.
func bindingsDisagree[T indexEntry](get func(i int) *indexMaps[T], n int, ref int) bool {
	r := get(ref)

	unmatched := 0
	for i := range n {
		m := get(i)
		if i == ref || m.names == nil {
			continue
		}
		for ord := range m.sorted {
			o, ok := r.getOrdinal(m.sorted[ord].GetMetricID())
			if !ok {
				unmatched++
				continue
			}
			if r.names[o] != m.names[ord] {
				return true
			}
		}
	}

	if unmatched == 0 {
		return false
	}

	bindings := make([]probeBinding, 0, unmatched)
	for i := range n {
		m := get(i)
		if i == ref || m.names == nil {
			continue
		}
		for ord := range m.sorted {
			id := m.sorted[ord].GetMetricID()
			if _, ok := r.getOrdinal(id); ok {
				continue
			}
			bindings = append(bindings, probeBinding{id: id, member: int32(i), ord: int32(ord)})
		}
	}

	slices.SortFunc(bindings, func(x, y probeBinding) int { return cmp.Compare(x.id, y.id) })

	for k := 1; k < len(bindings); k++ {
		prev, cur := bindings[k-1], bindings[k]
		if prev.id != cur.id {
			continue
		}
		if get(int(prev.member)).names[prev.ord] != get(int(cur.member)).names[cur.ord] {
			return true
		}
	}

	return false
}

// newSetLogicalIdentity runs the collision probe and, only when a collision is
// observed, builds the identity table. Returns nil for a no-collision (or
// names-free) set, so ID-keyed access stays on the direct per-member path.
func newSetLogicalIdentity[T indexEntry](get func(i int) *indexMaps[T], n int) *setLogicalIdentity {
	if !setHasCollision(get, n) {
		return nil
	}

	plan := buildLogicalPlan(get, n)

	// A collided ID is one bound to >=2 slots (>=2 distinct names) in the plan.
	counts := make(map[uint64]int, len(plan.ids))
	for _, id := range plan.ids {
		counts[id]++
	}

	firstName := make(map[uint64]string)
	for id, c := range counts {
		if c >= 2 {
			firstName[id] = plan.names[plan.byID[id]]
		}
	}
	if len(firstName) == 0 {
		return nil
	}

	return &setLogicalIdentity{firstName: firstName}
}

// buildLogicalPlan enumerates the set's logical metrics in canonical order.
// It is O(total entries) and always allocates; callers that must stay zero-alloc on the
// no-collision path (raw ID accessors) do NOT call it — they use setLogicalIdentity.
// Enumeration methods and the materialized-set builders (which already decode
// everything) call it.
func buildLogicalPlan[T indexEntry](get func(i int) *indexMaps[T], n int) logicalPlan {
	p := logicalPlan{
		byName: make(map[string]int),
		byID:   make(map[uint64]int),
	}

	// Phase 1: named logical metrics, in canonical (member, index) order.
	for i := 0; i < n; i++ {
		m := get(i)
		if m.names == nil {
			continue
		}
		for ord := range m.sorted {
			name := m.names[ord]
			if _, ok := p.byName[name]; ok {
				continue
			}
			id := m.sorted[ord].GetMetricID()
			slot := len(p.ids)
			p.ids = append(p.ids, id)
			p.names = append(p.names, name)
			p.byName[name] = slot
			if _, ok := p.byID[id]; !ok {
				p.byID[id] = slot
			}
		}
	}

	// Phase 2: id-only logical metrics for stripped/names-free members whose id is not
	// bound to any name. A stripped id that IS named attaches to its first named slot
	// (already in byID), preserving the merge.
	for i := 0; i < n; i++ {
		m := get(i)
		if m.names != nil {
			continue
		}
		for ord := range m.sorted {
			id := m.sorted[ord].GetMetricID()
			if _, ok := p.byID[id]; ok {
				continue
			}
			slot := len(p.ids)
			p.ids = append(p.ids, id)
			p.names = append(p.names, "")
			p.byID[id] = slot
		}
	}

	return p
}

// ownSetNames makes a materialized set's names fully owning. A logical plan
// built over member indexes carries strings that alias whichever members borrowed
// their names (NewNumericDecoderBorrowed / NewTextDecoderBorrowed); a materialized
// set must not, so when ANY member borrowed, this deep-clones the per-slot names
// and rebuilds byName with the cloned keys. When no member borrowed, the inputs
// already own their bytes and are returned unchanged.
//
// names[k] is the name of logical slot k ("" for an id-only slot); byName maps
// each named slot's name to its slot index. Both invariants are preserved.
func ownSetNames[T indexEntry](names []string, byName map[string]int, get func(i int) *indexMaps[T], n int) ([]string, map[string]int) {
	borrowed := false
	for i := 0; i < n; i++ {
		if get(i).namesBorrowed {
			borrowed = true
			break
		}
	}
	if !borrowed {
		return names, byName
	}

	out := make([]string, len(names))
	nb := make(map[string]int, len(byName))
	for k, name := range names {
		if name == "" {
			continue
		}
		c := strings.Clone(name)
		out[k] = c
		nb[c] = k
	}

	return out, nb
}

// metricNames returns the distinct logical metric names in canonical order, excluding
// id-only metrics.
func (p logicalPlan) metricNames() []string {
	if len(p.byName) == 0 {
		return nil
	}
	names := make([]string, 0, len(p.byName))
	for k := range p.names {
		if p.names[k] != "" {
			names = append(names, p.names[k])
		}
	}

	return names
}
