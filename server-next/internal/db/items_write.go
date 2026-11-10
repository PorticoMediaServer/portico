package db

import (
	"context"
	"database/sql"
	"errors"
	"path"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/sorttext"
)

// ImportBatch is what a scan found in some folders of one library path: the folders, the files
// in them with their probe facts, and the items those files make up. Importing the same batch
// again changes nothing, because every folder, file and item is found by its unique key first.
// A batch should hold a few hundred rows so a person's write never waits long behind it.
type ImportBatch struct {
	LibraryID int64
	PathID    int64
	Scan      int64
	Dirs      []ImportDir  // parents before children
	Files     []ImportFile // each in a folder of this batch or of an earlier one
	Items     []ImportItem // parents before children
	// What only a scan of real folders reports. Moves are applied before Files, Gone after.
	Moves []ImportMove // files that are the same file at a new place
	Gone  []FileRef    // files a complete listing of their folder no longer has
}

// ImportDir is one folder, by its path relative to the library path ("" is the path itself).
type ImportDir struct {
	RelPath    string
	ModifiedNS int64
}

// FileRef names a file by its folder and name.
type FileRef struct {
	Dir  string
	Name string
}

// ImportFile is one file with what probing it found.
type ImportFile struct {
	FileRef
	Role       FileRole
	Size       int64
	ModifiedNS int64
	Container  string
	DurationMS int64
	Bitrate    int64
	Streams    []Stream
	// Unprobed says the facts above are not known yet: the file is stored for the probe pass
	// to read. A file stored this way and found again unchanged is never read again.
	Unprobed bool
	Problem  FileProblem // why the file is in Files skipped, if it is
}

// Stream is one video, audio or subtitle track of a file.
type Stream struct {
	Kind            StreamKind
	Codec           string
	Profile         string
	Language        string
	Title           string
	Default         bool
	Forced          bool
	HearingImpaired bool
	Bitrate         int64
	Width           int
	Height          int
	FrameRate       float64
	BitDepth        int
	HDR             string
	Channels        int
	ChannelLayout   string
	SampleRate      int
}

// ItemKey is one fact that identifies an item across rescans.
type ItemKey struct {
	Kind KeyKind
	Key  string
}

// ImportItem is one item as the scan understands it. Keys must name it uniquely within the
// library; Parent is a key of the item that holds it, which is in this batch or already stored.
// Descriptive fields are written when the item is first created. After that they belong to
// metadata matching and to people's edits, and a rescan leaves them alone.
type ImportItem struct {
	Kind            ItemKind
	Keys            []ItemKey
	Parent          *ItemKey
	Title           string
	SortTitle       string // a sort title given by the files; empty means derive it from the title
	Language        string
	OriginalTitle   string
	Year            int
	ReleaseDate     string
	IndexNumber     *int
	ParentIndex     *int
	AddedAt         int64 // 0 means now
	ContentRating   string
	RatingAge       *int // nil means take the parent's
	CommunityRating float64
	DurationMS      int64 // 0 means take the first version's
	Summary         string
	ExternalIDs     []ExternalID
	Tags            []TagName
	Credits         []ImportCredit
	Images          []ImportImage
	Versions        []ImportVersion
	// ParentRow names the parent by its place in this batch's Items, counting from 1, for a
	// parent that has no library-wide key (a season). 0 means use Parent.
	ParentRow int
	// ChildKey is the item's place under its parent ("s2", "e5", "d1t4"). It is stored as a
	// name key with the parent's id in front, and is looked up after Keys.
	ChildKey string
	// Follow is a file of this container that moved here. When no key finds the container,
	// it is the one the file belonged to before: a renamed show folder is the same show.
	Follow *FileRef
	// ExtraOf makes this item an extra or trailer of the item one of these keys names.
	ExtraOf   []ItemKey
	ExtraKind RelationKind
}

// ExternalID is an id a provider gave an item or a person.
type ExternalID struct {
	Provider string
	ID       string
}

// TagName names a genre, studio, label or other tag.
type TagName struct {
	Kind string
	Name string
}

