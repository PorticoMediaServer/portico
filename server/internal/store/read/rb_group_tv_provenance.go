package read

// Show-workspace default resolution remains blocked on one exact source fact:
// whether an owner show-settings row exists. Legacy readShowSettings inherits
// library_preferences.episode_numbering only when catalog_show_settings has no
// row; an explicitly saved all-false row suppresses that inheritance. The merged
// schema has entity hide_seasons/absolute_numbering/newest_first defaults of0 and
// show_settings_revision default1, with no presence/source discriminator. Library
// preference_present/preference_episode_numbering IS representable, but does not
// distinguish the two show states. A typed harness flag would invent provenance.
// Owner decision: preserve an explicit show-settings presence fact, or approve
// changed inheritance semantics. No DDL change is drafted/adopted by this helper.
//
// RBTVGroupExact is deliberately the separate explicit browse groupOrder
// contract (browse_group_scope.go showEpisodeOrder), not workspace defaults,
// next-up selection, range ranking, availability projection, or public-ID-tied
// workspace episode pagination. It reads and fences actual current source
// settings values/revision without resolving absent/explicit settings. It returns
// complete direct membership only within256 raw positions and16 season points,
// with a256KiB retained-value/fence allowance. Restriction policies refuse; large
// preparation/cache, lifting, inherited display and workspace semantics stay with
// root. Frozen expected tuples are recorded in the native test; no legacy test
// dependency or legacy runtime oracle is permitted.
