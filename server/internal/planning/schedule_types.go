package planning

import "time"

const ScheduleVersion = 1
const MaxScheduleAnchors = 6
const MaxProjectedWindows = MaxGuidanceFacts * MaxGuidanceWindows * 3

// An anchor is an explicit planning assumption, never an observed garden event.
type ScheduleAnchor struct {
	Kind string `json:"kind"`
	On   string `json:"on"`
}
type ScheduleAssumptions struct {
	Hemisphere *string          `json:"hemisphere,omitempty"`
	Anchors    []ScheduleAnchor `json:"anchors"`
}
type ScheduleInput struct {
	Range            MonthRange
	Assumptions      ScheduleAssumptions
	Today            string
	Method           string
	Target           *Period
	TargetHemisphere *string
	Facts            []GuidanceFact
}

// Period is source precision. DisplayMonths is a clipped rendering band and
// must never be used as an asserted planting day or a replacement source period.
type ProjectedWindow struct {
	ID                  string
	FactID              string
	WindowIndex, Year   int
	Phase, Label        string
	Resolution, Message string
	Status              string
	Period              *Period
	DisplayMonths       *MonthRange
	ContinuesBefore     bool
	ContinuesAfter      bool
	Estimated           bool
	FirstHarvestUnknown bool
	Selectable          bool
}
type ScheduleProjection struct {
	TargetStatus        string
	TargetDisplayMonths *MonthRange
	TargetVisible       bool
	Windows             []ProjectedWindow
}

// Stored with the accepted snapshot pointer. Refresh never rewrites this
// decision; manual target-period changes clear both pieces of acceptance.
type ScheduleSelection struct {
	Version         int                 `json:"version"`
	FactID          string              `json:"factId"`
	WindowIndex     int                 `json:"windowIndex"`
	Year            int                 `json:"year"`
	Assumptions     ScheduleAssumptions `json:"assumptions"`
	SupportedPeriod *Period             `json:"supportedPeriod"`
	ConfirmedAt     time.Time           `json:"confirmedAt"`
}
type ConfirmScheduleInput struct {
	PlanID, SnapshotID string
	ExpectedRevision   int32
	Generation         int32
	FactID             string
	WindowIndex, Year  int
	Assumptions        ScheduleAssumptions
	TargetPeriod       *Period
}
type ScheduleQuery struct {
	PlanID      string
	Range       MonthRange
	Assumptions ScheduleAssumptions
	First       int
	After       *string
}
type SchedulePage struct {
	PlanID, Timezone, Today string
	Revision, Generation    int32
	SourceSnapshotID        *string
	GuidanceState, Message  string
	GuidanceCurrent         bool
	Assumptions             ScheduleAssumptions
	TargetPeriod            *Period
	TargetHemisphere        *string
	Selection               *ScheduleSelection
	ScheduleProjection
	HasNext   bool
	EndCursor *string
}
