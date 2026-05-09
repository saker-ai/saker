package agent

import (
	"time"

	"github.com/cinience/saker/pkg/model"
)

// Context carries runtime state for a single agent execution.
type Context struct {
	Iteration       int
	StartedAt       time.Time
	Values          map[string]any
	ToolResults     []ToolResult
	LastModelOutput *ModelOutput
	// CumulativeUsage is the sum of token accounting across every
	// model.Generate call observed during the current Run. Updated by the
	// agent loop after each iteration; middleware and tools may inspect it
	// for adaptive throttling.
	CumulativeUsage model.Usage
	// CumulativeCostUSD is the running EstimateCost(ModelName, CumulativeUsage)
	// value in US dollars. Zero when ModelName is unset or pricing is unknown.
	CumulativeCostUSD float64
}

func NewContext() *Context {
	return &Context{
		StartedAt: time.Now(),
		Values:    map[string]any{},
	}
}
