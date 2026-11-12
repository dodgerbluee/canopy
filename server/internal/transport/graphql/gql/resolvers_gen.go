package gql

// THIS CODE WILL BE UPDATED WITH SCHEMA CHANGES. PREVIOUS IMPLEMENTATION FOR SCHEMA CHANGES WILL BE KEPT IN THE COMMENT SECTION. IMPLEMENTATION FOR UNCHANGED SCHEMA WILL BE KEPT.

import (
	"canopy/server/internal/transport/graphql/scalars"
	"context"
)

type Resolver struct{}

// UpdateWorkspace is the resolver for the updateWorkspace field.
func (r *mutationResolver) UpdateWorkspace(ctx context.Context, input UpdateWorkspaceInput, clientMutationID *string) (*UpdateWorkspacePayload, error) {
	panic("not implemented")
}

// CreatePlant is the resolver for the createPlant field.
func (r *mutationResolver) CreatePlant(ctx context.Context, input CreatePlantInput, clientMutationID *string) (*CreatePlantPayload, error) {
	panic("not implemented")
}

// UpdatePlant is the resolver for the updatePlant field.
func (r *mutationResolver) UpdatePlant(ctx context.Context, input UpdatePlantInput, clientMutationID *string) (*UpdatePlantPayload, error) {
	panic("not implemented")
}

// ArchivePlant is the resolver for the archivePlant field.
func (r *mutationResolver) ArchivePlant(ctx context.Context, input ArchivePlantInput, clientMutationID *string) (*ArchivePlantPayload, error) {
	panic("not implemented")
}

// RestorePlant is the resolver for the restorePlant field.
func (r *mutationResolver) RestorePlant(ctx context.Context, input RestorePlantInput, clientMutationID *string) (*RestorePlantPayload, error) {
	panic("not implemented")
}

// PurgePlant is the resolver for the purgePlant field.
func (r *mutationResolver) PurgePlant(ctx context.Context, input PurgePlantInput, clientMutationID *string) (*PurgePlantPayload, error) {
	panic("not implemented")
}

// CreateLocation is the resolver for the createLocation field.
func (r *mutationResolver) CreateLocation(ctx context.Context, input CreateLocationInput, clientMutationID *string) (*CreateLocationPayload, error) {
	panic("not implemented")
}

// UpdateLocation is the resolver for the updateLocation field.
func (r *mutationResolver) UpdateLocation(ctx context.Context, input UpdateLocationInput, clientMutationID *string) (*UpdateLocationPayload, error) {
	panic("not implemented")
}

// CreateTag is the resolver for the createTag field.
func (r *mutationResolver) CreateTag(ctx context.Context, input CreateTagInput, clientMutationID *string) (*CreateTagPayload, error) {
	panic("not implemented")
}

// UpdateTag is the resolver for the updateTag field.
func (r *mutationResolver) UpdateTag(ctx context.Context, input UpdateTagInput, clientMutationID *string) (*UpdateTagPayload, error) {
	panic("not implemented")
}

// ArchiveTag is the resolver for the archiveTag field.
func (r *mutationResolver) ArchiveTag(ctx context.Context, input ArchiveTagInput, clientMutationID *string) (*ArchiveTagPayload, error) {
	panic("not implemented")
}

// SetPlantTags is the resolver for the setPlantTags field.
func (r *mutationResolver) SetPlantTags(ctx context.Context, input SetPlantTagsInput, clientMutationID *string) (*SetPlantTagsPayload, error) {
	panic("not implemented")
}

// SetPlantContext is the resolver for the setPlantContext field.
func (r *mutationResolver) SetPlantContext(ctx context.Context, input SetPlantContextInput, clientMutationID *string) (*SetPlantContextPayload, error) {
	panic("not implemented")
}

// RecordActivity is the resolver for the recordActivity field.
func (r *mutationResolver) RecordActivity(ctx context.Context, input RecordActivityInput, clientMutationID *string) (*RecordActivityPayload, error) {
	panic("not implemented")
}

