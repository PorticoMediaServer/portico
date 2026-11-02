package compactcatalog

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

const realisticWordSource = "https://raw.githubusercontent.com/hermitdave/FrequencyWords/525f9b560de45753a5ea01069454e72e9aa541c6/content/2018/en/en_50k.txt"
const realisticPOSSource = "https://raw.githubusercontent.com/nltk/nltk_data/550b6625bcef1f2abff2ff770a5a0d272c9c6b2a/packages/corpora/wordnet.zip"
const realisticPOSSHA = "cbda5ea6eef7f36a97a43d4a75f85e07fccbb4f23657d27b4ccbc93e2646ab59"

const realisticWordSHA = "5351ff405b1126ef555791dd4d9798a48e3e9a501a9fc481a9da957752cfb458"

type realisticQuery struct {
	Name            string `json:"name"`
	Q               string `json:"q"`
	TitleMatches    int    `json:"title_matches"`
	DocumentMatches int    `json:"document_matches"`
}
type realisticCorpus struct {
	POSSource      string           `json:"pos_source"`
	POSSHA         string           `json:"pos_sha256"`
	POSLicense     string           `json:"pos_license"`
	Nouns          int              `json:"noun_vocabulary"`
	Adjectives     int              `json:"adjective_vocabulary"`
	Source         string           `json:"source"`
	SHA            string           `json:"sha256"`
	License        string           `json:"content_license"`
	Vocabulary     int              `json:"vocabulary"`
	Items          int              `json:"items"`
	Anchors        int              `json:"anchors"`
	RetitleSeconds float64          `json:"retitle_seconds"`
	Examples       []string         `json:"examples"`
	Queries        []realisticQuery `json:"queries"`
	WordDocuments  map[string]int   `json:"word_documents"`
	KindDocuments  map[string]int   `json:"kind_documents"`
}

