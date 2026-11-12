# UI19 additive contract change request

Status: adopted for the explicitly requested Calendar/Add rain feature. Existing journal unions and watering semantics unchanged.

Add CalendarEvents range/paginated query (maximum 62 days), flat calendar read model and RecordRain/VoidRain mutations. Photo day means plant-photo createdAt; other journal days use occurredAt and current workspace timezone. Rain date is an explicit local date, not an inferred watering. Rain is workspace area context, optional nonnegative depth in millimeters; no plant allocation or task completion. Stable mutation receipts and revision-guarded voids allow safe retries/correction. Export/backup retain rain records.