// UpdateActivity is the resolver for the updateActivity field.
func (r *mutationResolver) UpdateActivity(ctx context.Context, input UpdateActivityInput, clientMutationID *string) (*UpdateActivityPayload, error) {
	panic("not implemented")
}

// VoidActivity is the resolver for the voidActivity field.
func (r *mutationResolver) VoidActivity(ctx context.Context, input VoidActivityInput, clientMutationID *string) (*VoidActivityPayload, error) {
	panic("not implemented")
}

// RecordMeasurement is the resolver for the recordMeasurement field.
func (r *mutationResolver) RecordMeasurement(ctx context.Context, input RecordMeasurementInput, clientMutationID *string) (*RecordMeasurementPayload, error) {
	panic("not implemented")
}

// UpdateMeasurement is the resolver for the updateMeasurement field.
func (r *mutationResolver) UpdateMeasurement(ctx context.Context, input UpdateMeasurementInput, clientMutationID *string) (*UpdateMeasurementPayload, error) {
	panic("not implemented")
}

// DeleteMeasurement is the resolver for the deleteMeasurement field.
func (r *mutationResolver) DeleteMeasurement(ctx context.Context, input DeleteMeasurementInput, clientMutationID *string) (*DeleteMeasurementPayload, error) {
	panic("not implemented")
}

// RecordAcquisition is the resolver for the recordAcquisition field.
func (r *mutationResolver) RecordAcquisition(ctx context.Context, input RecordAcquisitionInput, clientMutationID *string) (*RecordAcquisitionPayload, error) {
	panic("not implemented")
}

// UpdateAcquisition is the resolver for the updateAcquisition field.
func (r *mutationResolver) UpdateAcquisition(ctx context.Context, input UpdateAcquisitionInput, clientMutationID *string) (*UpdateAcquisitionPayload, error) {
	panic("not implemented")
}

// PriceAcquisition is the resolver for the priceAcquisition field.
func (r *mutationResolver) PriceAcquisition(ctx context.Context, input PriceAcquisitionInput, clientMutationID *string) (*PriceAcquisitionPayload, error) {
	panic("not implemented")
}

// CreatePurchase is the resolver for the createPurchase field.
func (r *mutationResolver) CreatePurchase(ctx context.Context, input CreatePurchaseInput, clientMutationID *string) (*CreatePurchasePayload, error) {
	panic("not implemented")
}

// UpdatePurchase is the resolver for the updatePurchase field.
func (r *mutationResolver) UpdatePurchase(ctx context.Context, input UpdatePurchaseInput, clientMutationID *string) (*UpdatePurchasePayload, error) {
	panic("not implemented")
}

// VoidPurchase is the resolver for the voidPurchase field.
func (r *mutationResolver) VoidPurchase(ctx context.Context, input VoidPurchaseInput, clientMutationID *string) (*VoidPurchasePayload, error) {
	panic("not implemented")
}

// ReserveUpload is the resolver for the reserveUpload field.
func (r *mutationResolver) ReserveUpload(ctx context.Context, input ReserveUploadInput, clientMutationID *string) (*ReserveUploadPayload, error) {
	panic("not implemented")
}

// FinalizeUpload is the resolver for the finalizeUpload field.
func (r *mutationResolver) FinalizeUpload(ctx context.Context, input FinalizeUploadInput, clientMutationID *string) (*FinalizeUploadPayload, error) {
	panic("not implemented")
}

// AttachPhoto is the resolver for the attachPhoto field.
func (r *mutationResolver) AttachPhoto(ctx context.Context, input AttachPhotoInput, clientMutationID *string) (*AttachPhotoPayload, error) {
	panic("not implemented")
}

// UpdatePhoto is the resolver for the updatePhoto field.
func (r *mutationResolver) UpdatePhoto(ctx context.Context, input UpdatePhotoInput, clientMutationID *string) (*UpdatePhotoPayload, error) {
	panic("not implemented")
}

