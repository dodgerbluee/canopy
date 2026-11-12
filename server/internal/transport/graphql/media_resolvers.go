// Upload lifecycle resolvers (T13): reserveUpload / finalizeUpload /
// pendingUploads. Bytes never travel through GraphQL — the payload
// points the client at PUT /api/uploads/{assetId}.
package graphql

import (
	"context"
	"time"

	"canopy/server/internal/media"
	"canopy/server/internal/transport/graphql/gql"
	"canopy/server/internal/transport/graphql/scalars"
	httpx "canopy/server/internal/transport/http"
)

func mapMediaProblems(ps []media.Problem) []*gql.MutationProblem {
	out := make([]*gql.MutationProblem, 0, len(ps))
	for _, p := range ps {
		mp := &gql.MutationProblem{
			Code:    gql.ProblemCode(p.Code),
			Message: p.Message,
		}
		if p.Field != "" {
			f := p.Field
			mp.Field = &f
		}
		if p.CurrentRevision != nil {
			rev := int(*p.CurrentRevision)
			mp.CurrentRevision = &rev
		}
		out = append(out, mp)
	}
	return out
}

func mActor(p httpx.Principal) media.Actor {
	return media.Actor{WorkspaceID: p.WorkspaceID, OwnerID: p.OwnerID, SessionID: p.SessionID}
}

func assetOut(v media.AssetView) *gql.Asset {
	a := &gql.Asset{
		ID:        v.ID,
		Purpose:   gql.AssetPurpose(v.Purpose),
		State:     gql.AssetState(v.State),
		ByteCount: int(v.ByteCount),
		Sha256:    v.Sha256,
		MimeType:  v.MimeType,
		Variants:  []*gql.MediaVariant{},
		CreatedAt: scalars.DateTime(v.CreatedAt),
		UpdatedAt: scalars.DateTime(v.UpdatedAt),
	}
	if v.DisplayName != nil {
		a.DisplayName = v.DisplayName
	}
	if v.ErrorCode != nil {
		a.ErrorCode = v.ErrorCode
	}
	if v.ExpiresAt != nil {
		d := scalars.DateTime(*v.ExpiresAt)
		a.ExpiresAt = &d
	}
	for _, vr := range v.Variants {
		mv := &gql.MediaVariant{Name: vr.Name}
		if vr.WidthPx != nil {
			w := int(*vr.WidthPx)
			mv.WidthPx = &w
		}
		if vr.HeightPx != nil {
			h := int(*vr.HeightPx)
			mv.HeightPx = &h
		}
		a.Variants = append(a.Variants, mv)
	}
	return a
}

