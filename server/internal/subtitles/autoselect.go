package subtitles

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/subtitleoffer"
	"strings"
)

// The viewer's subtitle preferences existed and nothing read them: a profile set
// to "subtitles on, Spanish" started every title with subtitles off, and a film
// whose aliens speak in a forced track showed no translation unless someone went
// looking for it. The selection below runs once, when a session's subtitle state
// is first created, and only ever chooses a text track that is already stored:
// it never extracts, converts or burns anything in, so it adds one indexed read
// to starting a title and cannot fail it.

// AutoChoice is what the viewer wants without being asked.
type AutoChoice struct {
	ForeignAudio          bool
	PreferSDH             bool
	DisableUntaggedForced bool
	FullFallback          bool
	NoLanguage            bool
	// Enabled is the viewer's "always show subtitles" preference.
	Enabled bool
	// Off is the viewer's "never choose subtitles for me": no track is chosen, not even
	// a forced one or one for audio in a language they did not ask for.
	Off bool
	// Languages is the viewer's ordered subtitle language list.
	Languages []string
	// AudioLanguage is the language of the audio track the session will play. A
	// forced track is only meaningful beside audio in the same language.
	AudioLanguage string
}

// AutoCandidate is one stored track that could be chosen.
type AutoCandidate struct {
	ID              string
	Revision        int64
	Language        string
	Title           string
	HearingImpaired bool
	Forced          bool
	Default         bool
	OffsetUS        int64
}

func subtitleLanguageKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.IndexAny(s, "-_"); i > 0 {
		s = s[:i]
	}
	if two, ok := iso639ThreeToTwo[s]; ok {
		return two
	}
	if s == "und" {
		return ""
	}
	return s
}

// TwoLetterLanguage is the ISO 639-1 code of a three-letter ISO 639-2 code, in
// either its bibliographic or its terminologic form. Audio and subtitle
// matching fold language tags through this one table. Two folds go beyond the
// standard and are kept: Bokmål is matched as Norwegian and Filipino as
// Tagalog.
func TwoLetterLanguage(three string) (string, bool) {
	two, ok := iso639ThreeToTwo[three]
	return two, ok
}

var iso639ThreeToTwo = map[string]string{
	"aar": "aa", "abk": "ab", "afr": "af", "aka": "ak", "alb": "sq", "amh": "am", "ara": "ar", "arg": "an", "arm": "hy",
	"asm": "as", "ava": "av", "ave": "ae", "aym": "ay", "aze": "az", "bak": "ba", "bam": "bm", "baq": "eu", "bel": "be",
	"ben": "bn", "bih": "bh", "bis": "bi", "bod": "bo", "bos": "bs", "bre": "br", "bul": "bg", "bur": "my", "cat": "ca",
	"ces": "cs", "cha": "ch", "che": "ce", "chi": "zh", "chu": "cu", "chv": "cv", "cor": "kw", "cos": "co", "cre": "cr",
	"cym": "cy", "cze": "cs", "dan": "da", "deu": "de", "div": "dv", "dut": "nl", "dzo": "dz", "ell": "el", "eng": "en",
	"epo": "eo", "est": "et", "eus": "eu", "ewe": "ee", "fao": "fo", "fas": "fa", "fij": "fj", "fil": "tl", "fin": "fi",
	"fra": "fr", "fre": "fr", "fry": "fy", "ful": "ff", "geo": "ka", "ger": "de", "gla": "gd", "gle": "ga", "glg": "gl",
	"glv": "gv", "gre": "el", "grn": "gn", "guj": "gu", "hat": "ht", "hau": "ha", "heb": "he", "her": "hz", "hin": "hi",
	"hmo": "ho", "hrv": "hr", "hun": "hu", "hye": "hy", "ibo": "ig", "ice": "is", "ido": "io", "iii": "ii", "iku": "iu",
	"ile": "ie", "ina": "ia", "ind": "id", "ipk": "ik", "isl": "is", "ita": "it", "jav": "jv", "jpn": "ja", "kal": "kl",
	"kan": "kn", "kas": "ks", "kat": "ka", "kau": "kr", "kaz": "kk", "khm": "km", "kik": "ki", "kin": "rw", "kir": "ky",
	"kom": "kv", "kon": "kg", "kor": "ko", "kua": "kj", "kur": "ku", "lao": "lo", "lat": "la", "lav": "lv", "lim": "li",
	"lin": "ln", "lit": "lt", "ltz": "lb", "lub": "lu", "lug": "lg", "mac": "mk", "mah": "mh", "mal": "ml", "mao": "mi",
	"mar": "mr", "may": "ms", "mkd": "mk", "mlg": "mg", "mlt": "mt", "mon": "mn", "mri": "mi", "msa": "ms", "mya": "my",
	"nau": "na", "nav": "nv", "nbl": "nr", "nde": "nd", "ndo": "ng", "nep": "ne", "nld": "nl", "nno": "nn", "nob": "no",
	"nor": "no", "nya": "ny", "oci": "oc", "oji": "oj", "ori": "or", "orm": "om", "oss": "os", "pan": "pa", "per": "fa",
	"pli": "pi", "pol": "pl", "por": "pt", "pus": "ps", "que": "qu", "roh": "rm", "ron": "ro", "rum": "ro", "run": "rn",
	"rus": "ru", "sag": "sg", "san": "sa", "sin": "si", "slk": "sk", "slo": "sk", "slv": "sl", "sme": "se", "smo": "sm",
	"sna": "sn", "snd": "sd", "som": "so", "sot": "st", "spa": "es", "sqi": "sq", "srd": "sc", "srp": "sr", "ssw": "ss",
	"sun": "su", "swa": "sw", "swe": "sv", "tah": "ty", "tam": "ta", "tat": "tt", "tel": "te", "tgk": "tg", "tgl": "tl",
	"tha": "th", "tib": "bo", "tir": "ti", "ton": "to", "tsn": "tn", "tso": "ts", "tuk": "tk", "tur": "tr", "twi": "tw",
	"uig": "ug", "ukr": "uk", "urd": "ur", "uzb": "uz", "ven": "ve", "vie": "vi", "vol": "vo", "wel": "cy", "wln": "wa",
	"wol": "wo", "xho": "xh", "yid": "yi", "yor": "yo", "zha": "za", "zho": "zh", "zul": "zu",
}

