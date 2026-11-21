import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {defaultI18n} from '@i18n';
import {moreFieldsSummary} from '../src/kit/browse/FilterBuilder.tsx';

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');
const t = defaultI18n.t;

test('A6: Show all names the field in the plural (desktop-browse #filters: "Show all 13 genres")', () => {
  assert.equal(t('next.browse.showAllValues', {count: 13, field: 'genre', name: 'genre'}), 'Show all 13 genres');
  assert.equal(t('next.browse.showAllValues', {count: 40, field: 'country', name: 'country'}), 'Show all 40 countries');
  assert.equal(t('next.browse.showAllValues', {count: 4, field: 'mystery', name: 'edition'}), 'Show all 4 edition');
  assert.doesNotMatch(read('../src/kit/browse/FilterBuilder.tsx'), /t\('next\.browse\.showAll',/);
});

test('A22: the More row is a plain list, with the rating fields named once as "ratings"', () => {
  const fields = ['writer', 'tag', 'label', 'audienceRating', 'criticRating', 'releaseDate'].map(id => ({id}));
  const names: Record<string, string> = {writer: 'Writer', tag: 'Tag', label: 'Label', audienceRating: 'Audience rating', criticRating: 'Critic rating', releaseDate: 'Release date'};
  assert.equal(moreFieldsSummary(fields, field => names[field.id]!), 'Writer, Tag, Label, ratings, Release date');
  // A single rating keeps its own name.
  assert.equal(moreFieldsSummary([{id: 'writer'}, {id: 'criticRating'}], field => names[field.id]!), 'Writer, Critic rating');
  assert.equal(defaultI18n.list(['A', 'B', 'C'], 'unit'), 'A, B, C');
});

test('A21: a collection card\'s years stay grey; only the watched share is blue (desktop-browse #collections)', () => {
  const css = read('../src/kit/cards/cards.module.css');
  assert.doesNotMatch(css, /^\.collectionDetail \{ color:var\(--color-accent-strong\)/m);
  assert.match(css, /\.collectionDetail > \[data-fact='watched'\] \{ color:var\(--color-accent-strong\); font-weight:700; \}/);
});

test('A23/A24 (owner, 18 Nov): at phone width the toolbar is one row with no count line and no Save view; the divider after Filters only draws when something follows', () => {
  const toolbar = read('../src/kit/browse/BrowseToolbar.tsx');
  assert.match(toolbar, /<div className=\{styles\.toolbarMain\}>\{filters\}\{sort\}\n\s*<ChipRail>/);
  assert.doesNotMatch(toolbar, /phoneCount/);
  assert.match(toolbar, /\{filters\}\{sort\}\{platform === 'tv' && onJump/, 'wide and TV: Sort sits directly after Filters (owner, 18 Nov)');
  // ... and only when something stands before it (See all has no Filters or sort: no divider starts its row).
  assert.match(toolbar, /\{\(filters \|\| sort \|\| platform === 'tv' && onJump\) && \(actions != null && actions !== false \|\| quick\.length > 0\) && <span className=\{styles\.separator\} aria-hidden="true"\/>\}/);
});

test('SMALL-FAULTS 29: the chip rail never draws a partly visible chip: it fades out from the end of the last whole chip', () => {
  const css = read('../src/kit/browse/browse.module.css'), toolbar = read('../src/kit/browse/BrowseToolbar.tsx');
  assert.doesNotMatch(css, /\.chipRail \{[^}]*mask-image/, 'no fixed end fade that a wide partial chip shows through');
  assert.match(css, /\.chipRail\[data-cut\] \{[^}]*transparent var\(--rail-cut\)/, 'past the last whole chip the rail is transparent');
  assert.match(toolbar, /ends\.filter\(end => end <= view \+ \.5\)\.pop\(\)/, 'the cut is the end of the last chip that fits whole');
});

test('the chip rail is drawn only with room for its first chip whole: with less it is hidden, not scrollable and not read (lead, 19 Nov)', () => {
  const css = read('../src/kit/browse/browse.module.css'), toolbar = read('../src/kit/browse/BrowseToolbar.tsx');
  assert.match(css, /\.chipRail\[data-no-room\] \{ visibility: hidden; overflow: hidden; pointer-events: none; \}/);
  assert.match(toolbar, /setRoom\(!items\[0\] \|\| first <= view \+ \.5\)/, 'room is the first chip fitting whole');
  assert.match(toolbar, /aria-hidden=\{room \? undefined : true\}/);
});
