#!/usr/bin/env python3
"""Reviewed population content compatibility; never a qualification verdict.

A class consumer accepts a downloaded population only when its recorded content
is the exact content the consumer expects: the shape version (which names the
kinds and their counts), the resident count, the recorded logical digest of the
logical rows and the exact schema SHA-256. Test-source bytes are not compared,
so an edit that leaves population content unchanged stays compatible, while a
real content change, a relabelled shape or a different schema refuses with a
named reason.

The reviewed identity below is a content address, not an image or source digest:
the manifest's own `database_sha256` binds the image bytes to these facts. A
shape that has no reviewed entry (for example the search shape until its paired
producer artifact is attached) is refused rather than guessed.
"""
import re

SHA = re.compile(r'^[0-9a-f]{64}$')

CANONICAL_MOVIES = 'canonical-movies-v1'
CANONICAL_MOVIES_SEARCH = 'canonical-movies-v1-search'

# Exact reviewed content per shape. `counts` is the complete facts.counts map;
# an added, removed or changed table/kind count is a different shape.
REVIEWED = {
    CANONICAL_MOVIES: {
        'seed': 0,
        'residents': 1000000,
        'counts': {
            'asset_streams': 0,
            'catalog_age_facet_counts': 0,
            'catalog_age_item_counts': 1,
            'catalog_aggregate_publications': 0,
            'catalog_asset_contributions': 1000000,
            'catalog_asset_links': 1000000,
            'catalog_assets': 1000000,
            'catalog_entities': 1000000,
            'catalog_entity_terms': 0,
            'catalog_identities': 0,
            'catalog_info_changes': 0,
            'catalog_kinds': 1,
            'catalog_libraries': 1,
            'catalog_membership_clock': 1,
            'catalog_scalar_contributions': 1000000,
            'catalog_subjects': 1000000,
            'current_movies': 1000000,
            'enrichment_command_receipts': 0,
            'inventory_objects': 0,
            'inventory_runs': 0,
            'libraries': 1,
            'metadata_evidence': 0,
            'metadata_publications': 0,
            'scan_command_receipts': 0,
            'search_image_seals': 0,
            'search_publish_jobs': 0,
            'search_publish_receipts': 0,
        },
        'logical_sha256': 'eb1b1a877268c5f57744c153161bc2700dedc2ed2d5e0acc11018d31cc89ac1d',
    },
}


def _refuse(message):
    raise ValueError(message)


def require_content(manifest, *, schema):
    """Return the reviewed content identity of a validated population manifest.

    `manifest` is the parsed `population.json`; `schema` is the consumer's
    embedded schema SHA-256. Every mismatch refuses with a named reason.
    """
    if type(manifest) is not dict or type(manifest.get('schema_sha256')) is not str:
        _refuse('population content record invalid: manifest/schema missing')
    image_schema = manifest['schema_sha256']
    if not SHA.fullmatch(schema):
        _refuse('consumer schema SHA-256 invalid')
    if image_schema != schema:
        _refuse(f'population schema mismatch: image {image_schema} consumer {schema}')
    facts = manifest.get('facts')
    if type(facts) is not dict:
        _refuse('population content record invalid: facts missing')
    shape = facts.get('version')
    expected = REVIEWED.get(shape) if isinstance(shape, str) else None
    if expected is None:
        _refuse(f'population shape has no reviewed content identity: {shape!r}')
    if type(facts.get('seed')) is not int or facts['seed'] != expected['seed']:
        _refuse(f'population seed mismatch: image {facts.get("seed")!r} expected {expected["seed"]}')
    if type(facts.get('residents')) is not int or facts['residents'] != expected['residents']:
        _refuse(f'population residents mismatch: image {facts.get("residents")!r} expected {expected["residents"]}')
    counts = facts.get('counts')
    if type(counts) is not dict:
        _refuse('population shape counts missing')
    for name, count in expected['counts'].items():
        if type(counts.get(name)) is not int or counts[name] != count:
            _refuse(f'population shape counts mismatch: {name}={counts.get(name)!r} expected {count}')
    for name in sorted(set(counts) - set(expected['counts'])):
        _refuse(f'population shape counts mismatch: unexpected table {name}')
    digest = facts.get('logical_sha256')
    if not isinstance(digest, str) or not SHA.fullmatch(digest) or digest != expected['logical_sha256']:
        _refuse(f'population logical digest mismatch: image {digest!r} expected {expected["logical_sha256"]}')
    return {'version': shape, 'seed': expected['seed'], 'residents': expected['residents'],
            'counts': dict(expected['counts']), 'logical_sha256': expected['logical_sha256'],
            'schema_sha256': schema}
