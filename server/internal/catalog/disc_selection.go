package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/disc"
)

var ErrDiscSelectionChanged = errors.New("disc selection changed; reload titles")
var ErrDiscPreparing = errors.New("disc main title is being analyzed")

// DiscTitles contains only public physical-version identity and display facts.
// Selection IDs are one-way opaque handles, never playlist names or private keys.
type DiscTitles struct {
	VersionID string            `json:"versionId"`
	Revision  string            `json:"revision"`
	Kind      FileInfoDiscKind  `json:"kind"`
	Packaging DiscPackaging     `json:"packaging"`
	Ready     bool              `json:"ready"`
	Titles    []DiscTitleOption `json:"titles"`
}
type DiscTitleOption struct {
	Ready        bool   `json:"ready"`
	Revision     string `json:"revision,omitempty"`
	ID           string `json:"id"`
	Number       int    `json:"number"`
	DurationMs   int64  `json:"durationMs"`
	ChapterCount int    `json:"chapterCount"`
	Main         bool   `json:"main"`
}
type DiscMainTitleRequest struct {
	VersionID string `json:"versionId"`
	Revision  string `json:"revision"`
	TitleID   string `json:"titleId"`
}

// DiscSelection is server-private. Its read is not an access grant; the caller
// must authorize the item/library in the same transaction before using it.
type DiscSelection struct {
	Object, Asset, Source, Revision, Fingerprint, OwnerKey, MediaKey  string
	SourceGeneration, ChoiceRevision, StreamRevision, ChapterRevision int64
	Root, RootIdentity, Incarnation, Relative, ObjectIdentity         string
	FollowSymlinks                                                    bool
	Size, ModifiedNS                                                  int64
	Structure                                                         disc.Probe
}

