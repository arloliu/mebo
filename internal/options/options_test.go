package options

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// Test types for testing the generic options pattern
type TestConfig struct {
	Value    int
	Name     string
	Enabled  bool
	LastCall string
}

func (tc *TestConfig) SetValue(v int) error {
	if v < 0 {
		return errors.New("value cannot be negative")
	}
	tc.Value = v
	tc.LastCall = "SetValue"

	return nil
}

func (tc *TestConfig) SetName(name string) {
	tc.Name = name
	tc.LastCall = "SetName"
}

func (tc *TestConfig) SetEnabled(enabled bool) {
	tc.Enabled = enabled
	tc.LastCall = "SetEnabled"
}

func TestOption_New(t *testing.T) {
	config := &TestConfig{}

	t.Run("creates option that can return error", func(t *testing.T) {
		opt := New(func(c *TestConfig) error {
			return c.SetValue(42)
		})

		err := opt.apply(config)
		require.NoError(t, err)
		require.Equal(t, 42, config.Value)
		require.Equal(t, "SetValue", config.LastCall)
	})

	t.Run("propagates errors from option function", func(t *testing.T) {
		opt := New(func(c *TestConfig) error {
			return c.SetValue(-1) // This should return an error
		})

		err := opt.apply(config)
		require.Error(t, err)
		require.Contains(t, err.Error(), "value cannot be negative")
	})
}

func TestOption_NoError(t *testing.T) {
	config := &TestConfig{}

	t.Run("creates option from function without error", func(t *testing.T) {
		opt := NoError(func(c *TestConfig) {
			c.SetName("test")
		})

		err := opt.apply(config)
		require.NoError(t, err)
		require.Equal(t, "test", config.Name)
		require.Equal(t, "SetName", config.LastCall)
	})

	t.Run("works with boolean setter", func(t *testing.T) {
		opt := NoError(func(c *TestConfig) {
			c.SetEnabled(true)
		})

		err := opt.apply(config)
		require.NoError(t, err)
		require.True(t, config.Enabled)
		require.Equal(t, "SetEnabled", config.LastCall)
	})
}

func TestOption_Apply(t *testing.T) {
	config := &TestConfig{}

	t.Run("applies multiple options in order", func(t *testing.T) {
		opts := []Option[*TestConfig]{
			New(func(c *TestConfig) error { return c.SetValue(10) }),
			NoError(func(c *TestConfig) { c.SetName("test") }),
			NoError(func(c *TestConfig) { c.SetEnabled(true) }),
		}

		err := Apply(config, opts...)
		require.NoError(t, err)
		require.Equal(t, 10, config.Value)
		require.Equal(t, "test", config.Name)
		require.True(t, config.Enabled)
		require.Equal(t, "SetEnabled", config.LastCall) // Last option should be the last call
	})

	t.Run("stops at first error and returns it", func(t *testing.T) {
		config := &TestConfig{} // Reset config

		opts := []Option[*TestConfig]{
			New(func(c *TestConfig) error { return c.SetValue(5) }),  // Should succeed
			New(func(c *TestConfig) error { return c.SetValue(-1) }), // Should fail
			NoError(func(c *TestConfig) { c.SetName("should not be set") }),
		}

		err := Apply(config, opts...)
		require.Error(t, err)
		require.Contains(t, err.Error(), "value cannot be negative")
		require.Equal(t, 5, config.Value)             // First option applied
		require.Equal(t, "", config.Name)             // Third option should not have been applied
		require.Equal(t, "SetValue", config.LastCall) // Should be from first option
	})

	t.Run("works with empty options slice", func(t *testing.T) {
		config := &TestConfig{}
		err := Apply(config)
		require.NoError(t, err)
		// Config should remain unchanged
		require.Equal(t, 0, config.Value)
		require.Equal(t, "", config.Name)
		require.False(t, config.Enabled)
	})
}

func TestOption_Integration(t *testing.T) {
	config := &TestConfig{}

	// Create helper functions that return options (similar to WithXxx patterns)
	withValue := func(v int) Option[*TestConfig] {
		return New(func(c *TestConfig) error {
			return c.SetValue(v)
		})
	}

	withName := func(name string) Option[*TestConfig] {
		return NoError(func(c *TestConfig) {
			c.SetName(name)
		})
	}

	withEnabled := func(enabled bool) Option[*TestConfig] {
		return NoError(func(c *TestConfig) {
			c.SetEnabled(enabled)
		})
	}

	t.Run("works with helper functions", func(t *testing.T) {
		err := Apply(config,
			withValue(100),
			withName("integration test"),
			withEnabled(true),
		)

		require.NoError(t, err)
		require.Equal(t, 100, config.Value)
		require.Equal(t, "integration test", config.Name)
		require.True(t, config.Enabled)
	})
}

// Test with different types to ensure generics work properly
type SimpleStruct struct {
	Data string
}

func TestOption_GenericsWithDifferentTypes(t *testing.T) {
	t.Run("works with simple struct", func(t *testing.T) {
		s := &SimpleStruct{}
		opt := NoError(func(ss *SimpleStruct) {
			ss.Data = "generic test"
		})

		err := opt.apply(s)
		require.NoError(t, err)
		require.Equal(t, "generic test", s.Data)
	})

	t.Run("works with primitive types", func(t *testing.T) {
		var num int
		opt := NoError(func(n *int) {
			*n = 42
		})

		err := opt.apply(&num)
		require.NoError(t, err)
		require.Equal(t, 42, num)
	})
}