// ImportCredit is a person's part in an item. A person with a provider id is found by it;
// otherwise by name.
type ImportCredit struct {
	Name      string
	Person    ExternalID
	Role      CreditRole
	Character string
}

// ImportImage is a picture found beside or inside the files.
type ImportImage struct {
	Role     ImageRole
	Source   ImageSource
	FileHash string
	Width    int
	Height   int
}

// ImportVersion is one playable version of an item: the files or file ranges it is made of.
type ImportVersion struct {
	Name     string
	Edition  string
	Parts    []ImportPart
	Sidecars []FileRef // subtitle and similar files that belong to this version
	Markers  []Marker
	// Shared says the same file or range also plays other items (two episodes in one file),
	// so finding it on another item does not mean it moved from there.
	Shared bool
}

// ImportPart is a whole file, or the range of a file between StartMS and EndMS.
type ImportPart struct {
	File    FileRef
	StartMS *int64
	EndMS   *int64
}

// Marker is a chapter or a skippable span of a version.
type Marker struct {
	Kind    MarkerKind
	Source  MarkerSource
	StartMS int64
	EndMS   int64
	Title   string
}

// ImportStats says what one import did. RowsWritten counts every row inserted, updated or
// deleted, so an unchanged rescan reports zero.
type ImportStats struct {
	Files        int
	FilesNew     int
	FilesChanged int
	Items        int
	ItemsNew     int
	RowsWritten  int64
	Took         time.Duration // how long the transaction held the writer, before its commit
}

// Import writes one batch in one background transaction.
func (d *DB) Import(ctx context.Context, batch ImportBatch) (ImportStats, error) {
	var stats ImportStats
	err := d.write(ctx, Background, func(tx *sql.Tx) error {
		im := &importer{batchTx: newBatchTx(tx), batch: batch, now: time.Now().UnixMilli(),
			dirs: map[string]int64{}, files: map[FileRef]*importedFile{}, items: map[ItemKey]*importedItem{},
			tags: map[TagName]int64{}, people: map[string]int64{}, counts: map[int64]*countChange{}}
		defer im.close()
		started := time.Now()
		defer func() { stats.Took = time.Since(started) }()
		stats = ImportStats{Files: len(batch.Files), Items: len(batch.Items)}
		var before int64
		if err := tx.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
			return err
		}
		if err := im.run(&stats); err != nil {
			return err
		}
		if err := tx.QueryRow(`SELECT total_changes()`).Scan(&stats.RowsWritten); err != nil {
			return err
		}
		stats.RowsWritten -= before
		return nil
	})
	return stats, err
}

type importer struct {
	*batchTx
	batch  ImportBatch
	now    int64
	dirs   map[string]int64
	files  map[FileRef]*importedFile
	items  map[ItemKey]*importedItem
	tags   map[TagName]int64
	people map[string]int64
	counts map[int64]*countChange
	rows   []*importedItem // the stored item of each of the batch's Items, in order
	// Files whose missing mark or place changed, and items that lost a version: what is
	// available is worked out again for them at the end.
	changedFiles []int64
	unsettled    []int64
	restores     *bool // whether this library has state kept from removed titles
}

type importedFile struct {
	id      int64
	touched bool // new or changed in this batch
	moved   bool // the same file, found at a new place in this batch
	facts   *ImportFile
}

type importedItem struct {
	id        int64
	rootID    int64 // 0 for a top-level item
	parentID  int64
	ratingAge *int
}

type countChange struct {
	children, leaves int
	lastAdded        int64
}

func (im *importer) run(stats *ImportStats) error {
	for _, dir := range im.batch.Dirs {
		if err := im.importDir(dir); err != nil {
			return err
		}
	}
	for _, move := range im.batch.Moves {
		if err := im.moveFile(move); err != nil {
			return err
		}
	}
	for i := range im.batch.Files {
		if err := im.importFile(&im.batch.Files[i], stats); err != nil {
			return err
		}
	}
	for _, ref := range im.batch.Gone {
		if err := im.goneFile(ref); err != nil {
			return err
		}
	}
	for i := range im.batch.Items {
		if err := im.importItem(&im.batch.Items[i], stats); err != nil {
			return err
		}
	}
	for id, c := range im.counts {
		if _, err := im.exec(`UPDATE items SET child_count = child_count + ?, leaf_count = leaf_count + ?,
			last_child_added_at = max(ifnull(last_child_added_at, 0), ?) WHERE id = ?`, c.children, c.leaves, c.lastAdded, id); err != nil {
			return err
		}
	}
	return im.settle()
}

