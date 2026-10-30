package catalog

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// FileInfoReadRequest is shared by the explicit bounded lane routes. Kind is
// files, groups, members, tracks, head-tracks, sources or disc-titles. It is not
// a query language; HTTP validates each route's finite parameter set.
type FileInfoReadRequest struct {
	Kind, Lane, Opening, Cursor, Around, Occurrence, Group string
	Limit                                                  int
	LimitPresent                                           bool
}

func fileInfoRequestLane(req FileInfoReadRequest) (byte, error) {
	switch req.Kind {
	case "files":
		return fileInfoLaneFiles, nil
	case "groups":
		return fileInfoLaneGroups, nil
	case "members":
		return fileInfoLaneMembers, nil
	case "sources":
		return fileInfoLaneSources, nil
	case "disc-titles":
		return fileInfoLaneDisc, nil
	case "tracks", "head-tracks":
		if req.Lane == "audio" {
			return fileInfoLaneAudio, nil
		}
		if req.Lane == "subtitles" {
			return fileInfoLaneSubtitles, nil
		}
	}
	return 0, readprep.ErrInvalid
}
func (r *FileInfoRuntime) Read(ctx context.Context, caller readprep.Caller, req FileInfoReadRequest) (FileInfoWindowResult, error) {
	lane, e := fileInfoRequestLane(req)
	if e != nil {
		return FileInfoWindowResult{}, e
	}
	if req.Cursor != "" && req.Around != "" {
		return FileInfoWindowResult{}, readprep.ErrInvalid
	}
	proof, ad, e := r.opening(ctx, caller, req.Opening)
	if e != nil {
		return FileInfoWindowResult{}, e
	}
	desc := fileInfoWindowDescriptor{Opening: req.Opening, Witness: fileInfoWitness(ad), Lane: lane, Limit: FileInfoDefaultLimit, Direction: fileInfoForward, Target: fileInfoOccurrence{Item: proof.Item}}
	if req.LimitPresent {
		if req.Limit < 1 || req.Limit > FileInfoMaximumLimit {
			return FileInfoWindowResult{}, readprep.ErrInvalid
		}
		desc.Limit = req.Limit
	}
	if lane == fileInfoLaneGroups || lane == fileInfoLaneMembers || req.Kind == "head-tracks" {
		if !ad.Playable || ad.Children {
			return FileInfoWindowResult{}, readprep.ErrInvalid
		}
	}
	if req.Kind == "members" || req.Kind == "head-tracks" {
		p, e := r.resolveOccurrence(ctx, caller, req.Opening, req.Group, proof, ad, false)
		if e != nil {
			return FileInfoWindowResult{}, e
		}
		if !p.IsGroup {
			return FileInfoWindowResult{}, readprep.ErrInvalid
		}
		desc.Group = p.Group
		desc.Target = p.Coordinate
	} else if lane == fileInfoLaneAudio || lane == fileInfoLaneSubtitles || lane == fileInfoLaneDisc {
		p, e := r.resolveOccurrence(ctx, caller, req.Opening, req.Occurrence, proof, ad, false)
		if e != nil {
			return FileInfoWindowResult{}, e
		}
		if p.IsGroup {
			return FileInfoWindowResult{}, readprep.ErrInvalid
		}
		if lane == fileInfoLaneDisc && (!ad.Owner || !p.DiscWinner) {
			return FileInfoWindowResult{}, readprep.ErrStale
		}
		desc.Target = p.Coordinate
	}
	if req.Around != "" {
		if lane != fileInfoLaneFiles && lane != fileInfoLaneGroups {
			return FileInfoWindowResult{}, readprep.ErrInvalid
		}
		p, e := r.resolveOccurrence(ctx, caller, req.Opening, req.Around, proof, ad, true)
		if e != nil {
			return FileInfoWindowResult{}, e
		}
		desc.Anchor = p.Coordinate
		if lane == fileInfoLaneGroups && p.IsGroup {
			desc.Anchor = fileInfoOccurrence{Item: p.Group.ShapeHead, Asset: p.Group.Head}
		}
		desc.HasAnchor, desc.Direction = true, fileInfoReload
	}
	plan := r.lanePlan
	if req.Cursor != "" {
		signed, e := r.anchors.decodeWindowAnchor(caller, req.Cursor, fileInfoOpeningDigest(req.Opening))
		if e != nil {
			return FileInfoWindowResult{}, e
		}
		if signed.Lane != lane {
			return FileInfoWindowResult{}, readprep.ErrStale
		}
		// The private descriptor must also agree with route-specific target/group.
		plan = func(c context.Context, who readprep.Caller, p fileInfoOpeningProof, a FileInfoAdmission, d fileInfoWindowDescriptor) (readprep.Plan, error) {
			if d.Target != desc.Target || d.Group != desc.Group {
				return readprep.Plan{}, readprep.ErrStale
			}
			return r.lanePlan(c, who, p, a, d)
		}
		return r.windowContinue(ctx, caller, req.Opening, req.Cursor, req.Limit, req.LimitPresent, plan, r.hydrateWindow)
	}
	return r.windowSubmit(ctx, caller, desc, plan, r.hydrateWindow, time.Time{})
}
func (r *FileInfoRuntime) Poll(ctx context.Context, caller readprep.Caller, id string) (FileInfoWindowResult, error) {
	if strings.HasPrefix(id, "open.") {
		return r.pollOpening(ctx, caller, id)
	}
	return r.windowPoll(ctx, caller, id, r.hydrateWindow)
}
func (r *FileInfoRuntime) Cancel(ctx context.Context, caller readprep.Caller, id string) error {
	if strings.HasPrefix(id, "open.") {
		h, e := fileInfoOpeningHandle(id)
		if e != nil {
			return nil
		}
		return r.store.CancelNamespace(ctx, caller, h, fileInfoOpeningNamespace)
	}
	return r.windowCancel(ctx, caller, id)
}