// DeletePhoto is the resolver for the deletePhoto field.
func (r *mutationResolver) DeletePhoto(ctx context.Context, input DeletePhotoInput, clientMutationID *string) (*DeletePhotoPayload, error) {
	panic("not implemented")
}

// AttachReceipt is the resolver for the attachReceipt field.
func (r *mutationResolver) AttachReceipt(ctx context.Context, input AttachReceiptInput, clientMutationID *string) (*AttachReceiptPayload, error) {
	panic("not implemented")
}

// DetachReceipt is the resolver for the detachReceipt field.
func (r *mutationResolver) DetachReceipt(ctx context.Context, input DetachReceiptInput, clientMutationID *string) (*DetachReceiptPayload, error) {
	panic("not implemented")
}

// RequestExport is the resolver for the requestExport field.
func (r *mutationResolver) RequestExport(ctx context.Context, input RequestExportInput, clientMutationID *string) (*RequestExportPayload, error) {
	panic("not implemented")
}

// SaveAgentGrant is the resolver for the saveAgentGrant field.
func (r *mutationResolver) SaveAgentGrant(ctx context.Context, input SaveAgentGrantInput) (*AgentGrantPayload, error) {
	panic("not implemented")
}

// RotateAgentCredential is the resolver for the rotateAgentCredential field.
func (r *mutationResolver) RotateAgentCredential(ctx context.Context, input ChangeAgentCredentialInput) (*AgentGrantPayload, error) {
	panic("not implemented")
}

// RevokeAgentGrant is the resolver for the revokeAgentGrant field.
func (r *mutationResolver) RevokeAgentGrant(ctx context.Context, input ChangeAgentCredentialInput) (*AgentGrantPayload, error) {
	panic("not implemented")
}

// DecideAgentAction is the resolver for the decideAgentAction field.
func (r *mutationResolver) DecideAgentAction(ctx context.Context, input DecideAgentActionInput) (*AgentActionPayload, error) {
	panic("not implemented")
}

// SaveAIConnection is the resolver for the saveAIConnection field.
func (r *mutationResolver) SaveAIConnection(ctx context.Context, input SaveAIConnectionInput, clientMutationID *string) (*AIConnectionPayload, error) {
	panic("not implemented")
}

// SaveAIFeatureBindings is the resolver for the saveAIFeatureBindings field.
func (r *mutationResolver) SaveAIFeatureBindings(ctx context.Context, input []*AIFeatureBindingInput, clientMutationID *string) (*AIConnectionSettingsPayload, error) {
	panic("not implemented")
}

// DeleteAIConnection is the resolver for the deleteAIConnection field.
func (r *mutationResolver) DeleteAIConnection(ctx context.Context, input DeleteAIConnectionInput, clientMutationID *string) (*AIConnectionSettingsPayload, error) {
	panic("not implemented")
}

// TestAIConnection is the resolver for the testAIConnection field.
func (r *mutationResolver) TestAIConnection(ctx context.Context, input TestAIConnectionInput) (*AIConnectionTestPayload, error) {
	panic("not implemented")
}

// RequestAnalysis is the resolver for the requestAnalysis field.
func (r *mutationResolver) RequestAnalysis(ctx context.Context, input RequestAnalysisInput, clientMutationID *string) (*RequestAnalysisPayload, error) {
	panic("not implemented")
}

// CancelAnalysis is the resolver for the cancelAnalysis field.
func (r *mutationResolver) CancelAnalysis(ctx context.Context, input CancelAnalysisInput, clientMutationID *string) (*CancelAnalysisPayload, error) {
	panic("not implemented")
}

// AcceptSuggestion is the resolver for the acceptSuggestion field.
func (r *mutationResolver) AcceptSuggestion(ctx context.Context, input AcceptSuggestionInput, clientMutationID *string) (*AcceptSuggestionPayload, error) {
	panic("not implemented")
}