func (im *importer) importDir(dir ImportDir) error {
	var id, modified int64
	err := im.queryRow(`SELECT id, modified_ns FROM directories WHERE library_path_id = ? AND rel_path = ?`,
		im.batch.PathID, dir.RelPath).Scan(&id, &modified)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		var parent any
		if dir.RelPath != "" {
			parentID, err := im.dirID(parentDir(dir.RelPath))
			if err != nil {
				return err
			}
			parent = parentID
		}
		res, err := im.exec(`INSERT INTO directories (library_path_id, parent_id, rel_path, modified_ns, seen_scan)
			VALUES (?, ?, ?, ?, ?)`, im.batch.PathID, parent, dir.RelPath, dir.ModifiedNS, im.batch.Scan)
		if err != nil {
			return err
		}
		id, _ = res.LastInsertId()
	case err != nil:
		return err
	case modified != dir.ModifiedNS:
		if _, err := im.exec(`UPDATE directories SET modified_ns = ?, seen_scan = ? WHERE id = ?`, dir.ModifiedNS, im.batch.Scan, id); err != nil {
			return err
		}
	}
	im.dirs[dir.RelPath] = id
	return nil
}

func parentDir(rel string) string {
	if p := path.Dir(rel); p != "." {
		return p
	}
	return ""
}

func (im *importer) dirID(rel string) (int64, error) {
	if id, ok := im.dirs[rel]; ok {
		return id, nil
	}
	var id int64
	err := im.queryRow(`SELECT id FROM directories WHERE library_path_id = ? AND rel_path = ?`, im.batch.PathID, rel).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, invalid("folder %q is not in this batch or an earlier one", rel)
	}
	im.dirs[rel] = id
	return id, err
}

func (im *importer) importFile(f *ImportFile, stats *ImportStats) error {
	dir, err := im.dirID(f.Dir)
	if err != nil {
		return err
	}
	var id, size, modified int64
	var missing, problem sql.NullInt64
	err = im.queryRow(`SELECT id, size, modified_ns, missing_since_at, problem FROM files WHERE directory_id = ? AND name = ?`,
		dir, f.Name).Scan(&id, &size, &modified, &missing, &problem)
	state := &importedFile{id: id, facts: f}
	replaced := size != f.Size || modified != f.ModifiedNS
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if state.id, err = im.insertFile(dir, f, im.batch.Scan, im.now); err != nil {
			return err
		}
		state.touched = true
		stats.FilesNew++
	case err != nil:
		return err
	case f.Unprobed && (replaced || missing.Valid):
		// A file that came back as it was keeps everything read from it. A replaced file keeps
		// its tracks until the probe pass has read the new ones.
		state.touched = true
		stats.FilesChanged++
		if _, err := im.exec(`UPDATE files SET role = ?, size = ?, modified_ns = ?, seen_scan = ?, missing_since_at = NULL,
			probed_at = CASE WHEN ? THEN NULL ELSE probed_at END, problem = ? WHERE id = ?`, f.Role, f.Size, f.ModifiedNS,
			im.batch.Scan, replaced, nz(f.Problem), id); err != nil {
			return err
		}
		im.changedFiles = append(im.changedFiles, id)
	case replaced || missing.Valid:
		// The file was replaced or came back: its facts and tracks are read again.
		state.touched = true
		stats.FilesChanged++
		if _, err := im.exec(`UPDATE files SET role = ?, size = ?, modified_ns = ?, seen_scan = ?, missing_since_at = NULL,
			probed_at = ?, container = ?, duration_ms = ?, bitrate = ? WHERE id = ?`, f.Role, f.Size, f.ModifiedNS, im.batch.Scan,
			im.now, nz(f.Container), nz(f.DurationMS), nz(f.Bitrate), id); err != nil {
			return err
		}
		if _, err := im.exec(`DELETE FROM media_streams WHERE file_id = ?`, id); err != nil {
			return err
		}
		if err := im.insertStreams(id, f.Streams); err != nil {
			return err
		}
		// Whatever plays this file has changed: open screens and the search index hear of it.
		if _, err := im.exec(`INSERT INTO changes (at, topic, op, ref_id, library_id)
			SELECT DISTINCT ?, ?, ?, m.item_id, ? FROM media_parts p JOIN media m ON m.id = p.media_id WHERE p.file_id = ?`,
			im.now, TopicItem, OpUpdated, im.batch.LibraryID, id); err != nil {
			return err
		}
		im.changedFiles = append(im.changedFiles, id)
	case FileProblem(problem.Int64) != f.Problem:
		if _, err := im.exec(`UPDATE files SET problem = ? WHERE id = ?`, nz(f.Problem), id); err != nil {
			return err
		}
	}
	im.files[f.FileRef] = state
	return nil
}