func discPublicID(token string) string {
	sum := sha256.Sum256([]byte("disc_title_public_v1\x00" + token))
	return hex.EncodeToString(sum[:])
}
func (d DiscSelection) PublicRevision() string {
	raw, _ := json.Marshal([]any{d.Object, d.Asset, d.SourceGeneration, d.Incarnation, d.Revision, d.Fingerprint, d.OwnerKey, d.ChoiceRevision, d.MediaKey, d.StreamRevision, d.ChapterRevision})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (d DiscSelection) MainTitle() (disc.Title, bool) {
	if d.OwnerKey == "" {
		return d.Structure.MainTitle()
	}
	for _, t := range d.Structure.Titles {
		if t.Key(d.Structure.Kind) == d.OwnerKey {
			return t, true
		}
	}
	return disc.Title{}, false
}
func ReadDiscSelection(ctx context.Context, q discQueryer, asset string) (*DiscSelection, error) {
	rows, err := q.QueryContext(ctx, `SELECT d.object_id,d.asset_id,o.source_id,d.source_revision,d.fingerprint,d.owner_title_key,d.media_title_key,
 d.source_generation,d.choice_revision,d.media_stream_revision,d.media_chapter_revision,s.root,s.root_identity,s.incarnation,o.relative_path,o.object_identity,s.follow_symlinks,o.size,o.modified_ns
 FROM inventory_disc_facts d JOIN inventory_objects o ON o.id=d.object_id AND o.asset_id=d.asset_id
 JOIN library_sources s ON s.id=o.source_id JOIN catalog_assets a ON a.token=d.asset_id
 WHERE d.asset_id=? AND a.available=1 AND a.size=o.size AND a.modified_ns=o.modified_ns AND o.retired=0 AND o.state='available' AND s.enabled=1 AND s.kind='local' AND d.encryption='none'
 AND o.revision=d.source_revision AND d.source_generation=s.generation AND d.root_incarnation=s.incarnation AND o.root_incarnation=s.incarnation LIMIT 2`, asset)
	if err != nil {
		return nil, err
	}
	var out *DiscSelection
	for rows.Next() {
		d := new(DiscSelection)
		if err = rows.Scan(&d.Object, &d.Asset, &d.Source, &d.Revision, &d.Fingerprint, &d.OwnerKey, &d.MediaKey, &d.SourceGeneration, &d.ChoiceRevision, &d.StreamRevision, &d.ChapterRevision, &d.Root, &d.RootIdentity, &d.Incarnation, &d.Relative, &d.ObjectIdentity, &d.FollowSymlinks, &d.Size, &d.ModifiedNS); err != nil {
			rows.Close()
			return nil, err
		}
		if out != nil {
			rows.Close()
			return nil, ErrDiscSelectionChanged
		}
		out = d
	}
	err = rows.Err()
	rows.Close()
	if err != nil || out == nil {
		return out, err
	}
	out.Structure, err = ReadInventoryDiscProbe(ctx, q, out.Object, out.Revision)
	return out, err
}
func (d DiscSelection) Ready(ctx context.Context, q discQueryer) error {
	t, ok := d.MainTitle()
	if !ok || d.MediaKey != t.Key(d.Structure.Kind) {
		return ErrDiscPreparing
	}
	var ready bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_assets a JOIN asset_stream_facts st ON st.asset_id=a.token AND st.size=a.size AND st.modified_ns=a.modified_ns JOIN asset_chapter_facts ch ON ch.asset_id=a.token AND ch.size=a.size AND ch.modified_ns=a.modified_ns WHERE a.token=? AND st.revision=? AND ch.revision=? AND ch.status='known')`, d.Asset, d.StreamRevision, d.ChapterRevision).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return ErrDiscPreparing
	}
	return nil
}
func DiscTitlesTx(ctx context.Context, tx *sql.Tx, item, asset string) (DiscTitles, *DiscSelection, error) {
	var linked bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entities e JOIN catalog_asset_links l ON l.entity_id=e.id JOIN catalog_assets a ON a.id=l.asset_id WHERE e.public_id=pid_blob(?) AND e.retired=0 AND a.token=? AND a.available=1)`, item, asset).Scan(&linked)
	if err != nil {
		return DiscTitles{}, nil, err
	}
	if !linked {
		return DiscTitles{}, nil, sql.ErrNoRows
	}
	d, err := ReadDiscSelection(ctx, tx, asset)
	if err != nil {
		return DiscTitles{}, nil, err
	}
	if d == nil {
		return DiscTitles{}, nil, sql.ErrNoRows
	}
	main, ok := d.MainTitle()
	if !ok {
		return DiscTitles{}, nil, disc.ErrProtocol
	}
	readyErr := d.Ready(ctx, tx)
	if readyErr != nil && !errors.Is(readyErr, ErrDiscPreparing) {
		return DiscTitles{}, nil, readyErr
	}
	out := DiscTitles{VersionID: asset, Revision: d.PublicRevision(), Kind: FileInfoDiscKind(d.Structure.Kind), Packaging: DiscPackaging(d.Structure.Packaging), Ready: readyErr == nil, Titles: []DiscTitleOption{}}
	rows, err := tx.QueryContext(ctx, `SELECT t.selection_token,t.title_key,t.title_number,t.duration_ms,(SELECT COUNT(*) FROM inventory_disc_chapters c WHERE c.object_id=t.object_id AND c.title_key=t.title_key),COALESCE(m.revision,'') FROM inventory_disc_titles t LEFT JOIN inventory_disc_title_media m ON m.object_id=t.object_id AND m.title_key=t.title_key AND m.source_revision=? AND m.fingerprint=? WHERE t.object_id=? ORDER BY t.title_number LIMIT ?`, d.Revision, d.Fingerprint, d.Object, disc.MaxTitles+1)
	if err != nil {
		return DiscTitles{}, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var token, key, mediaRevision string
		var t DiscTitleOption
		if err = rows.Scan(&token, &key, &t.Number, &t.DurationMs, &t.ChapterCount, &mediaRevision); err != nil {
			return DiscTitles{}, nil, err
		}
		t.ID = discPublicID(token)
		t.Ready = mediaRevision != ""
		if t.Ready {
			t.Revision = discTitleRevision(d.PublicRevision(), token, mediaRevision)
		}
		t.Main = key == main.Key(d.Structure.Kind)
		out.Titles = append(out.Titles, t)
		if len(out.Titles) > disc.MaxTitles {
			return DiscTitles{}, nil, disc.ErrProtocol
		}
	}
	return out, d, rows.Err()
}