func (r *FileInfoRuntime) acceptedTrackStat(ctx context.Context, asset int64) (size, modified int64, err error) {
	err = dbwork.ReadHandle(ctx, r.db).QueryRowContext(ctx, `SELECT `+fileInfoGroupSizeFrame+`,COALESCE((SELECT o.old_integer FROM catalog_info_changes c JOIN catalog_info_scalar_old o ON o.change_id=c.id AND o.field=3 WHERE c.asset_id=a.id AND c.asset_token=a.token AND c.phase<3 ORDER BY c.slot LIMIT 1),a.modified_ns) FROM catalog_assets a WHERE a.id=?`, asset).Scan(&size, &modified)
	if errors.Is(err, sql.ErrNoRows) {
		err = readprep.ErrStale
	}
	return
}
func (r *FileInfoRuntime) lanePlan(ctx context.Context, caller readprep.Caller, proof fileInfoOpeningProof, ad FileInfoAdmission, d fileInfoWindowDescriptor) (readprep.Plan, error) {
	// The shared worker owns its own cancellation context. Capture only the
	// request's live guard (not its deadline or snapshot), then install that
	// guard on each worker quantum so a missing dispatcher never authorizes.
	guard, ok := ctx.Value(fileInfoAuthorityContextKey{}).(FileInfoAuthorityCheck)
	if !ok {
		guard = r.check
	}
	check := func(c context.Context) error {
		_, e := r.checkWindowBasis(WithFileInfoAuthority(c, guard), caller, proof, d.Witness)
		return e
	}
	capture := int64(8192+len(caller)+len(d.Opening)) + ad.CaptureBytes
	if capture < 0 {
		return readprep.Plan{}, readprep.ErrCapacity
	}
	var plan readprep.Plan
	var e error
	ordered := fileInfoOrderedSpec{Limit: d.Limit, Anchor: d.Anchor, HasAnchor: d.HasAnchor, Direction: d.Direction}
	switch d.Lane {
	case fileInfoLaneFiles, fileInfoLaneSources:
		var source *fileInfoFlatSource
		source, e = newFileInfoFlatSource(r.db, proof.Item, ad.Children, capture, check, ad.ChildVisible)
		if e == nil {
			plan, e = fileInfoOrderedPlan(source, ordered)
		}
	case fileInfoLaneGroups:
		split := ad.Kind == "movie" || ad.Kind == "song" || ad.Kind == "audiobook_file"
		plan, e = fileInfoGroupPlan(r.db, proof.Item, ad.Owner, split, fileInfoGroupSpec{Limit: d.Limit, Anchor: d.Anchor, HasAnchor: d.HasAnchor, Direction: d.Direction}, capture, check)
	case fileInfoLaneMembers:
		var source fileInfoOrderedSource
		source, e = newFileInfoMemberSource(r.db, proof.Item, d.Group, capture, check)
		if e == nil {
			plan, e = fileInfoOrderedPlan(source, ordered)
		}
	case fileInfoLaneAudio, fileInfoLaneSubtitles:
		var size, modified int64
		size, modified, e = r.acceptedTrackStat(ctx, d.Target.Asset)
		if e != nil {
			break
		}
		var source fileInfoOrderedSource
		if d.Group.Type != 0 {
			source, e = newFileInfoHeadTrackSource(r.db, d.Target.Item, d.Target.Asset, size, modified, d.Lane, ad.SubtitleActor, capture, check)
			if e == nil && d.Lane == fileInfoLaneSubtitles && !ad.DiscSubtitlesAvailable {
				source = &fileInfoHeadCapabilitySource{raw: source, db: r.db}
			}
		} else {
			source, e = newFileInfoTrackSource(r.db, d.Target.Asset, size, modified, d.Lane, capture, check)
		}
		if e == nil {
			plan, e = fileInfoOrderedPlan(source, ordered)
		}
	case fileInfoLaneDisc:
		if !ad.Owner {
			return readprep.Plan{}, readprep.ErrStale
		}
		plan, e = fileInfoIDPlan(&fileInfoDiscSource{db: r.db, asset: d.Target.Asset, bytes: capture, check: check}, fileInfoIDSpec{Limit: d.Limit, Anchor: d.Row, HasAnchor: d.HasAnchor, Direction: d.Direction})
	default:
		return readprep.Plan{}, readprep.ErrInvalid
	}
	if e != nil {
		return readprep.Plan{}, e
	}
	if d.Lane == fileInfoLaneFiles || d.Lane == fileInfoLaneGroups || d.Lane == fileInfoLaneMembers {
		options := fileInfoDecorationOptions{}
		if ad.Owner && (d.Lane == fileInfoLaneFiles || d.Lane == fileInfoLaneGroups) {
			options.Surface, e = newFileInfoFlatSource(r.db, proof.Item, ad.Children, capture, check, ad.ChildVisible)
			if e != nil {
				return readprep.Plan{}, e
			}
			options.DiscCheck = r.discCandidate
		}
		plan, e = fileInfoDecoratedSurfacePlan(plan, r.db, d.Lane, d.Target, capture, check, options)
		if e != nil {
			return readprep.Plan{}, e
		}
		if d.Lane == fileInfoLaneFiles && ad.Playable && !ad.Children {
			groups, err := fileInfoGroupPlan(r.db, proof.Item, ad.Owner, ad.Kind == "movie" || ad.Kind == "song" || ad.Kind == "audiobook_file", fileInfoGroupSpec{Limit: 1, Direction: fileInfoForward}, capture, check)
			if err != nil {
				return readprep.Plan{}, err
			}
			return fileInfoSurfacePlan(plan, groups)
		}
		return plan, nil
	}
	return plan, nil
}