// file returns a file of this batch, or one stored by an earlier batch with its facts read back.
func (im *importer) file(ref FileRef) (*importedFile, error) {
	if f, ok := im.files[ref]; ok {
		return f, nil
	}
	dir, err := im.dirID(ref.Dir)
	if err != nil {
		return nil, err
	}
	f := &importedFile{facts: &ImportFile{FileRef: ref}}
	var container sql.NullString
	var duration, bitrate sql.NullInt64
	err = im.queryRow(`SELECT id, size, container, duration_ms, bitrate FROM files WHERE directory_id = ? AND name = ?`, dir, ref.Name).
		Scan(&f.id, &f.facts.Size, &container, &duration, &bitrate)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, invalid("file %q in %q is not in this batch or an earlier one", ref.Name, ref.Dir)
	}
	if err != nil {
		return nil, err
	}
	f.facts.Container, f.facts.DurationMS, f.facts.Bitrate = container.String, duration.Int64, bitrate.Int64
	rows, err := im.tx.Query(`SELECT kind, ifnull(codec, ''), is_default, ifnull(width, 0), ifnull(height, 0), ifnull(hdr, ''),
		ifnull(channels, 0) FROM media_streams WHERE file_id = ? ORDER BY stream_index`, f.id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s Stream
		if err := rows.Scan(&s.Kind, &s.Codec, &s.Default, &s.Width, &s.Height, &s.HDR, &s.Channels); err != nil {
			return nil, err
		}
		f.facts.Streams = append(f.facts.Streams, s)
	}
	im.files[ref] = f
	return f, rows.Err()
}

func (im *importer) importItem(it *ImportItem, stats *ImportStats) error {
	parent, keys, err := im.place(it)
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return invalid("item %q has no key", it.Title)
	}
	// A moved file whose title the owner made or matched by hand stays with that title, whatever
	// its new names say. Any other file is read again under its new names.
	found, err := im.pinnedItem(it)
	if err != nil {
		return err
	}
	if found != nil {
		keys = fileKeys(keys)
	} else if found, err = im.findItem(keys); err != nil {
		return err
	}
	if found == nil {
		// A file that moved brings its title with it when the new names say nothing stored.
		if found, err = im.movedItem(it, parent); err != nil {
			return err
		}
	}
	if found == nil {
		stats.ItemsNew++
		return im.createItem(it, parent, keys)
	}
	im.rows = append(im.rows, found)
	// A key the item did not have yet is added, so the title is found by it next time too.
	for _, key := range keys {
		if im.items[key] == nil {
			if err := im.insertItemKey(im.batch.LibraryID, key, found.id); err != nil {
				return err
			}
			im.items[key] = found
		}
	}
	for i := range it.Versions {
		if err := im.ensureVersion(found.id, &it.Versions[i]); err != nil {
			return err
		}
	}
	return im.relate(found.id, it)
}