// ChooseDiscTitleTx only records intent when the caller also atomically queues
// real reanalysis. Playback remains closed until that title's facts are accepted.
func ChooseDiscTitleTx(ctx context.Context, tx *sql.Tx, item string, in DiscMainTitleRequest) (DiscTitles, *DiscSelection, error) {
	out, d, err := DiscTitlesTx(ctx, tx, item, in.VersionID)
	if err != nil {
		return out, d, err
	}
	if in.Revision != out.Revision {
		return out, d, ErrDiscSelectionChanged
	}
	var key string
	rows, err := tx.QueryContext(ctx, `SELECT selection_token,title_key FROM inventory_disc_titles WHERE object_id=? LIMIT ?`, d.Object, disc.MaxTitles+1)
	if err != nil {
		return out, d, err
	}
	for rows.Next() {
		var token, k string
		if err = rows.Scan(&token, &k); err != nil {
			rows.Close()
			return out, d, err
		}
		if discPublicID(token) == in.TitleID {
			key = k
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, d, err
	}
	if key == "" {
		return out, d, sql.ErrNoRows
	}
	if _, err = tx.ExecContext(ctx, `UPDATE inventory_disc_facts SET owner_title_key=?,choice_revision=choice_revision+1 WHERE object_id=? AND choice_revision=?`, key, d.Object, d.ChoiceRevision); err != nil {
		return out, d, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE inventory_objects SET analysis_revision='',analysis_policy=0 WHERE id=? AND revision=?`, d.Object, d.Revision); err != nil {
		return out, d, err
	}
	return DiscTitlesTx(ctx, tx, item, in.VersionID)
}

// InventoryDiscMainTitle honors only an exact current observation. A changed
// structure cannot transfer the old owner's choice to a different title.
func (s *Service) InventoryDiscMainTitle(ctx context.Context, object, revision string, p disc.Probe) (disc.Title, bool, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return disc.Title{}, false, err
	}
	sum := sha256.Sum256(append([]byte("disc_structure_v1\x00"), raw...))
	var key string
	err = s.read().QueryRowContext(ctx, `SELECT owner_title_key FROM inventory_disc_facts WHERE object_id=? AND source_revision=? AND fingerprint=?`, object, revision, hex.EncodeToString(sum[:])).Scan(&key)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return disc.Title{}, false, err
	}
	if key != "" {
		for _, t := range p.Titles {
			if t.Key(p.Kind) == key {
				return t, true, nil
			}
		}
		return disc.Title{}, false, ErrDiscSelectionChanged
	}
	t, ok := p.MainTitle()
	return t, ok, nil
}

// RequestDiscTitleTx captures one checked alternate probe under the real job.
func RequestDiscTitleTx(ctx context.Context, tx *sql.Tx, item, job string, in DiscMainTitleRequest) error {
	out, d, err := DiscTitlesTx(ctx, tx, item, in.VersionID)
	if err != nil {
		return err
	}
	if out.Revision != in.Revision {
		return ErrDiscSelectionChanged
	}
	rows, err := tx.QueryContext(ctx, `SELECT selection_token,title_key FROM inventory_disc_titles WHERE object_id=? LIMIT ?`, d.Object, disc.MaxTitles+1)
	if err != nil {
		return err
	}
	var key string
	for rows.Next() {
		var token, k string
		if err = rows.Scan(&token, &k); err != nil {
			break
		}
		if discPublicID(token) == in.TitleID {
			key = k
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	if key == "" {
		return sql.ErrNoRows
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO inventory_disc_title_requests(job_id,object_id,title_key,source_revision,fingerprint) VALUES(?,?,?,?,?)`, job, d.Object, key, d.Revision, d.Fingerprint)
	return err
}
