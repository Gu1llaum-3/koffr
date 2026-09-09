package binlog

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBounds(t *testing.T) {
	require.NoError(t, Bounds{High: 100, Low: 20}.Validate())
	require.Error(t, Bounds{High: 100, Low: 100}.Validate(), "equal bounds flap")
	require.Error(t, Bounds{High: 100, Low: 150}.Validate())
	require.Error(t, Bounds{Low: 20}.Validate())

	assert.Equal(t, uint64(20), Bounds{High: 100}.WithDefaultLow().Low, "a fifth, the ratio that measured well")
	assert.Equal(t, uint64(1), Bounds{High: 3}.WithDefaultLow().Low, "never zero")
	assert.Equal(t, uint64(7), Bounds{High: 100, Low: 7}.WithDefaultLow().Low, "an explicit value is kept")
}

// The gate has memory. Without it the receiver stops at 100, restarts at 99,
// stops at 100 again -- once a second, for ever, on a slow link.
func TestGate_HasHysteresis(t *testing.T) {
	g := NewGate(Bounds{High: 100, Low: 20})

	assert.True(t, g.Allow(0))
	assert.True(t, g.Allow(99), "under the high mark, keep running")
	assert.False(t, g.Allow(100), "at the high mark, stop")
	assert.False(t, g.Allow(99), "just under it is not enough to restart")
	assert.False(t, g.Allow(20), "nor is the low mark itself")
	assert.True(t, g.Allow(19), "below the low mark, resume")
	assert.True(t, g.Allow(99), "and stay resumed until the high mark again")
	assert.False(t, g.Allow(150))
	assert.True(t, g.Stopped())
}