// findItem returns the stored item any of the keys names, or nil.
func (im *importer) findItem(keys []ItemKey) (*importedItem, error) {
	var found *importedItem
	for _, key := range keys {
		item, err := im.itemByKey(key)
		if err != nil {
			return nil, err
		}
		if item != nil && found == nil {
			found = item
		}
	}
	return found, nil
}

func (im *importer) itemByKey(key ItemKey) (*importedItem, error) {
	if item, ok := im.items[key]; ok {
		return item, nil
	}
	item := &importedItem{}
	var root, parent, rating sql.NullInt64
	err := im.queryRow(`SELECT i.id, i.root_id, i.parent_id, i.rating_age FROM item_keys k JOIN items i ON i.id = k.item_id
		WHERE k.library_id = ? AND k.kind = ? AND k.key = ?`, im.batch.LibraryID, key.Kind, key.Key).Scan(&item.id, &root, &parent, &rating)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	item.rootID, item.parentID = root.Int64, parent.Int64
	if rating.Valid {
		age := int(rating.Int64)
		item.ratingAge = &age
	}
	im.items[key] = item
	return item, nil
}

func (im *importer) createItem(it *ImportItem, parent *importedItem, keys []ItemKey) error {
	row := itemRow{ImportItem: it, libraryID: im.batch.LibraryID, publicID: NewID("itm"), addedAt: it.AddedAt, ratingAge: it.RatingAge}
	if row.addedAt == 0 {
		row.addedAt = im.now
	}
	if parent != nil {
		row.parentID, row.rootID = parent.id, parent.rootID
		if row.rootID == 0 {
			row.rootID = parent.id
		}
		if row.ratingAge == nil {
			row.ratingAge = parent.ratingAge
		}
		im.count(parent.id).children++
		// Titles nest at most three deep (show, season, episode), so the containers above a
		// leaf are its parent and its root.
		if it.Kind.Playable() {
			im.count(parent.id).leaves++
			if row.rootID != parent.id {
				im.count(row.rootID).leaves++
			}
		}
		for _, id := range []int64{parent.id, row.rootID} {
			im.count(id).lastAdded = max(im.count(id).lastAdded, row.addedAt)
		}
	}
	versions := make([]versionFacts, len(it.Versions))
	for i := range it.Versions {
		facts, err := im.versionFacts(&it.Versions[i])
		if err != nil {
			return err
		}
		versions[i] = facts
	}
	row.durationMS = it.DurationMS
	if row.durationMS == 0 && len(versions) > 0 {
		row.durationMS = versions[0].durationMS
	}
	id, err := im.insertItem(row)
	if err != nil {
		return err
	}
	stored := &importedItem{id: id, rootID: row.rootID, parentID: row.parentID, ratingAge: row.ratingAge}
	im.rows = append(im.rows, stored)
	for _, key := range keys {
		if err := im.insertItemKey(im.batch.LibraryID, key, id); err != nil {
			return err
		}
		im.items[key] = stored
	}
	for _, ext := range it.ExternalIDs {
		if _, err := im.exec(`INSERT INTO item_external_ids (item_id, provider, external_id) VALUES (?, ?, ?)`, id, ext.Provider, ext.ID); err != nil {
			return err
		}
	}
	for _, tag := range it.Tags {
		tagID, err := im.tagID(tag)
		if err != nil {
			return err
		}
		if err := im.tagItem(id, tagID); err != nil {
			return err
		}
	}
	for position, credit := range it.Credits {
		personID, err := im.personID(credit)
		if err != nil {
			return err
		}
		if err := im.insertCredit(id, position, personID, credit.Role, credit.Character); err != nil {
			return err
		}
	}
	for position, image := range it.Images {
		if err := im.insertImage(id, position, image); err != nil {
			return err
		}
	}
	for i := range it.Versions {
		if err := im.createVersion(id, i, &it.Versions[i], versions[i], row.addedAt); err != nil {
			return err
		}
	}
	if err := im.relate(id, it); err != nil {
		return err
	}
	if err := im.restore(id); err != nil {
		return err
	}
	return appendChange(im.tx, Change{At: im.now, Topic: TopicItem, Op: OpCreated, RefID: id, LibraryID: im.batch.LibraryID})
}

