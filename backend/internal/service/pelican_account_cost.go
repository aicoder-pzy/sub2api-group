package service

import (
	"encoding/json"
	"math"
)

const AccountCostMultiplierExtraKey = "cost_multiplier"

// Only Pelican test cost estimates use this extra; it never changes user billing.
// Match the reference repository's default estimate when no override is saved.
func (a *Account) CostMultiplier() float64 {
	if a == nil {
		return 0.1
	}
	var value float64
	switch v := a.Extra["cost_multiplier"].(type) {
	case float64:
		value = v
	case int:
		value = float64(v)
	case int64:
		value = float64(v)
	case json.Number:
		var err error
		value, err = v.Float64()
		if err != nil {
			return 0.1
		}
	default:
		return 0.1
	}
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1000000 {
		return 0.1
	}
	return value
}
