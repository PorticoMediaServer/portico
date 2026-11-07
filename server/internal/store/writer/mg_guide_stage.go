package writer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// These constructors run before the admitted writer hold. They own every byte
// and bind every native column, including absent optional attributes. The parent
// still owns parser validation, byte admission and the immutable whole-import
// manifest. No locator or programme field authorizes a source or a capture.
type mgGuideChannel struct {
	id, provider, name, number, group, availableStart, availableEnd string
	position                                                        int64
	locator                                                         []byte
	sourceNumber                                                    *string
	attributes                                                      *mgGuideChannelAttributes
	logo                                                            *string
}
type mgGuideChannelAttributes struct{ country, catchup, source, days string }
type mgGuideProgramme struct {
	id, channel, provider, title, start, end, lineage string
	metadata                                          *mgGuideProgrammeMetadata
	icon                                              *string
}
type mgGuideProgrammeMetadata struct{ series, episode, newEvidence, description, facts string }
type mgGuideChoice struct{ key, name, group string }
type mgGuideFrozenStage struct {
	family       mgGuideFamily
	rows         [8][]any
	count, bytes int64
	payload      [32]byte
}

func mgGuideNullable(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}
func freezeMGGuideChannels(rows []mgGuideChannel) (mgGuideFrozenStage, error) {
	if len(rows) == 0 || len(rows) > 8 {
		return mgGuideFrozenStage{}, ErrInvalid
	}
	var values [][]any
	for _, r := range rows {
		if r.id == "" || r.name == "" || r.position < 0 || len(r.locator) == 0 {
			return mgGuideFrozenStage{}, ErrInvalid
		}
		a, country, catchup, source, days := int64(0), any(nil), any(nil), any(nil), any(nil)
		if r.attributes != nil {
			a = 1
			country = r.attributes.country
			catchup = r.attributes.catchup
			source = r.attributes.source
			days = r.attributes.days
		}
		logo := int64(0)
		if r.logo != nil {
			logo = 1
		}
		values = append(values, []any{r.id, r.provider, r.name, r.number, mgGuideNullable(r.sourceNumber), r.group, r.position, r.locator, r.availableStart, r.availableEnd, a, country, catchup, source, days, logo, mgGuideNullable(r.logo)})
	}
	return freezeMGGuideRows(mgGuideChannels, values)
}
func freezeMGGuideProgrammes(rows []mgGuideProgramme) (mgGuideFrozenStage, error) {
	if len(rows) == 0 || len(rows) > 8 {
		return mgGuideFrozenStage{}, ErrInvalid
	}
	var values [][]any
	for _, r := range rows {
		if r.id == "" || r.channel == "" || r.start == "" || r.end <= r.start {
			return mgGuideFrozenStage{}, ErrInvalid
		}
		m, series, episode, evidence, description, facts := int64(0), any(nil), any(nil), any(nil), any(nil), any("{}")
		if r.metadata != nil {
			m = 1
			series = r.metadata.series
			episode = r.metadata.episode
			evidence = r.metadata.newEvidence
			description = r.metadata.description
			facts = r.metadata.facts
		}
		icon := int64(0)
		if r.icon != nil {
			icon = 1
		}
		values = append(values, []any{r.id, r.channel, r.provider, r.title, r.start, r.end, r.lineage, m, series, episode, evidence, description, facts, icon, mgGuideNullable(r.icon)})
	}
	return freezeMGGuideRows(mgGuideProgrammes, values)
}
func freezeMGGuideChoices(rows []mgGuideChoice) (mgGuideFrozenStage, error) {
	if len(rows) == 0 || len(rows) > 8 {
		return mgGuideFrozenStage{}, ErrInvalid
	}
	var values [][]any
	for _, r := range rows {
		if r.key == "" {
			return mgGuideFrozenStage{}, ErrInvalid
		}
		values = append(values, []any{r.key, r.name, r.group})
	}
	return freezeMGGuideRows(mgGuideChoices, values)
}
func mgGuideHashValue(h hash.Hash, value any) (int64, error) {
	var size int64
	switch v := value.(type) {
	case nil:
		h.Write([]byte{0})
		size = 1
	case int64:
		h.Write([]byte{1})
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(v)))
		size = 9
	case string:
		if !utf8.ValidString(v) {
			return 0, ErrInvalid
		}
		h.Write([]byte{2})
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(v))))
		h.Write([]byte(v))
		size = 9 + int64(len(v))
	case []byte:
		h.Write([]byte{3})
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(v))))
		h.Write(v)
		size = 9 + int64(len(v))
	default:
		return 0, ErrInvalid
	}
	return size, nil
}
func freezeMGGuideRows(family mgGuideFamily, values [][]any) (mgGuideFrozenStage, error) {
	var out mgGuideFrozenStage
	if len(values) == 0 || len(values) > int(mgGuideStageRows) {
		return out, ErrInvalid
	}
	out.family = family
	out.count = int64(len(values))
	h := sha256.New()
	h.Write([]byte("portico.mg.guide.native.rows.v1\x00"))
	h.Write([]byte{byte(family)})
	for j, row := range values {
		out.rows[j] = make([]any, len(row))
		for k, v := range row {
			size := int64(9)
			switch x := v.(type) {
			case string:
				size += int64(len(x))
			case []byte:
				size += int64(len(x))
			}
			if size > mgGuideStageBytes-out.bytes {
				return mgGuideFrozenStage{}, ErrInvalid
			}
			if b, ok := v.([]byte); ok {
				v = append([]byte(nil), b...)
			}
			n, err := mgGuideHashValue(h, v)
			if err != nil {
				return mgGuideFrozenStage{}, err
			}
			out.bytes += n
			if out.bytes > mgGuideStageBytes {
				return mgGuideFrozenStage{}, ErrInvalid
			}
			out.rows[j][k] = v
		}
	}
	copy(out.payload[:], h.Sum(nil))
	return out, nil
}