// DismissSuggestion is the resolver for the dismissSuggestion field.
func (r *mutationResolver) DismissSuggestion(ctx context.Context, input DismissSuggestionInput, clientMutationID *string) (*DismissSuggestionPayload, error) {
	panic("not implemented")
}

// RecordRain is the resolver for the recordRain field.
func (r *mutationResolver) RecordRain(ctx context.Context, input RecordRainInput, clientMutationID *string) (*RecordRainPayload, error) {
	panic("not implemented")
}

// VoidRain is the resolver for the voidRain field.
func (r *mutationResolver) VoidRain(ctx context.Context, input VoidRainInput, clientMutationID *string) (*VoidRainPayload, error) {
	panic("not implemented")
}

// CreateSource is the resolver for the createSource field.
func (r *mutationResolver) CreateSource(ctx context.Context, input CreateSourceInput, clientMutationID *string) (*SourcePayload, error) {
	panic("not implemented")
}

// UpdateSource is the resolver for the updateSource field.
func (r *mutationResolver) UpdateSource(ctx context.Context, input UpdateSourceInput, clientMutationID *string) (*SourcePayload, error) {
	panic("not implemented")
}

// RetireSource is the resolver for the retireSource field.
func (r *mutationResolver) RetireSource(ctx context.Context, input RetireSourceInput, clientMutationID *string) (*SourcePayload, error) {
	panic("not implemented")
}

// CreateCareProfileDraft is the resolver for the createCareProfileDraft field.
func (r *mutationResolver) CreateCareProfileDraft(ctx context.Context, input CreateCareProfileDraftInput, clientMutationID *string) (*CareProfilePayload, error) {
	panic("not implemented")
}

// UpdateCareProfileDraft is the resolver for the updateCareProfileDraft field.
func (r *mutationResolver) UpdateCareProfileDraft(ctx context.Context, input UpdateCareProfileDraftInput, clientMutationID *string) (*CareProfilePayload, error) {
	panic("not implemented")
}

// DeleteCareProfileDraft is the resolver for the deleteCareProfileDraft field.
func (r *mutationResolver) DeleteCareProfileDraft(ctx context.Context, input DeleteCareProfileDraftInput, clientMutationID *string) (*DeleteCareProfileDraftPayload, error) {
	panic("not implemented")
}

// PublishCareProfile is the resolver for the publishCareProfile field.
func (r *mutationResolver) PublishCareProfile(ctx context.Context, input PublishCareProfileInput, clientMutationID *string) (*CareProfilePayload, error) {
	panic("not implemented")
}

// RetireCareProfileVersion is the resolver for the retireCareProfileVersion field.
func (r *mutationResolver) RetireCareProfileVersion(ctx context.Context, input RetireCareProfileVersionInput, clientMutationID *string) (*CareProfilePayload, error) {
	panic("not implemented")
}

// CreateCarePlan is the resolver for the createCarePlan field.
func (r *mutationResolver) CreateCarePlan(ctx context.Context, input CreateCarePlanInput, clientMutationID *string) (*CarePlanPayload, error) {
	panic("not implemented")
}

// UpdateCarePlan is the resolver for the updateCarePlan field.
func (r *mutationResolver) UpdateCarePlan(ctx context.Context, input UpdateCarePlanInput, clientMutationID *string) (*CarePlanPayload, error) {
	panic("not implemented")
}

// CompleteOccurrence is the resolver for the completeOccurrence field.
func (r *mutationResolver) CompleteOccurrence(ctx context.Context, input CompleteOccurrenceInput, clientMutationID *string) (*CompleteOccurrencePayload, error) {
	panic("not implemented")
}

// SkipOccurrence is the resolver for the skipOccurrence field.
func (r *mutationResolver) SkipOccurrence(ctx context.Context, input SkipOccurrenceInput, clientMutationID *string) (*OccurrencePayload, error) {
	panic("not implemented")
}

