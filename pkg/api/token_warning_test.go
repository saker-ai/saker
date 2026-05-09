package api

import "testing"

func TestCalculateTokenWarning_nilCompactor(t *testing.T) {
	var c *compactor
	state := c.CalculateTokenWarning(100000)
	if state.PercentUsed != 0 {
		t.Errorf("nil compactor should return zero state, got %+v", state)
	}
}

func TestCalculateTokenWarning_low(t *testing.T) {
	c := &compactor{
		cfg: CompactConfig{
			BufferTokens:    defaultBufferTokens,
			MaxOutputTokens: defaultMaxOutputTokens,
		}.withDefaults(),
		limit: 200000,
	}
	state := c.CalculateTokenWarning(50000)
	if state.IsAboveWarningThreshold {
		t.Error("50K tokens should not be above warning")
	}
	if state.IsAboveErrorThreshold {
		t.Error("50K tokens should not be above error")
	}
	if state.IsAtBlockingLimit {
		t.Error("50K tokens should not be at blocking limit")
	}
	if state.PercentUsed < 0.24 || state.PercentUsed > 0.26 {
		t.Errorf("PercentUsed = %f, expected ~0.25", state.PercentUsed)
	}
}

func TestCalculateTokenWarning_warning(t *testing.T) {
	c := &compactor{
		cfg: CompactConfig{
			BufferTokens:    defaultBufferTokens,
			MaxOutputTokens: defaultMaxOutputTokens,
		}.withDefaults(),
		limit: 200000,
	}
	// effectiveLimit = 200000 - 20000 - 13000 = 167000
	// warningThreshold = 167000 - 20000 = 147000
	state := c.CalculateTokenWarning(150000)
	if !state.IsAboveWarningThreshold {
		t.Error("150K tokens should be above warning threshold")
	}
	if state.IsAboveErrorThreshold {
		t.Error("150K tokens should not be above error threshold")
	}
}

func TestCalculateTokenWarning_error(t *testing.T) {
	c := &compactor{
		cfg: CompactConfig{
			BufferTokens:    defaultBufferTokens,
			MaxOutputTokens: defaultMaxOutputTokens,
		}.withDefaults(),
		limit: 200000,
	}
	// errorThreshold = 167000 + 20000 = 187000
	state := c.CalculateTokenWarning(190000)
	if !state.IsAboveWarningThreshold {
		t.Error("190K should be above warning")
	}
	if !state.IsAboveErrorThreshold {
		t.Error("190K should be above error")
	}
	if state.IsAtBlockingLimit {
		t.Error("190K should not be at blocking limit (need 197K)")
	}
}

func TestCalculateTokenWarning_blocking(t *testing.T) {
	c := &compactor{
		cfg: CompactConfig{
			BufferTokens:    defaultBufferTokens,
			MaxOutputTokens: defaultMaxOutputTokens,
		}.withDefaults(),
		limit: 200000,
	}
	// blockingThreshold = 200000 - 3000 = 197000
	state := c.CalculateTokenWarning(198000)
	if !state.IsAtBlockingLimit {
		t.Error("198K should be at blocking limit")
	}
	if state.PercentUsed < 0.99 {
		t.Errorf("PercentUsed = %f, expected ~0.99", state.PercentUsed)
	}
}

func TestCalculateTokenWarning_overLimit(t *testing.T) {
	c := &compactor{
		cfg: CompactConfig{
			BufferTokens:    defaultBufferTokens,
			MaxOutputTokens: defaultMaxOutputTokens,
		}.withDefaults(),
		limit: 200000,
	}
	state := c.CalculateTokenWarning(250000)
	if state.PercentUsed != 1.0 {
		t.Errorf("over-limit should cap at 1.0, got %f", state.PercentUsed)
	}
}
