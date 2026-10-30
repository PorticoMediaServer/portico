package recordingmedia

import (
	"context"
	"os"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/artifactlease"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels/dvr"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func bindingFromReceipt(r dvr.RecordingLayoutReceipt) layoutBinding {
	return layoutBinding{OwnerKey: r.OwnerKey, RecordingID: r.RecordingID, ArtifactKey: r.ArtifactKey, RootIdentity: r.RootIdentity, SourceIdentity: r.SourceIdentity, TargetIdentity: r.TargetIdentity, Source: r.Source, Target: r.Target, Object: r.Object}
}

// PrepareRecordingLayout acquires source/root identity without touching the
// target. Only the existing retired, sealed occurrence output can be proposed.
func (d *Driver) PrepareRecordingLayout(ctx context.Context, in dvr.CaptureRequest, result dvr.CaptureResult, target string, revision int64) (dvr.RecordingLayoutReceipt, error) {
	var out dvr.RecordingLayoutReceipt
	if !in.Owner.Valid() || !result.WriterRetired || result.Object.Size <= 0 || !recordingID.MatchString(in.Recording.ID) || !recordingID.MatchString(result.Object.Digest) {
		return out, mediaartifact.ErrIdentity
	}
	if err := d.checkRoot(); err != nil {
		return out, err
	}
	if result.Root != filepath.Join(d.root, in.Owner.Key()) || result.Path != filepath.Join(result.Root, "captures", in.Recording.ID, "objects", result.Object.Digest) {
		return out, mediaartifact.ErrIdentity
	}
	root, err := openLayoutDirectory(d.root)
	if err != nil {
		return out, err
	}
	defer root.Close()
	info, err := root.file.Stat()
	if err != nil {
		return out, err
	}
	if !os.SameFile(info, d.rootIdentity) {
		return out, mediaartifact.ErrIdentity
	}
	rootID, err := storage.FileObjectIdentity(root.file)
	if err != nil {
		return out, err
	}
	source := in.Owner.Key() + "/captures/" + in.Recording.ID + "/objects/" + result.Object.Digest
	parent, name, err := root.parent(source, false)
	if err != nil {
		return out, err
	}
	defer parent.Close()
	file, err := parent.open(name)
	if err != nil {
		return out, err
	}
	defer file.Close()
	if err = artifactlease.Shared(file); err != nil {
		return out, mediaartifact.ErrLeased
	}
	sourceID, err := storage.FileObjectIdentity(file)
	if err != nil {
		return out, err
	}
	if err = verifyLayoutObject(ctx, file, result.Object); err != nil {
		return out, err
	}
	out = dvr.RecordingLayoutReceipt{OwnerKey: in.Owner.Key(), RecordingID: in.Recording.ID, ArtifactKey: in.ArtifactKey, LayoutRevision: revision, RootPath: d.root, RootIdentity: rootID, SourceIdentity: sourceID, Source: source, Target: target, BaseTarget: target, Collision: 1, Object: result.Object, Phase: "prepared"}
	if !out.Valid() {
		return dvr.RecordingLayoutReceipt{}, dvr.ErrInvalid
	}
	return out, nil
}
func (d *Driver) LinkRecordingLayout(ctx context.Context, receipt dvr.RecordingLayoutReceipt, recovering bool) (dvr.RecordingLayoutReceipt, error) {
	if !receipt.Valid() || receipt.RootPath != d.root || receipt.Phase == "retired" {
		return receipt, mediaartifact.ErrIdentity
	}
	if err := d.checkRoot(); err != nil {
		return receipt, err
	}
	binding := bindingFromReceipt(receipt)
	binding.allowExisting = recovering
	linked, err := linkLayout(ctx, d.root, binding)
	if err != nil {
		return receipt, err
	}
	receipt.TargetIdentity, receipt.Phase = linked.TargetIdentity, "linked"
	return receipt, nil
}
func (d *Driver) RemoveRecordingLayout(ctx context.Context, receipt dvr.RecordingLayoutReceipt) error {
	if !receipt.Valid() || receipt.RootPath != d.root {
		return mediaartifact.ErrIdentity
	}
	if receipt.Phase == "retired" {
		return ctx.Err()
	}
	if err := d.checkRoot(); err != nil {
		return err
	}
	binding := bindingFromReceipt(receipt)
	// A prepared DB reservation survives link-before-receipt crashes. Only that
	// exact source inode can be retired; an unowned collision is never removed.
	if binding.TargetIdentity == "" {
		binding.TargetIdentity = binding.SourceIdentity
	}
	return removeLayout(ctx, d.root, binding)
}

var _ dvr.RecordingLayoutDriver = (*Driver)(nil)