const (
	mgGuideInsertChannel   stmt.ID = "mg.guide.stage.channel"
	mgGuideInsertProgramme stmt.ID = "mg.guide.stage.programme"
	mgGuideInsertChoice    stmt.ID = "mg.guide.stage.choice"
	mgGuideAdvanceStage    stmt.ID = "mg.guide.stage.advance"
)

func mgGuideStageDefinitions() []stmt.Definition {
	txt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	blob := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	nullable := func(p stmt.Param) stmt.Param { p.Nullable = true; return p }
	defs := []stmt.Definition{
		{ID: mgGuideInsertChannel, Kind: stmt.Write, SQL: `INSERT INTO live_channel_versions(generation_id,channel_id,provider_key,name,number,source_number,group_name,position,locator_sealed,available_start,available_end,has_live_channel_attributes,attribute_country,attribute_catchup,attribute_catchup_source,attribute_catchup_days,has_live_channel_logos,logo_url) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING generation_id,channel_id,provider_key,name,number,source_number,group_name,position,locator_sealed,available_start,available_end,has_live_channel_attributes,attribute_country,attribute_catchup,attribute_catchup_source,attribute_catchup_days,has_live_channel_logos,logo_url`, Params: []stmt.Param{txt("generation"), txt("channel"), txt("provider"), txt("name"), txt("number"), nullable(txt("source_number")), txt("group"), integer("position"), blob("locator"), txt("start"), txt("end"), integer("attributes"), nullable(txt("country")), nullable(txt("catchup")), nullable(txt("catchup_source")), nullable(txt("days")), integer("logos"), nullable(txt("logo"))}},
		{ID: mgGuideInsertProgramme, Kind: stmt.Write, SQL: `INSERT INTO live_programmes(generation_id,id,channel_id,provider_key,title,start_utc,end_utc,lineage,has_live_programme_metadata,metadata_series_id,metadata_episode_id,metadata_new_evidence,metadata_description,metadata_facts,has_live_programme_icons,icon_url) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING generation_id,id,channel_id,provider_key,title,start_utc,end_utc,lineage,has_live_programme_metadata,metadata_series_id,metadata_episode_id,metadata_new_evidence,metadata_description,metadata_facts,has_live_programme_icons,icon_url`, Params: []stmt.Param{txt("generation"), txt("programme"), txt("channel"), txt("provider"), txt("title"), txt("start"), txt("end"), txt("lineage"), integer("metadata"), nullable(txt("series")), nullable(txt("episode")), nullable(txt("new")), nullable(txt("description")), nullable(txt("facts")), integer("icons"), nullable(txt("icon"))}},
		{ID: mgGuideInsertChoice, Kind: stmt.Write, SQL: `INSERT INTO live_channel_choices(generation_id,channel_key,name,group_name) VALUES(?,?,?,?) RETURNING generation_id,channel_key,name,group_name`, Params: []stmt.Param{txt("generation"), txt("key"), txt("name"), txt("group")}},
		{ID: mgGuideAdvanceStage, Kind: stmt.Write, SQL: `UPDATE live_operations INDEXED BY sqlite_autoindex_live_operations_1 SET last_command_sequence=?,stage_revision=?,staged_channel_count=?,staged_programme_count=?,staged_choice_count=?,staged_bytes=?,stage_digest=? WHERE request_id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND stage_revision=? AND status='staging' AND sealed=0 AND staged_channel_count=? AND staged_programme_count=? AND staged_choice_count=? AND staged_bytes=? AND stage_digest=?`, Params: []stmt.Param{integer("next_sequence"), integer("next_revision"), integer("channels"), integer("programmes"), integer("choices"), integer("bytes"), blob("chain"), txt("request"), blob("incarnation"), integer("sequence"), integer("ack"), integer("revision"), integer("before_channels"), integer("before_programmes"), integer("before_choices"), integer("before_bytes"), blob("before_chain")}},
	}
	for j := range defs {
		defs[j].Plan.Hot = true
		if defs[j].ID == mgGuideAdvanceStage {
			defs[j].Plan.Indexes = []string{"sqlite_autoindex_live_operations_1"}
			defs[j].Plan.AutoIndexes = []stmt.AutoIndexBinding{{Table: "live_operations", Name: "sqlite_autoindex_live_operations_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "request_id", Collation: "BINARY"}}}}
		}
		defs[j].Owner = "store/writer MG guide staging effects"
		defs[j].CallSites = []string{"store/writer/mg_guide_stage.go"}
		args := make([]any, len(defs[j].Params))
		for k, p := range defs[j].Params {
			switch p.Type {
			case stmt.Text:
				args[k] = "missing"
			case stmt.Integer:
				args[k] = int64(1)
			case stmt.Blob:
				args[k] = make([]byte, 32)
			}
		}
		defs[j].Cohorts = []stmt.Cohort{{Name: "missing-native-keys", Bindings: args}}

	}
	return defs
}
func mgStageGuideNative(ctx context.Context, t *commandTurn, before mgGuideNative, identity CommandIdentity, stage mgGuideFrozenStage) (out mgGuideNativeResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mgGuideNextNative(t, before, identity); err != nil {
		return out, err
	}
	actual, err := mgReadGuideNative(ctx, t, before.request)
	if err != nil {
		return out, err
	}
	if actual != before {
		return out, ErrConflict
	}
	p := before.progress
	if stage.family < mgGuideChannels || stage.family > mgGuideChoices || stage.count <= 0 || stage.count > 8 {
		return out, ErrInvalid
	}
	delta := mgGuideStageDelta{stage.family, p.manifest.source, p.manifest.generation, identity.Sequence, p.staged[int(stage.family)-1], stage.count, stage.bytes + stage.count*(9+int64(len(p.manifest.generation))), stage.payload}
	next, err := planMGGuideStage(p, delta)
	if err != nil {
		return out, err
	}
	head, err := mgGuideCurrentHead(ctx, t, p.manifest.source)
	if err != nil {
		return out, err
	}
	if head.revision != p.manifest.expectedRevision || head.generation != p.manifest.previousGeneration {
		return out, ErrConflict
	}
	applied, err := mgGuideNativeTime(ctx, t)
	if err != nil {
		return out, err
	}
	id := mgGuideInsertChannel
	if stage.family == mgGuideProgrammes {
		id = mgGuideInsertProgramme
	}
	if stage.family == mgGuideChoices {
		id = mgGuideInsertChoice
	}
	for j := int64(0); j < stage.count; j++ {
		args := append([]any{p.manifest.generation}, stage.rows[j]...)
		row, e := mgGuidePublicationQuery(ctx, t, id, args...)
		if e != nil {
			return out, e
		}
		values := make([]any, len(args))
		targets := make([]any, len(args))
		for k := range values {
			targets[k] = &values[k]
		}
		if e = row.Scan(targets...); e != nil {
			return out, e
		}
		for k, want := range args {
			switch v := want.(type) {
			case nil:
				if values[k] != nil {
					return out, ErrConflict
				}
			case string:
				if got, ok := values[k].(string); !ok || got != v {
					return out, ErrConflict
				}
			case int64:
				if got, ok := values[k].(int64); !ok || got != v {
					return out, ErrConflict
				}
			case []byte:
				if got, ok := values[k].([]byte); !ok || !bytes.Equal(got, v) {
					return out, ErrConflict
				}
			default:
				return out, ErrInvalid
			}
		}
	}
	if err = mgGuidePublicationChange(ctx, t, mgGuideAdvanceStage, next.sequence, next.revision, next.staged[0], next.staged[1], next.staged[2], next.bytes, next.chain[:], before.request, before.incarnation[:], p.sequence, before.acknowledged, p.revision, p.staged[0], p.staged[1], p.staged[2], p.bytes, p.chain[:]); err != nil {
		return out, err
	}
	want := before
	want.progress = next
	post, err := mgReadGuideNative(ctx, t, before.request)
	if err != nil {
		return out, err
	}
	if post != want {
		return out, ErrConflict
	}
	return mgGuideNativeResult{post, head, applied}, nil
}
