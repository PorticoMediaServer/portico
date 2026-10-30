package assets

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// streamInputFingerprint is shared by direct canonical fixtures and staged
// production acceptance. Both validate and normalize precisely the same facts.
func streamInputFingerprint(streams []Stream) (string, error) {
	if len(streams) > 128 {
		return "", errors.New("stream facts exceed capacity")
	}
	seen := map[int]bool{}
	for _, s := range streams {
		if s.Index < 0 || seen[s.Index] || (s.Type != "video" && s.Type != "audio" && s.Type != "subtitle") {
			return "", errors.New("invalid stream facts")
		}
		seen[s.Index] = true
	}
	raw, _ := json.Marshal(streams)
	for _, s := range streams {
		raw = append(raw, s.EncodeDetail()...)
		breadth, _ := json.Marshal(s.Detail.StreamBreadth)
		raw = append(raw, breadth...)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// BuildInfoStreams creates replayable typed row accessors over the caller's
// immutable probe. It writes no canonical facts and copies no complete row set.
// A nil result means the exact latest source frame is already canonical.
func BuildInfoStreams(ctx context.Context, tx *sql.Tx, asset int64, token string, size, modified int64, streams []Stream) ([]compactcatalog.InfoSourceFamily, error) {
	if streams == nil {
		return nil, nil
	}
	fingerprint, err := streamInputFingerprint(streams)
	if err != nil {
		return nil, err
	}
	revision, changed, err := compactcatalog.NextInfoFactRevisionTx(ctx, tx, asset, token, 1, fingerprint, size, modified)
	if err != nil || !changed {
		return nil, err
	}
	boolean := func(v bool) int64 {
		if v {
			return 1
		}
		return 0
	}
	return []compactcatalog.InfoSourceFamily{
		{Number: 1, Count: 1, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			return compactcatalog.InfoStreamFactRow{InputOrdinal: i, AssetID: token, Revision: revision, Size: size, ModifiedNS: modified, Fingerprint: fingerprint, DetailVersion: StreamDetailVersion}, nil
		}},
		{Number: 2, Count: int64(len(streams)), Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			s := streams[i]
			return compactcatalog.InfoStreamRow{InputOrdinal: i, AssetID: token, StreamIndex: int64(s.Index), Type: s.Type, Codec: s.Codec, Language: s.Language, Title: s.Title, Channels: int64(s.Channels), ChannelLayout: s.ChannelLayout, IsDefault: boolean(s.Default), IsForced: boolean(s.Forced), ColorTransfer: streamText(s.ColorTransfer, 32), ColorPrimaries: streamText(s.ColorPrimaries, 32), DetailJSON: s.EncodeDetail()}, nil
		}},
		{Number: 3, Count: int64(len(streams)), Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			s := streams[i]
			b := s.Detail.StreamBreadth
			return compactcatalog.InfoStreamBreadthRow{InputOrdinal: i, AssetID: token, StreamIndex: int64(s.Index), AudioDescription: boolean(b.AudioDescription), AudioVariant: b.AudioVariant, ChromaSubsampling: b.ChromaSubsampling, FPSNumerator: int64(b.FrameRateNumerator), FPSDenominator: int64(b.FrameRateDenominator), FrameRateMode: b.FrameRateMode, InterlaceAnalysis: b.InterlaceAnalysis, Telecine: boolean(b.Telecine), DOVIRPU: boolean(b.DolbyVisionRPU), DOVIBaseLayer: boolean(b.DolbyVisionBaseLayer), DOVIMetadataKnown: boolean(b.DolbyVisionMetadataKnown), DOVIEnhancementType: b.DolbyVisionEnhancementType, DOVICMVersion: b.DolbyVisionCMVersion}, nil
		}},
	}, nil
}

func chapterInput(f Facts) (string, []Chapter, string, error) {
	status := f.ChapterStatus
	if status == "" {
		return "", nil, "", nil
	}
	if status != "known" && status != "invalid" && status != "partial" {
		return "", nil, "", errors.New("invalid chapter status")
	}
	if status == "partial" {
		status = "known"
	}
	chapters := f.Chapters
	if status == "invalid" {
		chapters = nil
	}
	if len(chapters) > 4096 {
		return "", nil, "", errors.New("chapter count exceeds capacity")
	}
	last := 0.0
	for i, c := range chapters {
		if math.IsNaN(c.Start) || math.IsNaN(c.End) || math.IsInf(c.Start, 0) || math.IsInf(c.End, 0) || c.Start < 0 || c.End <= c.Start || c.End > f.Duration || i > 0 && c.Start < last {
			return "", nil, "", errors.New("invalid chapter timeline")
		}
		last = c.End
	}
	raw, _ := json.Marshal([]any{status, chapters})
	sum := sha256.Sum256(raw)
	return status, chapters, hex.EncodeToString(sum[:]), nil
}

func BuildInfoChapters(ctx context.Context, tx *sql.Tx, asset int64, token string, size, modified int64, f Facts) ([]compactcatalog.InfoSourceFamily, error) {
	status, chapters, fingerprint, err := chapterInput(f)
	if err != nil || status == "" {
		return nil, err
	}
	revision, changed, err := compactcatalog.NextInfoFactRevisionTx(ctx, tx, asset, token, 4, fingerprint, size, modified)
	if err != nil || !changed {
		return nil, err
	}
	return []compactcatalog.InfoSourceFamily{
		{Number: 4, Count: 1, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			return compactcatalog.InfoChapterFactRow{InputOrdinal: i, AssetID: token, Revision: revision, Size: size, ModifiedNS: modified, Status: status, Fingerprint: fingerprint}, nil
		}},
		{Number: 5, Count: int64(len(chapters)), Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			c := chapters[i]
			return compactcatalog.InfoChapterRow{InputOrdinal: i, AssetID: token, ChapterIndex: i + 1, Title: streamText(c.Title, 500), StartSeconds: c.Start, EndSeconds: c.End}, nil
		}},
	}, nil
}
