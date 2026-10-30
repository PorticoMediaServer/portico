package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// DiscTitleRef is a checked title selection within an existing physical
// version. Revision binds the offered title's measured facts as well as source.
type DiscTitleRef struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}

// PinnedDiscTitleRefTx projects an already checked physical title pin. It never
// chooses the current main title or substitutes another set of timed facts.
func PinnedDiscTitleRefTx(ctx context.Context, tx *sql.Tx, asset, key, factsRevision string) (*DiscTitleRef, error) {
	d, err := ReadDiscSelection(ctx, tx, asset)
	if err != nil {
		return nil, err
	}
	if d == nil || key == "" || factsRevision == "" {
		return nil, ErrDiscSelectionChanged
	}
	var token string
	err = tx.QueryRowContext(ctx, `SELECT t.selection_token FROM inventory_disc_titles t JOIN inventory_disc_title_media m ON m.object_id=t.object_id AND m.title_key=t.title_key WHERE t.object_id=? AND t.title_key=? AND m.source_revision=? AND m.fingerprint=? AND m.revision=?`, d.Object, key, d.Revision, d.Fingerprint, factsRevision).Scan(&token)
	if err != nil {
		return nil, err
	}
	return &DiscTitleRef{ID: discPublicID(token), Revision: discTitleRevision(d.PublicRevision(), token, factsRevision)}, nil
}

func discTitleRevision(source, token, facts string) string {
	sum := sha256.Sum256([]byte("disc_title_offer_v1\x00" + source + "\x00" + token + "\x00" + facts))
	return hex.EncodeToString(sum[:])
}

func ReadDiscTitleMedia(ctx context.Context, q discQueryer, d *DiscSelection, key string) (assets.Facts, string, error) {
	var f assets.Facts
	var revision string
	var ms int64
	err := q.QueryRowContext(ctx, `SELECT revision,container,video_codec,audio_codec,width,height,duration_ms,origin_us,timing_known FROM inventory_disc_title_media WHERE object_id=? AND title_key=? AND source_revision=? AND fingerprint=?`, d.Object, key, d.Revision, d.Fingerprint).Scan(&revision, &f.Container, &f.VideoCodec, &f.AudioCodec, &f.Width, &f.Height, &ms, &f.OriginUS, &f.TimingKnown)
	if err != nil {
		return f, "", err
	}
	f.Duration = float64(ms) / 1000
	rows, err := q.QueryContext(ctx, `SELECT stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,detail_json,breadth_json FROM inventory_disc_title_media_streams WHERE object_id=? AND title_key=? ORDER BY stream_index LIMIT 129`, d.Object, key)
	if err != nil {
		return f, "", err
	}
	for rows.Next() {
		var st assets.Stream
		var detail, breadth string
		if err = rows.Scan(&st.Index, &st.Type, &st.Codec, &st.Language, &st.Title, &st.Channels, &st.ChannelLayout, &st.Default, &st.Forced, &st.ColorTransfer, &st.ColorPrimaries, &detail, &breadth); err != nil {
			break
		}
		st.DecodeDetail(detail)
		if len(breadth) > 8192 || json.Unmarshal([]byte(breadth), &st.Detail.StreamBreadth) != nil {
			err = disc.ErrProtocol
			break
		}
		f.Streams = append(f.Streams, st)
		if len(f.Streams) > 128 {
			err = disc.ErrProtocol
			break
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil || len(f.Streams) == 0 {
		return f, "", disc.ErrProtocol
	}
	f.DiscSelection = &assets.DiscMediaSelection{TitleKey: key, StructureFingerprint: d.Fingerprint, Members: []storage.DiscMember{}}
	rows, err = q.QueryContext(ctx, `SELECT path,object_identity,size,modified_ns,required,directory FROM inventory_disc_title_media_members WHERE object_id=? AND title_key=? ORDER BY path LIMIT ?`, d.Object, key, disc.MaxConsumedMembers+1)
	if err != nil {
		return f, "", err
	}
	defer rows.Close()
	for rows.Next() {
		var m storage.DiscMember
		if err = rows.Scan(&m.Path, &m.ObjectIdentity, &m.Size, &m.ModifiedNS, &m.Required, &m.Directory); err != nil {
			return f, "", err
		}
		f.DiscSelection.Members = append(f.DiscSelection.Members, m)
		if len(f.DiscSelection.Members) > disc.MaxConsumedMembers {
			return f, "", disc.ErrProtocol
		}
	}
	return f, revision, rows.Err()
}

// ResolveDiscTitleTx binds a public title handle to current cached timed facts.
// The caller has already authorized this item and selected its physical asset.
func ResolveDiscTitleTx(ctx context.Context, tx *sql.Tx, d *DiscSelection, ref DiscTitleRef) (disc.Title, assets.Facts, string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT selection_token,title_key FROM inventory_disc_titles WHERE object_id=? LIMIT ?`, d.Object, disc.MaxTitles+1)
	if err != nil {
		return disc.Title{}, assets.Facts{}, "", err
	}
	var key, token string
	for rows.Next() {
		var t, k string
		if err = rows.Scan(&t, &k); err != nil {
			break
		}
		if discPublicID(t) == ref.ID {
			key, token = k, t
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return disc.Title{}, assets.Facts{}, "", err
	}
	if key == "" {
		return disc.Title{}, assets.Facts{}, "", sql.ErrNoRows
	}
	f, revision, err := ReadDiscTitleMedia(ctx, tx, d, key)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrDiscPreparing
	}
	if err != nil {
		return disc.Title{}, f, "", err
	}
	if ref.Revision != discTitleRevision(d.PublicRevision(), token, revision) {
		return disc.Title{}, f, "", ErrDiscSelectionChanged
	}
	for _, t := range d.Structure.Titles {
		if t.Key(d.Structure.Kind) == key {
			return t, f, revision, nil
		}
	}
	return disc.Title{}, f, "", disc.ErrProtocol
}
