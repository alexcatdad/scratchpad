package domain

// Schedule is one persisted scheduling deadline. A pointer distinguishes an
// absent deadline from the valid epoch-zero deadline.
type Schedule struct {
	Entity
	NextAt *float64 `json:"nextAt"`
}

func ScheduleFrom(fields map[string]any) Schedule {
	schedule := Schedule{Entity: EntityFrom(fields)}
	if value, ok := fields["nextAt"].(float64); ok {
		schedule.NextAt = &value
	}
	return schedule
}
