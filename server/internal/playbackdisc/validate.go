package playbackdisc

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
)

func text(s string, max int, empty bool) bool {
	if (!empty && s == "") || len(s) > max || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 32 || (r >= 127 && r <= 159) {
			return false
		}
	}
	return true
}
func digest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
func validKey(k Key) bool   { return text(k.SessionID, 256, false) && k.Generation > 0 }
func (s Snapshot) key() Key { return Key{s.SessionID, s.Generation} }
func validActor(a Actor) bool {
	for _, v := range []string{a.ServerID, a.Authority, a.AccountID, a.ProfileID, a.DeviceID, a.FamilyID, a.SessionHash} {
		if !text(v, 256, false) {
			return false
		}
	}
	return text(a.Grant, 256, true) && a.Epoch >= 0 && a.TokenGeneration > 0 && a.OriginalHorizonMs > 0
}
func validOperation(o Operation) bool {
	if !text(o.LegacyRequestID, 256, false) || !digest(o.StartKeyDigest) || !digest(o.StartBodyDigest) {
		return false
	}
	if o.QueueID == "" {
		return o.QueueEntryID == "" && o.QueueOwnerKind == "" && o.QueueOwnerID == "" && o.QueueRevision == 0 && o.QueueCustodyGeneration == 0
	}
	return text(o.QueueID, 256, false) && text(o.QueueEntryID, 256, false) && text(o.QueueOwnerKind, 80, false) && text(o.QueueOwnerID, 256, false) && o.QueueRevision > 0 && o.QueueCustodyGeneration >= 0
}
func validSource(s Source) bool {
	for _, v := range []string{s.LibraryID, s.LibrarySourceID, s.AssetID, s.InventoryObjectID, s.InventoryRevision, s.Incarnation, s.RemoteSourceID, s.EffectiveConfiguration, s.ReadPolicyWitness, s.ReadAuthorityWitness} {
		if !text(v, 256, false) {
			return false
		}
	}
	return text(s.RootIdentity, 4096, false) && s.LibraryGeneration > 0 && s.RawConfigurationGeneration > 0 && s.ProfileRevision >= 0 && s.Size > 0 && s.ModifiedNS >= 0
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func validateStream(v assets.Stream) bool {
	if v.Index < 0 || v.Index > 65535 || (v.Type != "video" && v.Type != "audio" && v.Type != "subtitle") || !text(v.Codec, 128, true) || !text(v.Language, 64, true) || !text(v.Title, 4096, true) || v.Channels < 0 || v.Channels > 256 || !text(v.ChannelLayout, 256, true) {
		return false
	}
	raw, err := json.Marshal(v.Detail)
	if err != nil || len(raw) > 8192 || !finite(v.Detail.FrameRate) || v.Detail.FrameRate < 0 || v.Detail.FrameRate > 1000 {
		return false
	}
	for _, str := range []string{v.ColorTransfer, v.ColorPrimaries, v.Detail.Profile, v.Detail.PixelFormat, v.Detail.SampleAspectRatio, v.Detail.CodecTag, v.Detail.DynamicRange, v.Detail.MasterDisplay, v.Detail.MaxCLL, v.Detail.ObjectAudio, v.Detail.AudioVariant, v.Detail.ChromaSubsampling, v.Detail.FrameRateMode, v.Detail.InterlaceAnalysis, v.Detail.DolbyVisionEnhancementType, v.Detail.DolbyVisionCMVersion} {
		if !text(str, 1024, true) {
			return false
		}
	}
	return true
}

// freeze copies every slice/pointer before any caller callback or SQL. The
// canonical bounded disc decoder validates structure; its JSON is not stored.
func freeze(in Snapshot) (Snapshot, error) {
	if !text(in.ID, 256, false) || !validKey(in.key()) || !validActor(in.Actor) || !validOperation(in.Operation) || !validSource(in.Source) {
		return Snapshot{}, ErrInput
	}
	if (in.Input.Conditional == nil) == (in.Input.Captured == nil) {
		return Snapshot{}, ErrInput
	}
	if c := in.Input.Conditional; c != nil {
		e := c.Version.Evidence()
		v, err := mediasource.NewVersion(e)
		if err != nil || v.ID() != c.Version.ID() || (e.Kind != mediasource.StrongETag && e.Kind != mediasource.ImmutableProviderVersion) || e.Size != in.Source.Size {
			return Snapshot{}, ErrInput
		}
		in.Input.Conditional = &Conditional{Version: v}
	} else {
		c := *in.Input.Captured
		r := c.Receipt
		if !digest(r.ID) || !digest(r.ArtifactID) || !digest(r.Object.Digest) || r.Object.Size != in.Source.Size || r.Version.ID() == "" || r.Version.Evidence().Kind != mediasource.ContentSHA256 || r.ExpiresAt.IsZero() {
			return Snapshot{}, ErrInput
		}
		b := r.Binding
		if b.SourceID != in.Source.RemoteSourceID || b.ConfigGeneration != in.Source.EffectiveConfiguration || b.ObjectID != in.Source.InventoryObjectID || b.ObjectRevision != in.Source.InventoryRevision || b.AssetID != in.Source.AssetID || b.PolicyWitness != in.Source.ReadPolicyWitness || b.AuthorityWitness != in.Source.ReadAuthorityWitness {
			return Snapshot{}, ErrInput
		}
		in.Input.Captured = &c
	}
	for _, v := range []string{in.Disc.SelectedTitleKey, in.Disc.StructureRevision, in.Disc.StreamRevision, in.Disc.ChapterRevision, in.Disc.FactsRevision} {
		if !text(v, 256, false) {
			return Snapshot{}, ErrInput
		}
	}
	// Bound work before encoding an in-memory candidate as well as after the
	// canonical decoder's byte limit. No field is truncated or normalized.
	if len(in.Disc.Structure.Titles) == 0 || len(in.Disc.Structure.Titles) > disc.MaxTitles {
		return Snapshot{}, ErrInput
	}
	rows := 0
	for _, title := range in.Disc.Structure.Titles {
		if len(title.Clips) > 4096 || len(title.Streams) > 65536 || len(title.Chapters) > 65535 || !text(title.PlaylistFile, 64, true) {
			return Snapshot{}, ErrInput
		}
		rows += len(title.Clips) + len(title.Streams) + len(title.Chapters)
		if rows > 131072 {
			return Snapshot{}, ErrInput
		}
		for _, clip := range title.Clips {
			if !text(clip.File, 64, false) {
				return Snapshot{}, ErrInput
			}
		}
		for _, stream := range title.Streams {
			if !text(stream.Language, 64, true) || !text(stream.VideoRole, 64, true) {
				return Snapshot{}, ErrInput
			}
		}
	}
	raw, err := json.Marshal(in.Disc.Structure)
	if err != nil {
		return Snapshot{}, ErrInput
	}
	p, err := disc.DecodeProbe(raw)
	if err != nil || p.Packaging != "iso" || len(p.Titles) == 0 {
		return Snapshot{}, ErrInput
	}
	selected := false
	for _, t := range p.Titles {
		selected = selected || t.Key(p.Kind) == in.Disc.SelectedTitleKey
	}
	if !selected {
		return Snapshot{}, ErrInput
	}
	in.Disc.Structure = p
	m := in.Media
	if !text(m.Container, 128, false) || !text(m.VideoCodec, 128, true) || !text(m.AudioCodec, 128, true) || m.Width < 0 || m.Width > 32768 || m.Height < 0 || m.Height > 32768 || m.DurationMs <= 0 || m.DurationMs > 7*24*60*60*1000 || (m.OriginUS < -7*24*60*60*1000000 || m.OriginUS > 7*24*60*60*1000000) || len(m.Streams) == 0 || len(m.Streams) > 128 || len(m.Chapters) > 4096 {
		return Snapshot{}, ErrInput
	}
	seen := map[int]bool{}
	for _, v := range m.Streams {
		if seen[v.Index] || !validateStream(v) {
			return Snapshot{}, ErrInput
		}
		seen[v.Index] = true
	}
	last := float64(0)
	for i, v := range m.Chapters {
		if !text(v.Title, 4096, true) || !finite(v.Start) || !finite(v.End) || v.Start < 0 || v.End <= v.Start || v.End > float64(m.DurationMs)/1000 || i > 0 && v.Start < last {
			return Snapshot{}, ErrInput
		}
		last = v.End
	}
	in.Media.Streams = append([]assets.Stream{}, m.Streams...)
	if m.Chapters != nil {
		in.Media.Chapters = append([]assets.Chapter{}, m.Chapters...)
	}
	if !digest(in.Tools.DiscHelperDigest) || !digest(in.Tools.FFmpegDigest) || !digest(in.Tools.FFprobeDigest) || !text(in.Tools.RuntimeIdentity, 4096, false) {
		return Snapshot{}, ErrInput
	}
	return in, nil
}
