package read

// Release-role ordering is not inferred from existence of immutable evidence.
// The canonical accepted head is metadata_subject_providers' exact entity,
// provider,role selection with accepted_publication_id/evidence_id and revision.
// metadata_evidence.primary_type and metadata_evidence_terms can carry release
// facts, but the current owned sources do not establish the MusicBrainz release
// role key and secondary-type vocabulary used by accepted album publication.
// Owner must identify that accepted selection/vocabulary before Album/EP/Single/
// Compilation precedence can be claimed. A caller-supplied type is no substitute.
// RBArtistSource therefore hydrates accepted catalogue album/year/primary-artist
// relationships from primary albums and credited-song edges, with current fences
// and explicit incomplete tails. These separate feeds must be deduplicated/merged
// and their accepted role facts resolved before public release order. No final
// order/count, hidden-child aggregate, or large exact qualification is asserted.
