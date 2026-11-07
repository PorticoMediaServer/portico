package writer

// Intent from the pinned owner handoff, never observed native facts, a Capture
// receipt or an execution pin. A recipe does not open its nil runner binding.
type canonicalReadRecipe struct {
	SourceHead    string   `json:"owner_source_head"`
	Route         string   `json:"route"`
	Population    string   `json:"required_population"`
	Cohorts       []string `json:"required_cohorts"`
	Contracts     []string `json:"required_contracts"`
	StatementDebt string   `json:"statement_debt"`
	Qualification bool     `json:"qualification"`
}

func canonicalReadRecipeFor(class string) *canonicalReadRecipe {
	var r canonicalReadRecipe
	r.Population = "QL canonical1M clone/exact same t/library7; trusted source fixtures must extend that parent"
	switch class {
	case "rb.numeric_advance", "rb.numeric_first_page", "rb.numeric_cached_page":
		r.SourceHead = "d3c788c173be46b106bbddb843a418e51ea13e95"
		r.Cohorts = []string{"year/decade", "sparse/zero/out-of-domain values", "foreground interruption/source drift", "unsupported restricted/custom/age/label policy refusal"}
		r.Contracts = []string{"full1024 raw quantum remains pending until separate exhaustion probe", "original F04 lifetime/deadline survives attachments; no lease renewal", "actual preparation bytes/cache fence and retained charges; pending/refusal/error stay raw", "full route/ConsumeForeground; distinct preparation, lookup, publication and joined-consumer errors/times"}
		switch class {
		case "rb.numeric_advance":
			r.Route = "ExactFacetReader.BeginNumericFacets -> NumericFacetPreparation.Advance (cold1024raw)"
		case "rb.numeric_first_page":
			r.Route = "NumericFacetPreparation.Page (first ready handoff/fresh LIVE)"
			r.Contracts = append(r.Contracts, "no Page before actual exhausted preparation; first authorized publication releases conflict lease, ready alone does not")
		case "rb.numeric_cached_page":
			r.Route = "NumericFacetPreparation.Page (cached continuation/fresh LIVE)"
			r.Contracts = append(r.Contracts, "cached Page reauthorizes complete fence/token horizon at original absolute deadline; independent output copy<=200values")
		}
		r.StatementDebt = "B-R budgets and #1357; include every advance/exhaustion probe and first/final authorization, not only final Page"
	case "rb.group_window":
		r.SourceHead = "0748faa2c779576892feac15f90b6701c54fe171"
		r.Route = "BrowseReader.RBGroupWindow /RBGroupDefinitions /ConsumeForeground"
		r.Cohorts = []string{"all ten families/both directions/first+far continuation", "sparse/NULL playlist occurrences/default disc-track-part/ties", "source reparent/rename/revocation", "unsupported age/label/custom/shared/restricted policy refusals"}
		r.Contracts = []string{"64raw/40output; full or empty-nonexhausted window retains raw cursor", "SourceOrdered=false is candidate-only, never a successful ready groupOrder page", "actual canonical source/subject and full initial/final publication fence", "native lookup+full route+joined consumer timing and retained bytes"}
		r.StatementDebt = "13..15routeSQL exceeds proposed B-R04 10; #1357 remains open"
	case "rb.relationship_exact":
		r.SourceHead = "0748faa2c779576892feac15f90b6701c54fe171"
		r.Route = "BrowseReader.RBRelationshipExact /RBGroupDefinitions /ConsumeForeground"
		r.Cohorts = []string{"complete small sets/256boundary/257raw refusal incl ineligible", "both directions/equal+prefix parent keys/merged entity-ID ties", "independent windows with cumulative retained-byte pressure", "unsupported age/label/custom/shared/restricted policy refusals"}
		r.Contracts = []string{"at most four64row quanta+one-row exhaustion probe", "cap+1 returns ZERO payload/ErrNotReady; no large-exact/top-K claim", "sort only after actual complete source exhaustion; merge parents rather than concatenate", "actual retained key/fence/vector/copy charges plus complete lookup/publication/consumer route"}
		r.StatementDebt = "B-R04 and #1357; all source quanta/parent points/shared fences count"
	case "rb.resolution":
		r.SourceHead = "aa957a0b6b6c6405f1a04435aa2e8ab563f49b3b"
		r.Route = "BrowseReader.ResolutionWindow /ConsumeForeground"
		r.Cohorts = []string{"movie/show/season/episode/collection/both directions", "NULL+explicit-zero ID ties/positive+negative keys/far ties", "sparse eligibility/empty-full-hidden quantum/raw continuation", "unsupported age/label/custom/shared/restricted policy refusals"}
		r.Contracts = []string{"128fetched/64consumed/40output; charge both lookahead streams", "NULL/zero interleave by ID with same zero-key ID continuation bound", "exhaustion only after both streams end and all fetched rows consumed", "original LIVE deadline and fresh first/final publication plus joined consumer"}
		r.StatementDebt = "12..13routeSQL exceeds proposed10; #1357 remains open"
	case "rs.listening":
		r.SourceHead = "9b3cb335877cd9f3d5c1910fcc2b7129ad76c2ab"
		r.Route = "NewRSChildrenReader().Window /ConsumeForeground"
		r.Cohorts = []string{"album/audiobook", "first/deep/sparse/repeated/import-pressure", "512raw boundary/overflow refusal/NOCASE four-key ties"}
		r.Contracts = []string{"full native lookup+complete-window Go sort+fresh publication and joined consumer", "actual retained charge128B/raw+2actualkeybytes; no fixed forecast relabelled observed", "raw512 bound; >512 is RS-001 post-lock indexed stream, no prelock gate", "original ready deadline and separate lookup/sort/publication/consumer errors and nullable facts"}
		r.StatementDebt = "13statements exceeds B-R10 target12; local29ms and oracle race are not qualification"
	default:
		return nil
	}
	return &r
}
