package read

// Show-workspace default resolution now has its exact source fact:
// catalog_entities.show_settings_present. Zero inherits the CURRENT library
// episode numbering even above settings revision one; one is an explicit owner
// save and suppresses inheritance even when every saved field is default.
// Library preference_present/preference_episode_numbering remains a separate
// source fact. RBTVSourceDefaults reads and fences both; a typed harness flag
// would still invent provenance. No DDL change is drafted/adopted here.
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