func (im *importer) count(id int64) *countChange {
	c := im.counts[id]
	if c == nil {
		c = &countChange{}
		im.counts[id] = c
	}
	return c
}

func (im *importer) tagID(tag TagName) (int64, error) {
	if id, ok := im.tags[tag]; ok {
		return id, nil
	}
	if !tagKinds[tag.Kind] {
		return 0, invalid("unknown tag kind %q", tag.Kind)
	}
	norm := normTag(tag.Name)
	var id int64
	err := im.queryRow(`SELECT id FROM tags WHERE kind = ? AND norm_name = ?`, tag.Kind, norm).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		var res sql.Result
		if res, err = im.exec(`INSERT INTO tags (public_id, kind, name, norm_name) VALUES (?, ?, ?, ?)`,
			NewID("tag"), tag.Kind, strings.TrimSpace(tag.Name), norm); err == nil {
			id, _ = res.LastInsertId()
		}
	}
	im.tags[tag] = id
	return id, err
}

// normTag folds a tag name so "Sci-Fi", "sci fi" and "Science Fiction" are one genre.
func normTag(name string) string {
	folded := strings.Join(strings.FieldsFunc(sorttext.Fold(name), func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == ',' || r == ' ' || r == '\t'
	}), " ")
	if folded == "sci fi" || folded == "scifi" {
		return "science fiction"
	}
	return folded
}

func (im *importer) personID(credit ImportCredit) (int64, error) {
	cacheKey := credit.Person.Provider + "\x00" + credit.Person.ID
	if credit.Person.ID == "" {
		cacheKey = "\x00" + sorttext.Fold(credit.Name)
	}
	if id, ok := im.people[cacheKey]; ok {
		return id, nil
	}
	var id int64
	var err error
	if credit.Person.ID != "" {
		err = im.queryRow(`SELECT person_id FROM person_external_ids WHERE provider = ? AND external_id = ?`,
			credit.Person.Provider, credit.Person.ID).Scan(&id)
	} else {
		err = im.queryRow(`SELECT id FROM people WHERE sort_name = ? ORDER BY id LIMIT 1`, sorttext.Fold(credit.Name)).Scan(&id)
	}
	if errors.Is(err, sql.ErrNoRows) {
		var res sql.Result
		if res, err = im.exec(`INSERT INTO people (public_id, name, sort_name, updated_at) VALUES (?, ?, ?, ?)`,
			NewID("per"), credit.Name, sorttext.Fold(credit.Name), im.now); err != nil {
			return 0, err
		}
		id, _ = res.LastInsertId()
		if credit.Person.ID != "" {
			_, err = im.exec(`INSERT INTO person_external_ids (provider, external_id, person_id) VALUES (?, ?, ?)`,
				credit.Person.Provider, credit.Person.ID, id)
		}
	}
	im.people[cacheKey] = id
	return id, err
}

// versionFacts is the summary a media row carries so lists can filter and show badges.
type versionFacts struct {
	fileIDs      []int64
	container    string
	durationMS   int64
	bitrate      int64
	size         int64
	width        int
	height       int
	videoCodec   string
	audioCodec   string
	audioChannel int
	hdr          string
}

