package collisiontest

import (
	"testing"

	"github.com/arloliu/mebo/internal/hash"
	"github.com/stretchr/testify/require"
)

// TestCollisionPair is the self-verifying guard for the committed collision
// fixture. If xxHash64 or the strings ever change, this fails loudly.
func TestCollisionPair(t *testing.T) {
	require.NotEqual(t, NameA, NameB, "collision fixture strings must be distinct")

	ha := hash.ID(NameA)
	hb := hash.ID(NameB)

	require.Equal(t, CollisionID, ha, "hash.ID(NameA) must equal declared CollisionID")
	require.Equal(t, CollisionID, hb, "hash.ID(NameB) must equal declared CollisionID")
	require.Equal(t, ha, hb, "NameA and NameB must collide")
}