// SnoozeOccurrence is the resolver for the snoozeOccurrence field.
func (r *mutationResolver) SnoozeOccurrence(ctx context.Context, input SnoozeOccurrenceInput, clientMutationID *string) (*OccurrencePayload, error) {
	panic("not implemented")
}

// CorrectOccurrenceCompletion is the resolver for the correctOccurrenceCompletion field.
func (r *mutationResolver) CorrectOccurrenceCompletion(ctx context.Context, input CorrectOccurrenceCompletionInput, clientMutationID *string) (*CorrectOccurrenceCompletionPayload, error) {
	panic("not implemented")
}

// MarkNotificationRead is the resolver for the markNotificationRead field.
func (r *mutationResolver) MarkNotificationRead(ctx context.Context, input MarkNotificationReadInput, clientMutationID *string) (*NotificationPayload, error) {
	panic("not implemented")
}

// SaveGardenKnowledgeSettings is the resolver for the saveGardenKnowledgeSettings field.
func (r *mutationResolver) SaveGardenKnowledgeSettings(ctx context.Context, input SaveGardenKnowledgeSettingsInput, clientMutationID *string) (*GardenKnowledgeSettingsPayload, error) {
	panic("not implemented")
}

// RefreshGardenKnowledge is the resolver for the refreshGardenKnowledge field.
func (r *mutationResolver) RefreshGardenKnowledge(ctx context.Context, plantID *string, clientMutationID *string) (*RefreshKnowledgePayload, error) {
	panic("not implemented")
}

// AskGarden is the resolver for the askGarden field.
func (r *mutationResolver) AskGarden(ctx context.Context, question string, year int) (*GardenAnswer, error) {
	panic("not implemented")
}

// Session is the resolver for the session field.
func (r *queryResolver) Session(ctx context.Context) (*Session, error) {
	panic("not implemented")
}

// Workspace is the resolver for the workspace field.
func (r *queryResolver) Workspace(ctx context.Context) (*Workspace, error) {
	panic("not implemented")
}

// Locations is the resolver for the locations field.
func (r *queryResolver) Locations(ctx context.Context) ([]*Location, error) {
	panic("not implemented")
}

// Tags is the resolver for the tags field.
func (r *queryResolver) Tags(ctx context.Context, first *int, after *string, filter *TagFilter) (*TagConnection, error) {
	panic("not implemented")
}

// Plants is the resolver for the plants field.
func (r *queryResolver) Plants(ctx context.Context, first *int, after *string, filter *PlantFilter, order *PlantOrder) (*PlantConnection, error) {
	panic("not implemented")
}

// Plant is the resolver for the plant field.
func (r *queryResolver) Plant(ctx context.Context, id string) (*Plant, error) {
	panic("not implemented")
}

// Journal is the resolver for the journal field.
func (r *queryResolver) Journal(ctx context.Context, plantID *string, first *int, after *string, filter *JournalFilter) (*JournalConnection, error) {
	panic("not implemented")
}

// Purchases is the resolver for the purchases field.
func (r *queryResolver) Purchases(ctx context.Context, first *int, after *string, filter *PurchaseFilter) (*PurchaseConnection, error) {
	panic("not implemented")
}

// Purchase is the resolver for the purchase field.
func (r *queryResolver) Purchase(ctx context.Context, id string) (*Purchase, error) {
	panic("not implemented")
}

// Spending is the resolver for the spending field.
func (r *queryResolver) Spending(ctx context.Context, filter *SpendingFilter) (*Spending, error) {
	panic("not implemented")
}

// PendingUploads is the resolver for the pendingUploads field.
func (r *queryResolver) PendingUploads(ctx context.Context, first *int, after *string) (*AssetConnection, error) {
	panic("not implemented")
}

// ExportJob is the resolver for the exportJob field.
func (r *queryResolver) ExportJob(ctx context.Context, id string) (*ExportJob, error) {
	panic("not implemented")
}