func (im *importer) versionFacts(v *ImportVersion) (versionFacts, error) {
	var facts versionFacts
	if len(v.Parts) == 0 {
		return facts, invalid("a version needs at least one part")
	}
	seen := map[int64]bool{}
	for i, part := range v.Parts {
		f, err := im.file(part.File)
		if err != nil {
			return facts, err
		}
		facts.fileIDs = append(facts.fileIDs, f.id)
		switch {
		case part.StartMS != nil && part.EndMS != nil:
			facts.durationMS += *part.EndMS - *part.StartMS
		case part.StartMS != nil:
			facts.durationMS += f.facts.DurationMS - *part.StartMS
		default:
			facts.durationMS += f.facts.DurationMS
		}
		if !seen[f.id] {
			seen[f.id] = true
			facts.size += f.facts.Size
		}
		if i > 0 {
			continue
		}
		facts.container, facts.bitrate = f.facts.Container, f.facts.Bitrate
		var audio *Stream
		for s := range f.facts.Streams {
			stream := &f.facts.Streams[s]
			switch {
			case stream.Kind == StreamVideo && facts.videoCodec == "":
				facts.videoCodec, facts.width, facts.height, facts.hdr = stream.Codec, stream.Width, stream.Height, stream.HDR
			case stream.Kind == StreamAudio && (audio == nil || stream.Default && !audio.Default):
				audio = stream
			}
		}
		if audio != nil {
			facts.audioCodec, facts.audioChannel = audio.Codec, audio.Channels
		}
	}
	return facts, nil
}

func (im *importer) createVersion(itemID int64, position int, v *ImportVersion, facts versionFacts, addedAt int64) error {
	mediaID, err := im.insertMedia(itemID, position, v, facts, addedAt)
	if err != nil {
		return err
	}
	for i, part := range v.Parts {
		if _, err := im.exec(`INSERT INTO media_parts (media_id, position, file_id, start_ms, end_ms) VALUES (?, ?, ?, ?, ?)`,
			mediaID, i, facts.fileIDs[i], part.StartMS, part.EndMS); err != nil {
			return err
		}
	}
	for _, marker := range v.Markers {
		if err := im.insertMarker(mediaID, marker); err != nil {
			return err
		}
	}
	return im.linkSidecars(mediaID, v, false)
}

// ensureVersion gives a stored item a version if it does not have it yet. A version is
// recognised by the file, and the place in the file, that its first part plays. The keys say
// which item a file belongs to, so a version found on another item moves to this one: that is
// a file renamed into a different title.
func (im *importer) ensureVersion(itemID int64, v *ImportVersion) error {
	facts, err := im.versionFacts(v)
	if err != nil {
		return err
	}
	var mediaID, owner int64
	err = im.queryRow(`SELECT m.id, m.item_id FROM media_parts p JOIN media m ON m.id = p.media_id
		WHERE p.file_id = ? AND p.position = 0 AND p.start_ms IS ? ORDER BY m.item_id = ? DESC LIMIT 1`,
		facts.fileIDs[0], v.Parts[0].StartMS, itemID).Scan(&mediaID, &owner)
	if errors.Is(err, sql.ErrNoRows) || err == nil && owner != itemID && v.Shared {
		var position int
		if err := im.queryRow(`SELECT count(*) FROM media WHERE item_id = ?`, itemID).Scan(&position); err != nil {
			return err
		}
		im.unsettled = append(im.unsettled, itemID)
		if err := im.createVersion(itemID, position, v, facts, im.now); err != nil {
			return err
		}
		return appendChange(im.tx, Change{At: im.now, Topic: TopicItem, Op: OpUpdated, RefID: itemID, LibraryID: im.batch.LibraryID})
	}
	if err != nil {
		return err
	}
	if owner != itemID {
		if _, err := im.exec(`UPDATE media SET item_id = ?, position = (SELECT count(*) FROM media WHERE item_id = ?) WHERE id = ?`,
			itemID, itemID, mediaID); err != nil {
			return err
		}
		im.unsettled = append(im.unsettled, owner, itemID)
	}
	if err := im.ensureParts(mediaID, v, facts); err != nil {
		return err
	}
	return im.linkSidecars(mediaID, v, true)
}

// linkSidecars ties subtitle and similar files to their version. For a version that already
// existed, only files this batch added or changed can need tying.
func (im *importer) linkSidecars(mediaID int64, v *ImportVersion, onlyTouched bool) error {
	for _, ref := range v.Sidecars {
		f, err := im.file(ref)
		if err != nil {
			return err
		}
		if onlyTouched && !f.touched {
			continue
		}
		if _, err := im.exec(`UPDATE files SET media_id = ? WHERE id = ? AND media_id IS NOT ?`, mediaID, f.id, mediaID); err != nil {
			return err
		}
	}
	return nil
}
