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

test('A23/A24: the phone count row has no Save view link, and the divider after Filters only draws when something follows', () => {
  const toolbar = read('../src/kit/browse/BrowseToolbar.tsx');
  assert.match(toolbar, /<div className=\{styles\.phoneCount\}><ResultCount [^>]*>\{resultCount\}<\/ResultCount>\{\/\* phone-app 5: Save this view lives in the ⋯ menu\. \*\/\}<span className=\{styles\.phoneView\}>/);
  assert.match(toolbar, /\{\(sortInFirstRow \|\| actions != null && actions !== false \|\| quick\.length > 0\) && <span className=\{styles\.separator\} aria-hidden="true"\/>\}/);
});