// AgentSettings is the resolver for the agentSettings field.
func (r *queryResolver) AgentSettings(ctx context.Context) (*AgentSettings, error) {
	panic("not implemented")
}

// AiConnections is the resolver for the aiConnections field.
func (r *queryResolver) AiConnections(ctx context.Context) (*AIConnectionSettings, error) {
	panic("not implemented")
}

// AnalysisRun is the resolver for the analysisRun field.
func (r *queryResolver) AnalysisRun(ctx context.Context, id string) (*AnalysisRun, error) {
	panic("not implemented")
}

// ProviderCapabilities is the resolver for the providerCapabilities field.
func (r *queryResolver) ProviderCapabilities(ctx context.Context) (*ProviderCapabilities, error) {
	panic("not implemented")
}

// Suggestions is the resolver for the suggestions field.
func (r *queryResolver) Suggestions(ctx context.Context, filter *SuggestionFilter, first *int, after *string) (*SuggestionConnection, error) {
	panic("not implemented")
}

// CalendarEvents is the resolver for the calendarEvents field.
func (r *queryResolver) CalendarEvents(ctx context.Context, from scalars.Date, to scalars.Date, first *int, after *string) (*CalendarEventConnection, error) {
	panic("not implemented")
}

// CareAgenda is the resolver for the careAgenda field.
func (r *queryResolver) CareAgenda(ctx context.Context, from *scalars.Date, to *scalars.Date, plantID *string, first *int, after *string) (*CareOccurrenceConnection, error) {
	panic("not implemented")
}

// CareProfiles is the resolver for the careProfiles field.
func (r *queryResolver) CareProfiles(ctx context.Context, filter *CareProfileFilter, first *int, after *string) (*CareProfileConnection, error) {
	panic("not implemented")
}

// CareProfile is the resolver for the careProfile field.
func (r *queryResolver) CareProfile(ctx context.Context, id string) (*CareProfile, error) {
	panic("not implemented")
}

// Sources is the resolver for the sources field.
func (r *queryResolver) Sources(ctx context.Context, filter *SourceFilter, first *int, after *string) (*SourceConnection, error) {
	panic("not implemented")
}

// PreviewCarePlan is the resolver for the previewCarePlan field.
func (r *queryResolver) PreviewCarePlan(ctx context.Context, input PreviewCarePlanInput) (*CarePlanPreviewPayload, error) {
	panic("not implemented")
}

// Notifications is the resolver for the notifications field.
func (r *queryResolver) Notifications(ctx context.Context, unreadOnly *bool, first *int, after *string) (*NotificationConnection, error) {
	panic("not implemented")
}

// GardenKnowledge is the resolver for the gardenKnowledge field.
func (r *queryResolver) GardenKnowledge(ctx context.Context, year int) (*GardenKnowledge, error) {
	panic("not implemented")
}

// GardenPlaces is the resolver for the gardenPlaces field.
func (r *queryResolver) GardenPlaces(ctx context.Context, search string) ([]*GardenPlace, error) {
	panic("not implemented")
}

// TodayOverview is the resolver for the todayOverview field.
func (r *queryResolver) TodayOverview(ctx context.Context) (*TodayOverview, error) {
	panic("not implemented")
}

// Mutation returns MutationResolver implementation.
func (r *Resolver) Mutation() MutationResolver { return &mutationResolver{r} }

// Query returns QueryResolver implementation.
func (r *Resolver) Query() QueryResolver { return &queryResolver{r} }

type (
	mutationResolver struct{ *Resolver }
	queryResolver    struct{ *Resolver }
)

// !!! WARNING !!!
// The code below was going to be deleted when updating resolvers. It has been copied here so you have
// one last chance to move it out of harms way if you want. There are two reasons this happens:
//  - When renaming or deleting a resolver the old code will be put in here. You can safely delete
//    it when you're done.
//  - You have helper methods in this file. Move them out to keep these resolver files clean.
/*
	type Resolver struct{}
*/