// Extend the existing seeder; memory is bounded by the fixed 50k vocabulary
// and one 256-ID page. Titles sample empirical word frequencies and templates,
// not a token shared by every title. One anchor per 10k keeps rare probes positive.
func seedRealisticSearchTitles(t *testing.T, db *sql.DB, n int) realisticCorpus {
	t.Helper()
	start := time.Now()
	raw, err := os.ReadFile(os.Getenv("PORTICO_SEARCH_WORD_SOURCE"))
	if err != nil || len(raw) > 2_000_000 {
		t.Fatalf("word source: %v", err)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != realisticWordSHA {
		t.Fatal("word source SHA mismatch")
	}
	posRaw, err := os.ReadFile(os.Getenv("PORTICO_SEARCH_POS_SOURCE"))
	if err != nil || len(posRaw) > 20_000_000 {
		t.Fatalf("POS source: %v", err)
	}
	posSum := sha256.Sum256(posRaw)
	if hex.EncodeToString(posSum[:]) != realisticPOSSHA {
		t.Fatal("POS source SHA mismatch")
	}
	archive, err := zip.NewReader(bytes.NewReader(posRaw), int64(len(posRaw)))
	if err != nil {
		t.Fatal(err)
	}
	pos := map[string]int{}
	found := 0
	for _, file := range archive.File {
		kind := 0
		switch file.Name {
		case "wordnet/index.noun":
			kind = 1
		case "wordnet/index.adj":
			kind = 2
		}
		if kind == 0 {
			continue
		}
		if file.UncompressedSize64 > 10_000_000 {
			t.Fatal("oversized POS index")
		}
		in, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(in)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" || line[0] == ' ' {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) > 1 {
				pos[fields[0]] |= kind
			}
		}
		if err := scanner.Err(); err != nil {
			in.Close()
			t.Fatal(err)
		}
		if err := in.Close(); err != nil {
			t.Fatal(err)
		}
		found++
	}
	if found != 2 {
		t.Fatal("missing POS indexes")
	}
	stop := map[string]bool{}
	for _, word := range strings.Fields("a an the of in on to at by for from with and or nor but yet so as is are was were be been being am do does did done had have has can could will would should may might must not no yes you your yours i me my mine we us our ours they them their theirs he him his she her hers it its this that these those there here then now just really very well oh uh um huh yeah okay sir said say says see saw look know knows get got go goes going come came want wants gonna wanna gotta lets let what why how when where who whom whose which if than all some any many much more most less enough only such other too also again even ever never each every both one two three four five im ive id dont cant didnt thats youre theres doesnt hes shes isnt wont wouldnt wasnt werent ill youll fuck fucking shit bullshit asshole don isn aren hasn haven wasn weren shouldn wouldn couldn didn doesn mustn shan ohh mmm hmm about out down away else") {
		stop[word] = true
	}
	type wordWeight struct {
		word       string
		cumulative int64
	}
	vocabulary := []wordWeight{}
	nouns, adjectives := []wordWeight{}, []wordWeight{}
	nounTotal, adjTotal := int64(0), int64(0)
	total := int64(0)
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	lines := 0
	for scanner.Scan() {
		lines++
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			t.Fatal("invalid frequency row")
		}
		word := fields[0]
		if len(word) < 3 || len(word) > 18 || stop[word] || pos[word] == 0 {
			continue
		}
		valid := true
		for _, r := range word {
			if r < 'a' || r > 'z' {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		count, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || count < 1 {
			t.Fatal("invalid frequency count")
		}
		total += count
		vocabulary = append(vocabulary, wordWeight{word, total})
		if pos[word]&1 != 0 {
			nounTotal += count
			nouns = append(nouns, wordWeight{word, nounTotal})
		}
		if pos[word]&2 != 0 {
			adjTotal += count
			adjectives = append(adjectives, wordWeight{word, adjTotal})
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if lines != 50000 || len(vocabulary) < 10000 || len(nouns) < 8000 || len(adjectives) < 2000 || total <= 0 {
		t.Fatalf("vocabulary %d/%d", len(vocabulary), lines)
	}
	random := rand.New(rand.NewSource(7112026))
	pick := func(pool []wordWeight, total int64) string {
		draw := random.Int63n(total)
		return pool[sort.Search(len(pool), func(i int) bool { return pool[i].cumulative > draw })].word
	}
	noun := func() string { return pick(nouns, nounTotal) }
	adjective := func() string { return pick(adjectives, adjTotal) }
	corpus := realisticCorpus{POSSource: realisticPOSSource, POSSHA: realisticPOSSHA, POSLicense: "Princeton WordNet3.0; https://wordnet.princeton.edu/license-and-commercial-use", Nouns: len(nouns), Adjectives: len(adjectives), Source: realisticWordSource, SHA: realisticWordSHA, License: "CC-BY-SA-4.0; https://github.com/hermitdave/FrequencyWords#license", Vocabulary: len(vocabulary), Items: n, WordDocuments: map[string]int{}, KindDocuments: map[string]int{}, Queries: []realisticQuery{{Name: "rare", Q: "extraordinary"}, {Name: "rare-common", Q: "extraordinary the"}, {Name: "common", Q: "the"}, {Name: "short-rare-control", Q: "gossamer"}, {Name: "short-rare-common-control", Q: "gossamer the"}}}
	after := int64(0)
	count := 0
	for {
		rows, err := db.Query(`SELECT id FROM catalog_entities WHERE kind=1 AND id>? ORDER BY id LIMIT 256`, after)
		if err != nil {
			t.Fatal(err)
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if len(ids) == 0 {
			break
		}
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			count++
			words := []string{}
			if count%10000 == 42 {
				words = []string{"the", "extraordinary", "gossamer"}
				corpus.Anchors++
			} else {
				switch random.Intn(5) {
				case 0:
					words = []string{"the", adjective(), noun()}
				case 1:
					words = []string{noun(), "of", noun()}
				case 2:
					words = []string{noun(), "and", noun()}
				case 3:
					words = []string{adjective(), noun()}
				case 4:
					words = []string{noun()}
				}
			}
			unique := map[string]bool{}
			titleWords := make([]string, len(words))
			for i, w := range words {
				unique[w] = true
				titleWords[i] = strings.ToUpper(w[:1]) + w[1:]
			}
			for w := range unique {
				corpus.WordDocuments[w]++
			}
			for i, q := range corpus.Queries {
				matches := true
				for _, term := range strings.Fields(q.Q) {
					found := false
					for _, w := range words {
						if strings.HasPrefix(w, term) {
							found = true
							break
						}
					}
					if !found {
						matches = false
						break
					}
				}
				if matches {
					corpus.Queries[i].TitleMatches++
				}
			}
			title := strings.Join(titleWords, " ")
			if len(corpus.Examples) < 24 {
				corpus.Examples = append(corpus.Examples, title)
			}
			key := strings.ToLower(title)
			if _, err = tx.Exec(`UPDATE catalog_entities SET title=?,sort_key=?,sort_head=? WHERE id=?`, title, key, key[:1], id); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			if _, err = tx.Exec(`UPDATE catalog_browse_rows SET title=?,sort_key=?,head=? WHERE entity_id=?`, title, key, key[:1], id); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		after = ids[len(ids)-1]
	}
	if count != n {
		t.Fatalf("retitled%d expected%d", count, n)
	}
	corpus.RetitleSeconds = time.Since(start).Seconds()
	return corpus
}

func validateRealisticSearchCorpus(t *testing.T, db *sql.DB, corpus *realisticCorpus) {
	t.Helper()
	for i, q := range corpus.Queries {
		terms := []string{}
		for _, word := range strings.Fields(q.Q) {
			terms = append(terms, `"`+word+`"*`)
		}
		match := "search : (" + strings.Join(terms, " AND ") + ")"
		var count int
		if err := db.QueryRow(`SELECT count(*) FROM catalog_search_titles WHERE catalog_search_titles MATCH ?`, match).Scan(&count); err != nil {
			t.Fatal(err)
		}
		corpus.Queries[i].DocumentMatches = count
		if count < 1 || count != q.TitleMatches {
			t.Fatalf("query%q documentmatches%d titlematches%d", q.Q, count, q.TitleMatches)
		}
	}
	// Use one connection: TEMP vocab belongs to the creating connection.
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(t.Context(), `CREATE VIRTUAL TABLE temp.f_realistic_vocab USING fts5vocab(main,'catalog_search_titles','row')`); err != nil {
		t.Fatal(err)
	}
	var library int
	if err = conn.QueryRowContext(t.Context(), `SELECT id FROM catalog_libraries WHERE library_id='movies'`).Scan(&library); err != nil {
		t.Fatal(err)
	}
	for _, term := range []string{"k1", fmt.Sprintf("l%d", library)} {
		var count int
		if err = conn.QueryRowContext(t.Context(), `SELECT doc FROM temp.f_realistic_vocab WHERE term=?`, term).Scan(&count); err != nil {
			t.Fatal(err)
		}
		corpus.KindDocuments[term] = count
	}
	if corpus.WordDocuments["the"] < corpus.Items/10 || corpus.WordDocuments["the"] > corpus.Items/2 || corpus.WordDocuments["extraordinary"] > max(20, corpus.Items/100) {
		t.Fatalf("unexpected corpus frequency spread: the%d extraordinary%d items%d", corpus.WordDocuments["the"], corpus.WordDocuments["extraordinary"], corpus.Items)
	}
}
