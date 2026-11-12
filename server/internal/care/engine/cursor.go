package engine

// Decision kinds that resolve an occurrence for cadence purposes.
type Decision string

const (
	DecisionCompleted Decision = "COMPLETED"
	DecisionSkipped   Decision = "SKIPPED"
)

// Cursor is the rule-level recurrence cursor for completion-relative
// intervals (03: stored independently from activity history). It records
// the latest resolved decision and the next due date it implies.
type Cursor struct {
	LastOrdinal  int64
	LastDecision Decision
	AnchorDate   Date
	NextDueDate  Date
}

// Complete anchors the next due date to the actual completion local date
// (occurredOn may be backdated) + N.
func (c Cursor) Complete(ordinal int64, occurredOn Date, days int) Cursor {
	c.LastOrdinal = ordinal
	c.LastDecision = DecisionCompleted
	c.AnchorDate = occurredOn
	c.NextDueDate = occurredOn.AddDays(days)
	return c
}

// Skip anchors the next due date to the occurrence's ORIGINAL scheduled due
// date + N — never the click date and never the snoozed date. Repeated
// skips each advance one original cycle.
func (c Cursor) Skip(ordinal int64, originalDue Date, days int) Cursor {
	c.LastOrdinal = ordinal
	c.LastDecision = DecisionSkipped
	c.AnchorDate = originalDue
	c.NextDueDate = originalDue.AddDays(days)
	return c
}

// CorrectLatestCompletion recalculates only the current uncompleted work
// when the LATEST resolved decision was a completion and its occurredOn is
// corrected. If a later decision exists, callers must not call this: newer
// history is authoritative and the older occurrence becomes
// COMPLETION_RETRACTED instead (a T23 persistence concern).
func (c Cursor) CorrectLatestCompletion(newOccurredOn Date, days int) Cursor {
	if c.LastDecision != DecisionCompleted {
		return c
	}
	c.AnchorDate = newOccurredOn
	c.NextDueDate = newOccurredOn.AddDays(days)
	return c
}

// Snooze never touches cadence: it changes an occurrence's effective due
// date (a field on the occurrence) and leaves the cursor alone. This
// no-op exists to make that rule explicit at call sites.
func (c Cursor) Snooze() Cursor { return c }
