// Package collisiontest provides a real, verified xxHash64 collision pair for
// exercising metric-name collision-handling code paths in tests.
//
// A hash collision cannot be fabricated: every decode path re-hashes each stored
// metric name and rejects a blob whose name does not hash to its declared ID
// (see blob.NumericDecoder.Decode). Collision paths therefore require two DISTINCT
// strings that genuinely hash to the same 64-bit ID under internal/hash.ID
// (xxHash64). This pair was found once, offline, via a ~2^32 birthday search
// using Floyd cycle detection, and is committed as a self-verifying fixture:
// TestCollisionPair asserts the two strings differ and both hash to CollisionID.
//
// Production code never uses this package and keeps calling hash.ID directly, so
// there is zero added indirection on the query hot path.
package collisiontest

// NameA and NameB are two distinct strings that hash to the same 64-bit metric
// ID (CollisionID) under internal/hash.ID. Use them to construct blobs and sets
// that exercise real hash-collision behaviour.
const (
	NameA = "mebo.collision.probe.9311205776335484502"
	NameB = "mebo.collision.probe.12220231876399211248"

	// CollisionID is hash.ID(NameA) == hash.ID(NameB).
	CollisionID uint64 = 0x81cd12cc642d886c
)