func (m mutationResolver) ReserveUpload(ctx context.Context, in gql.ReserveUploadInput, c *string) (*gql.ReserveUploadPayload, error) {
	if m.r.Media == nil {
		return nil, capabilityUnavailable("reserveUpload")
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	res, problems, err := m.r.Media.Reserve(ctx, mActor(p), media.ReserveInput{
		Purpose:      string(in.Purpose),
		Filename:     in.Filename,
		ClaimedBytes: int64(in.ClaimedBytes),
	}, c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(problems) > 0 {
		return &gql.ReserveUploadPayload{Problems: mapMediaProblems(problems), ClientMutationID: c}, nil
	}
	return &gql.ReserveUploadPayload{Asset: assetOut(res), Problems: []*gql.MutationProblem{}, ClientMutationID: c}, nil
}

func (m mutationResolver) FinalizeUpload(ctx context.Context, in gql.FinalizeUploadInput, c *string) (*gql.FinalizeUploadPayload, error) {
	if m.r.Media == nil {
		return nil, capabilityUnavailable("finalizeUpload")
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	res, problems, err := m.r.Media.Finalize(ctx, mActor(p), in.AssetID, in.Sha256, c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(problems) > 0 {
		return &gql.FinalizeUploadPayload{Problems: mapMediaProblems(problems), ClientMutationID: c}, nil
	}
	return &gql.FinalizeUploadPayload{Asset: assetOut(res), Problems: []*gql.MutationProblem{}, ClientMutationID: c}, nil
}

func (q queryResolver) PendingUploads(ctx context.Context, first *int, after *string) (*gql.AssetConnection, error) {
	if q.r.Media == nil {
		return nil, capabilityUnavailable("pendingUploads")
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	n := 25
	if first != nil {
		n = *first
	}
	page, err := q.r.Media.PendingUploads(ctx, p.WorkspaceID, n, after)
	if err != nil {
		if err == media.ErrInvalidCursor {
			return nil, invalidCursorError()
		}
		return nil, internalError(ctx, err)
	}
	items := make([]*gql.Asset, 0, len(page.Items))
	for _, v := range page.Items {
		items = append(items, assetOut(v))
	}
	pageInfo := &gql.PageInfo{HasNextPage: page.HasNext}
	if page.EndCursor != "" {
		ec := page.EndCursor
		pageInfo.EndCursor = &ec
	}
	return &gql.AssetConnection{Items: items, PageInfo: pageInfo}, nil
}

// ---- attachment mutations (T15) ---------------------------------------------

func photoOut(v media.PhotoView) *gql.PlantPhoto {
	out := &gql.PlantPhoto{
		ID:         v.ID,
		PlantID:    v.PlantID,
		Asset:      assetOut(v.Asset),
		DateSource: gql.PhotoDateSource(v.DateSource),
		Viewpoint:  gql.PhotoViewpoint(v.Viewpoint),
		IsCover:    v.IsCover,
		Revision:   int(v.Revision),
		CreatedAt:  scalars.DateTime(v.CreatedAt),
		UpdatedAt:  scalars.DateTime(v.UpdatedAt),
	}
	if v.TakenAt != nil {
		d := scalars.DateTime(*v.TakenAt)
		out.TakenAt = &d
	}
	if v.Timezone != nil {
		out.Timezone = v.Timezone
	}
	if v.Caption != nil {
		out.Caption = v.Caption
	}
	if v.DeletedAt != nil {
		d := scalars.DateTime(*v.DeletedAt)
		out.DeletedAt = &d
	}
	return out
}

func receiptOut(v media.ReceiptView) *gql.PurchaseReceipt {
	return &gql.PurchaseReceipt{ID: v.ID, PurchaseID: v.PurchaseID, Asset: assetOut(v.Asset)}
}

func (m mutationResolver) AttachPhoto(ctx context.Context, in gql.AttachPhotoInput, c *string) (*gql.AttachPhotoPayload, error) {
	if m.r.Media == nil {
		return nil, capabilityUnavailable("attachPhoto")
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	var takenAt *time.Time
	if in.TakenAt != nil {
		t := time.Time(*in.TakenAt)
		takenAt = &t
	}
	res, problems, err := m.r.Media.AttachPhoto(ctx, mActor(p), media.AttachPhotoInput{
		PreferCaptureDate: in.PreferCaptureDate != nil && *in.PreferCaptureDate,
		PlantID:           in.PlantID,
		AssetID:           in.AssetID,
		TakenAt:           takenAt,
		DateSource:        string(in.DateSource),
		Timezone:          in.Timezone,
		Caption:           in.Caption,
		Viewpoint:         string(in.Viewpoint),
		IsCover:           in.IsCover,
	}, c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(problems) > 0 {
		return &gql.AttachPhotoPayload{Problems: mapMediaProblems(problems), ClientMutationID: c}, nil
	}
	return &gql.AttachPhotoPayload{Photo: photoOut(res), Problems: []*gql.MutationProblem{}, ClientMutationID: c}, nil
}

func (m mutationResolver) UpdatePhoto(ctx context.Context, in gql.UpdatePhotoInput, c *string) (*gql.UpdatePhotoPayload, error) {
	if m.r.Media == nil {
		return nil, capabilityUnavailable("updatePhoto")
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	svcIn := media.UpdatePhotoInput{
		ID:               in.ID,
		ExpectedRevision: int32(in.ExpectedRevision),
		Timezone:         in.Timezone,
		Caption:          in.Caption,
		IsCover:          in.IsCover,
	}
	if in.TakenAt != nil {
		t := time.Time(*in.TakenAt)
		svcIn.TakenAt = &t
	}
	if in.DateSource != nil {
		ds := string(*in.DateSource)
		svcIn.DateSource = &ds
	}
	if in.Viewpoint != nil {
		vp := string(*in.Viewpoint)
		svcIn.Viewpoint = &vp
	}
	res, problems, err := m.r.Media.UpdatePhoto(ctx, mActor(p), svcIn, c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(problems) > 0 {
		return &gql.UpdatePhotoPayload{Problems: mapMediaProblems(problems), ClientMutationID: c}, nil
	}
	return &gql.UpdatePhotoPayload{Photo: photoOut(res), Problems: []*gql.MutationProblem{}, ClientMutationID: c}, nil
}

func (m mutationResolver) DeletePhoto(ctx context.Context, in gql.DeletePhotoInput, c *string) (*gql.DeletePhotoPayload, error) {
	if m.r.Media == nil {
		return nil, capabilityUnavailable("deletePhoto")
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	id, problems, err := m.r.Media.DeletePhoto(ctx, mActor(p), in.ID, int32(in.ExpectedRevision), c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(problems) > 0 {
		return &gql.DeletePhotoPayload{Problems: mapMediaProblems(problems), ClientMutationID: c}, nil
	}
	return &gql.DeletePhotoPayload{DeletedPhotoID: &id, Problems: []*gql.MutationProblem{}, ClientMutationID: c}, nil
}

func (m mutationResolver) AttachReceipt(ctx context.Context, in gql.AttachReceiptInput, c *string) (*gql.AttachReceiptPayload, error) {
	if m.r.Media == nil {
		return nil, capabilityUnavailable("attachReceipt")
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	res, problems, err := m.r.Media.AttachReceipt(ctx, mActor(p), media.AttachReceiptInput{
		PurchaseID: in.PurchaseID, AssetID: in.AssetID,
	}, c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(problems) > 0 {
		return &gql.AttachReceiptPayload{Problems: mapMediaProblems(problems), ClientMutationID: c}, nil
	}
	return &gql.AttachReceiptPayload{Receipt: receiptOut(res), Problems: []*gql.MutationProblem{}, ClientMutationID: c}, nil
}

func (m mutationResolver) DetachReceipt(ctx context.Context, in gql.DetachReceiptInput, c *string) (*gql.DetachReceiptPayload, error) {
	if m.r.Media == nil {
		return nil, capabilityUnavailable("detachReceipt")
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	id, problems, err := m.r.Media.DetachReceipt(ctx, mActor(p), media.DetachReceiptInput{
		PurchaseID: in.PurchaseID, ReceiptID: in.ReceiptID, ExpectedRevision: int32(in.ExpectedRevision),
	}, c)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if len(problems) > 0 {
		return &gql.DetachReceiptPayload{Problems: mapMediaProblems(problems), ClientMutationID: c}, nil
	}
	return &gql.DetachReceiptPayload{DetachedReceiptID: &id, Problems: []*gql.MutationProblem{}, ClientMutationID: c}, nil
}