func hearingImpairedTitle(s string) bool { return subtitleoffer.HearingImpairedTitle(s) }

// ChooseAutomatic applies the rule. With subtitles off altogether: nothing. With subtitles on: the first preferred
// language that has a track, taking an ordinary track over an SDH one and never a
// forced one (a forced track is a fragment, not the dialogue). With subtitles
// off: a forced track in the language of the audio, because that is the part of
// the film the audio does not carry. Otherwise nothing.
func ChooseAutomatic(choice AutoChoice, candidates []AutoCandidate) *AutoCandidate {
	if choice.Off || choice.NoLanguage {
		return nil
	}
	full := func() *AutoCandidate {
		for _, want := range choice.Languages {
			key := subtitleLanguageKey(want)
			if key == "" {
				continue
			}
			var fallback *AutoCandidate
			for i := range candidates {
				c := &candidates[i]
				if c.Forced || subtitleLanguageKey(c.Language) != key {
					continue
				}
				sdh := c.HearingImpaired || hearingImpairedTitle(c.Title)
				if sdh == choice.PreferSDH {
					return c
				}
				if fallback == nil {
					fallback = c
				}
			}
			if fallback != nil {
				return fallback
			}
		}
		return nil
	}
	if choice.Enabled || choice.ForeignAudio {
		return full()
	}

	audio := subtitleLanguageKey(choice.AudioLanguage)
	if audio == "" {
		return nil
	}
	for i := range candidates {
		if c := &candidates[i]; c.Forced && subtitleLanguageKey(c.Language) == audio {
			return c
		}
	}
	// One untagged forced track is the file's translation fragment. Multiple
	// untagged tracks remain a manual choice, even when one is marked default.
	var untagged *AutoCandidate
	for i := range candidates {
		c := &candidates[i]
		if !choice.DisableUntaggedForced && c.Forced && subtitleLanguageKey(c.Language) == "" {
			if untagged != nil {
				return nil
			}
			untagged = c
		}
	}
	if untagged != nil {
		return untagged
	}
	if choice.FullFallback {
		return full()
	}
	return nil
}

// ViewerKey is the key a viewer's personal subtitle tracks are stored under.
func ViewerKey(authority, account, profile, server string) string {
	return identityDigest(authority + "\x00" + account + "\x00" + profile + "\x00" + server)
}

// AutoSelectTx creates a session's subtitle state and, the first time only,
// applies the viewer's preferences to the tracks already stored for the source.
// owner is the viewer key personal tracks are stored under.
func AutoSelectTx(ctx context.Context, tx *sql.Tx, session string, generation int, item, source, owner string, choice AutoChoice) error {
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO playback_subtitle_state(session_id,generation) VALUES(?,?)`, session, generation)
	if err != nil {
		return err
	}
	if created, _ := result.RowsAffected(); created != 1 {
		return nil
	}
	if !choice.Enabled && !choice.ForeignAudio && choice.AudioLanguage == "" {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT r.id,v.revision,v.language,v.title,COALESCE(rr.is_forced,0),COALESCE(rr.is_default,0),v.offset_us,COALESCE(rr.hearing_impaired,0) FROM subtitle_resources r JOIN subtitle_revisions v ON v.resource_id=r.id AND v.revision=r.current_revision LEFT JOIN subtitle_render_revisions rr ON rr.resource_id=r.id AND rr.revision=v.revision JOIN catalog_assets a ON a.token=r.source_id WHERE r.item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND r.source_id=? AND r.deleted=0 AND (r.scope='shared' OR r.owner=?) AND COALESCE(rr.renderer,'external_text')='external_text' AND (COALESCE(rr.source_evidence,'')='' OR rr.source_evidence=(SELECT evidence FROM subtitle_remote_sessions WHERE session_id=?)) AND v.source_size=a.size AND v.source_modified_ns=a.modified_ns AND `+strings.ReplaceAll(discSessionResourcePredicate, "$SESSION", "?4")+` ORDER BY COALESCE(rr.is_default,0) DESC,r.id LIMIT 64`, item, source, owner, session)
	if err != nil {
		// A schema this query does not fit must never stop a title from starting.
		return nil
	}
	var candidates []AutoCandidate
	for rows.Next() {
		var c AutoCandidate
		if rows.Scan(&c.ID, &c.Revision, &c.Language, &c.Title, &c.Forced, &c.Default, &c.OffsetUS, &c.HearingImpaired) != nil {
			break
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	chosen := ChooseAutomatic(choice, candidates)
	if chosen == nil {
		return nil
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO playback_subtitle_pins VALUES(?,?,?)`, session, chosen.ID, chosen.Revision); err != nil {
		return nil
	}
	_, _ = tx.ExecContext(ctx, `UPDATE playback_subtitle_state SET resource_id=?,resource_revision=?,offset_us=?,renderer='external_text' WHERE session_id=? AND generation=?`, chosen.ID, chosen.Revision, chosen.OffsetUS, session, generation)
	return nil
}
