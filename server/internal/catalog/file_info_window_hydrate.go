package catalog

import (
	"context"
	"math/big"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

func (r *FileInfoRuntime) windowBasis(caller readprep.Caller, proof fileInfoOpeningProof, ad FileInfoAdmission, d fileInfoWindowDescriptor, id string) FileInfoWindowBasis {
	return FileInfoWindowBasis{Scope: ad.Scope, Detail: FileInfoDetailCapture{Scope: ad.DetailScope, Revision: proof.Revision}, Basis: fileInfoWindowBasis(d.Opening), Window: FileInfoWindow{QueryID: id, Limit: d.Limit}}
}
func (r *FileInfoRuntime) windowEdges(caller readprep.Caller, d fileInfoWindowDescriptor, id string, w *FileInfoWindow, coords []fileInfoOccurrence, rows []int64, earlier, later bool) error {
	w.Returned = len(coords)
	if len(coords) == 0 {
		return nil
	}
	row := func(i int) int64 {
		if rows != nil {
			return rows[i]
		}
		return 0
	}
	var e error
	w.ReloadCursor, e = r.signFileInfoWindowAnchor(caller, d.Opening, id, coords[0], row(0), fileInfoReload)
	if e != nil {
		return e
	}
	if earlier {
		w.PreviousCursor, e = r.signFileInfoWindowAnchor(caller, d.Opening, id, coords[0], row(0), fileInfoBackward)
		if e != nil {
			return e
		}
	}
	if later {
		last := len(coords) - 1
		w.NextCursor, e = r.signFileInfoWindowAnchor(caller, d.Opening, id, coords[last], row(last), fileInfoForward)
	}
	return e
}
func fileInfoManageFor(ad FileInfoAdmission) *FileInfoManage {
	if !ad.Owner {
		return nil
	}
	return &FileInfoManage{Delete: true, Split: ad.Kind == "movie" || ad.Kind == "song" || ad.Kind == "audiobook_file", Merge: true}
}
func (r *FileInfoRuntime) windowSurface(ctx context.Context, caller readprep.Caller, proof fileInfoOpeningProof, ad FileInfoAdmission, d fileInfoWindowDescriptor, files, eligible, groups int64, split bool, winner fileInfoOccurrence) (FileInfoSurface, error) {
	surface := FileInfoSurface{FileCount: files, EligibleFileCount: eligible, GroupCount: groups, CanSplit: split, Manage: fileInfoManageFor(ad)}
	if ad.Owner && winner.Asset > 0 {
		var e error
		surface.OwnerDiscOccurrence, e = r.issueDiscWinner(ctx, caller, d.Opening, proof, ad, winner)
		if e != nil {
			return surface, e
		}
		surface.HasDiscColumns = true
	}
	return surface, nil
}
func (r *FileInfoRuntime) hydrateWindow(ctx context.Context, caller readprep.Caller, proof fileInfoOpeningProof, ad FileInfoAdmission, d fileInfoWindowDescriptor, result readprep.Result, id string) (FileInfoWindowResult, error) {
	if e := result.ResizeDelivery(32768); e != nil {
		return FileInfoWindowResult{}, e
	}
	budget := fileInfoHydrationBudget{result: result, maximum: r.store.MaxBytes(), used: 32768}
	basis := r.windowBasis(caller, proof, ad, d, id)
	raw, extraGroups, extraSplit, e := splitFileInfoSurfacePayload(result.Payload)
	if e != nil {
		return FileInfoWindowResult{}, e
	}
	var decorations []fileInfoDecoration
	var winner fileInfoOccurrence
	if d.Lane == fileInfoLaneFiles || d.Lane == fileInfoLaneGroups || d.Lane == fileInfoLaneMembers {
		raw, decorations, winner, e = decodeFileInfoDecoratedSummary(raw)
		if e != nil {
			return FileInfoWindowResult{}, e
		}
	}
	var coords []fileInfoOccurrence
	if d.Lane == fileInfoLaneDisc {
		summary, e := decodeFileInfoIDs(raw)
		if e != nil {
			return FileInfoWindowResult{}, e
		}
		page := FileInfoDiscPage{FileInfoWindowBasis: basis, Kind: "file-info-disc-titles", Total: summary.Total, Titles: []FileInfoDiscTitle{}}
		page.Occurrence, e = r.issueDiscWinner(ctx, caller, d.Opening, proof, ad, d.Target)
		if e != nil {
			return FileInfoWindowResult{}, e
		}
		coords = make([]fileInfoOccurrence, 0, len(summary.IDs))
		for _, number := range summary.IDs {
			title, kind, e := r.hydrateDiscTitle(ctx, d.Target.Asset, number)
			if e != nil {
				return FileInfoWindowResult{}, e
			}
			page.DiscKind = kind
			page.Titles = append(page.Titles, title)
			coords = append(coords, d.Target)
		}
		if len(summary.IDs) == 0 {
			var kind string
			e = dbwork.ReadHandle(ctx, r.db).QueryRowContext(ctx, `SELECT d.kind FROM catalog_assets a NOT INDEXED CROSS JOIN inventory_disc_facts d ON d.object_id=a.info_owner_object AND d.source_revision=a.info_owner_revision WHERE a.id=?`, d.Target.Asset).Scan(&kind)
			if e != nil {
				return FileInfoWindowResult{}, e
			}
			page.DiscKind = FileInfoDiscKind(kind)
		}
		e = r.windowEdges(caller, d, id, &page.Window, coords, summary.IDs, summary.Earlier, summary.Later)
		return FileInfoWindowResult{Disc: &page}, e
	}
	if d.Lane == fileInfoLaneGroups {
		summary, e := decodeFileInfoGroupSummary(raw)
		if e != nil {
			return FileInfoWindowResult{}, e
		}
		page := FileInfoGroupsPage{FileInfoWindowBasis: basis, Kind: "file-info-groups", Groups: []FileInfoGroup{}}
		page.Surface, e = r.windowSurface(ctx, caller, proof, ad, d, summary.Files, summary.Eligible, summary.Total, summary.CanSplit, winner)
		if e != nil {
			return FileInfoWindowResult{}, e
		}
		for i, g := range summary.Groups {
			coord := fileInfoOccurrence{Item: proof.Item, Asset: g.Head}
			meta := decorations[i]
			bytes, e := r.fileRowBytes(ctx, coord, ad.Owner, meta.VideoRow)
			if e != nil {
				return FileInfoWindowResult{}, e
			}
			keep, e := budget.row(bytes)
			if e != nil {
				return FileInfoWindowResult{}, e
			}
			if !keep {
				break
			}
			head, e := r.hydrateFile(ctx, caller, d.Opening, proof, ad, coord, meta.VideoRow)
			if e != nil {
				return FileInfoWindowResult{}, e
			}
			group := FileInfoGroup{HeadOccurrence: head.Occurrence, Ordinal: summary.FirstOrdinal + int64(i), MemberCount: g.Members, Edition: head.Edition, Container: head.Container, Video: head.Video, AudioCount: meta.Audio, SubtitleCount: meta.Subtitles}
			if group.Edition == "" {
				e = dbwork.ReadHandle(ctx, r.db).QueryRowContext(ctx, `SELECT COALESCE((SELECT edition FROM catalog_item_details WHERE entity_id=?),'')`, proof.Item).Scan(&group.Edition)
				if e != nil {
					return FileInfoWindowResult{}, e
				}
			}
			group.SizeBytes, group.SizeBytesExact = fileInfoGroupInteger(g.SizeBytes, g.SizeHigh)
			if g.DurationBad || g.DurationHigh != 0 || g.DurationMS > fileInfoSafeInteger || g.DurationMS < -fileInfoSafeInteger {
				group.DurationState = "unrepresentable"
			} else {
				n := g.DurationMS
				group.DurationMs = &n
			}
			if g.Type == fileInfoGroupSingleton {
				group.Disc = head.Disc
			}
			group.ID, e = r.issueOccurrence(ctx, caller, d.Opening, proof, ad, coord, &g)
			if e != nil {
				return FileInfoWindowResult{}, e
			}
			page.Groups = append(page.Groups, group)
			coords = append(coords, fileInfoOccurrence{Item: g.ShapeHead, Asset: g.Head})
		}
		e = r.windowEdges(caller, d, id, &page.Window, coords, nil, summary.Earlier, summary.Later || len(page.Groups) < len(summary.Groups))
		return FileInfoWindowResult{Groups: &page}, e
	}
	summary, e := decodeFileInfoOrderedSummary(raw)
	if e != nil {
		return FileInfoWindowResult{}, e
	}
	switch d.Lane {
	case fileInfoLaneFiles, fileInfoLaneMembers:
		files := make([]FileInfoFile, 0, len(summary.Occurrences))
		for i, coord := range summary.Occurrences {
			meta := decorations[i]
			bytes, e := r.fileRowBytes(ctx, coord, ad.Owner, meta.VideoRow)
			if e != nil {
				return FileInfoWindowResult{}, e
			}
			keep, e := budget.row(bytes)
			if e != nil {
				return FileInfoWindowResult{}, e
			}
			if !keep {
				break
			}
			row, e := r.hydrateFile(ctx, caller, d.Opening, proof, ad, coord, meta.VideoRow)
			if e != nil {
				return FileInfoWindowResult{}, e
			}
			row.AudioCount, row.SubtitleCount = meta.Audio, meta.Subtitles
			files = append(files, row)
			coords = append(coords, coord)
		}
		e = r.windowEdges(caller, d, id, &basis.Window, coords, nil, summary.Earlier, summary.Later || len(files) < len(summary.Occurrences))
		if e != nil {
			return FileInfoWindowResult{}, e
		}
		if d.Lane == fileInfoLaneMembers {
			group, e := r.issueOccurrence(ctx, caller, d.Opening, proof, ad, d.Target, &d.Group)
			if e != nil {
				return FileInfoWindowResult{}, e
			}
			return FileInfoWindowResult{Members: &FileInfoMembersPage{FileInfoWindowBasis: basis, Kind: "file-info-members", GroupID: group, Total: summary.Total, Members: files}}, nil
		}
		surface, e := r.windowSurface(ctx, caller, proof, ad, d, summary.Total, summary.Eligible, extraGroups, extraSplit, winner)
		if e != nil {
			return FileInfoWindowResult{}, e
		}
		return FileInfoWindowResult{Files: &FileInfoFilesPage{FileInfoWindowBasis: basis, Kind: "file-info-files", ItemKind: ad.Kind, Surface: surface, Files: files}}, nil
	case fileInfoLaneSources:
		page := FileInfoSourcesPage{FileInfoWindowBasis: basis, Kind: "file-info-sources", Total: summary.Total, Sources: []FileInfoSource{}}
		for _, coord := range summary.Occurrences {
			bytes, e := r.fileRowBytes(ctx, coord, false, 0)
			if e != nil {
				return FileInfoWindowResult{}, e
			}
			keep, e := budget.row(bytes)
			if e != nil {
				return FileInfoWindowResult{}, e
			}
			if !keep {
				break
			}
			var token, public string
			e = dbwork.ReadHandle(ctx, r.db).QueryRowContext(ctx, `SELECT a.token,pid(e.public_id) FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id JOIN catalog_entities e ON e.id=l.entity_id WHERE l.entity_id=? AND l.asset_id=?`, coord.Item, coord.Asset).Scan(&token, &public)
			if e != nil {
				return FileInfoWindowResult{}, e
			}
			frame, e := compactcatalog.ReadInfoAsset(ctx, dbwork.ReadHandle(ctx, r.db), coord.Asset, token, false)
			if e != nil {
				return FileInfoWindowResult{}, e
			}
			occ, e := r.issueOccurrence(ctx, caller, d.Opening, proof, ad, coord, nil)
			if e != nil {
				return FileInfoWindowResult{}, e
			}
			row := FileInfoSource{Occurrence: occ, ItemID: public, ID: token, Container: frame.Container, VideoCodec: frame.VideoCodec, AudioCodec: frame.AudioCodec, Width: int(frame.Width), Height: int(frame.Height)}
			row.Duration, row.DurationState = fileInfoDuration(frame.Duration)
			page.Sources = append(page.Sources, row)
			coords = append(coords, coord)
		}
		e = r.windowEdges(caller, d, id, &page.Window, coords, nil, summary.Earlier, summary.Later || len(page.Sources) < len(summary.Occurrences))
		return FileInfoWindowResult{Sources: &page}, e
	case fileInfoLaneAudio, fileInfoLaneSubtitles:
		occ, e := r.issueOccurrence(ctx, caller, d.Opening, proof, ad, d.Target, nil)
		if e != nil {
			return FileInfoWindowResult{}, e
		}
		rawPage := FileInfoTracksPage{FileInfoWindowBasis: basis, Kind: "file-info-tracks", Occurrence: occ, Total: summary.Total, Lane: "audio", Tracks: []FileInfoRawTrack{}}
		headPage := FileInfoHeadTracksPage{FileInfoWindowBasis: basis, Kind: "file-info-head-tracks", Occurrence: occ, Total: summary.Total, Lane: "audio", Tracks: []FileInfoHeadTrack{}}
		if d.Lane == fileInfoLaneSubtitles {
			rawPage.Lane = "subtitles"
			headPage.Lane = "subtitles"
		}
		var size, modified int64
		if d.Group.Type != 0 {
			headPage.GroupID, e = r.issueOccurrence(ctx, caller, d.Opening, proof, ad, d.Target, &d.Group)
			if e != nil {
				return FileInfoWindowResult{}, e
			}
			size, modified, e = r.acceptedTrackStat(ctx, d.Target.Asset)
			if e != nil {
				return FileInfoWindowResult{}, e
			}
		}
		for i, coordinate := range summary.Occurrences {
			rowid := coordinate.Item
			var bytes int64
			if d.Group.Type != 0 && d.Lane == fileInfoLaneSubtitles {
				bytes, e = fileInfoHeadSubtitleTextBytes(ctx, dbwork.ReadHandle(ctx, r.db), rowid, d.Target.Item, d.Target.Asset, size, modified, ad.SubtitleActor)
			} else {
				bytes, e = r.trackRowBytes(ctx, d.Target.Asset, rowid, d.Lane)
			}
			if e != nil {
				return FileInfoWindowResult{}, e
			}
			keep, e := budget.row(bytes)
			if e != nil {
				return FileInfoWindowResult{}, e
			}
			if !keep {
				break
			}
			ordinal := summary.FirstOrdinal + int64(i)
			if d.Group.Type != 0 && d.Lane == fileInfoLaneSubtitles {
				row, e := fileInfoHeadSubtitleRow(ctx, dbwork.ReadHandle(ctx, r.db), rowid, d.Target.Item, d.Target.Asset, size, modified, ad.SubtitleActor)
				if e != nil {
					return FileInfoWindowResult{}, e
				}
				row.Ordinal = ordinal
				headPage.Tracks = append(headPage.Tracks, row)
			} else {
				row, e := r.hydrateTrack(ctx, d.Target.Asset, rowid, ordinal, d.Lane)
				if e != nil {
					return FileInfoWindowResult{}, e
				}
				if d.Group.Type != 0 {
					var index int64
					e = dbwork.ReadHandle(ctx, r.db).QueryRowContext(ctx, `SELECT stream_index FROM asset_streams WHERE rowid=? AND asset_id=(SELECT token FROM catalog_assets WHERE id=?)`, rowid, d.Target.Asset).Scan(&index)
					if e != nil {
						return FileInfoWindowResult{}, e
					}
					headPage.Tracks = append(headPage.Tracks, FileInfoHeadTrack{FileInfoRawTrack: row, ID: "a" + strconv.FormatInt(index, 10)})
				} else {
					rawPage.Tracks = append(rawPage.Tracks, row)
				}
			}
			coords = append(coords, coordinate)
		}
		later := summary.Later || len(coords) < len(summary.Occurrences)
		if d.Group.Type != 0 {
			e = r.windowEdges(caller, d, id, &headPage.Window, coords, nil, summary.Earlier, later)
			return FileInfoWindowResult{HeadTracks: &headPage}, e
		}
		e = r.windowEdges(caller, d, id, &rawPage.Window, coords, nil, summary.Earlier, later)
		return FileInfoWindowResult{Tracks: &rawPage}, e
	}
	return FileInfoWindowResult{}, readprep.ErrInvalid
}

// Only the transient ready response formats the fixed-width exact sum. Reducer
// and cache retain two integer words, never a decimal string or bigint graph.
func fileInfoGroupInteger(low, high int64) (*int64, string) {
	if high == 0 {
		return fileInfoInt(low)
	}
	var n, lo big.Int
	n.SetInt64(high)
	n.Lsh(&n, 64)
	lo.SetInt64(low)
	n.Add(&n, &lo)
	return nil, n.String()
}
