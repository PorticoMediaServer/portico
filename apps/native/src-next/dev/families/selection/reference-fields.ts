import {defaultI18n, formatMessage} from '../../../../../../packages/i18n/src/index';
import {selectionNativeFixtures as fixture} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/selection-native-fixtures';
import {selectionFixtures as sharedFixture} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/selection-fixtures';
import {browseNativeFixtures} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/browse-native-fixtures';
import type {RepairFieldSpec} from '../../../../../../packages/client-core/src/metadata-repair';
import type {BulkFieldValue} from '../../../kit/selection/types';

// Complete, explicit reference distributions. This module is DEV-only.
const shared = (value: string, count: number, entries: BulkFieldValue['entries'] = []): BulkFieldValue => ({state: 'shared', distinctCount: 1, truncated: false, value, values: [{value, count}], entries});
export function referenceFields(kind: 'movie' | 'episode' | 'song' = 'movie'): Readonly<Record<string, BulkFieldValue>> {
  if (kind === 'episode') return {
    // TV-MA has no existing DEV catalogue entry. Keep that observation absent;
    // a genuine controller supplies it rather than substituting a movie rating.
    network: shared(fixture.values.network, 3), year: shared(String(2023), 3),
    metadataLanguage: {state: 'mixed', distinctCount: 2, truncated: false, values: [{value: fixture.values.language, count: 2}, {value: formatMessage(sharedFixture['next.selection.fixture.french']), count: 1}], entries: []},
    tags: shared('[]', 3), labels: shared('[]', 3),
  };
  if (kind === 'song') {
    const genres = [formatMessage(sharedFixture['next.selection.fixture.rnb']), formatMessage(sharedFixture['next.selection.fixture.pop'])];
    return {
      year: {state: 'mixed', distinctCount: 2, truncated: false, values: [{value: String(2019), count: 1}, {value: String(2020), count: 3}], entries: []},
      metadataLanguage: shared(fixture.values.language, 4), genres: shared(JSON.stringify(genres), 4, genres.map(value => ({value, count: 4}))),
      tags: shared('[]', 4), labels: shared('[]', 4),
    };
  }
  const commonGenres = fixture.genres.slice(0, 2), allGenres = [...fixture.genres];
  return {
    contentRating: {state: 'mixed', distinctCount: 2, truncated: false, values: [{value: fixture.values.rating, count: 3}, {value: formatMessage(browseNativeFixtures['rating.pg']), count: 1}], entries: []},
    studio: shared(fixture.values.studio, 4), country: shared(fixture.values.country, 4),
    year: {state: 'mixed', distinctCount: 4, truncated: false, values: [1995, 1999, 2010, 2026].map(value => ({value: String(value), count: 1})), entries: []},
    edition: shared('', 4), metadataLanguage: shared(fixture.values.language, 4), network: shared(fixture.values.network, 4),
    genres: {state: 'mixed', distinctCount: 2, truncated: false, values: [{value: JSON.stringify(allGenres), count: 3}, {value: JSON.stringify(commonGenres), count: 1}], entries: fixture.genres.map((value, index) => ({value, count: index < 2 ? 4 : 3}))},
    tags: {state: 'mixed', distinctCount: 2, truncated: false, values: [{value: JSON.stringify([fixture.tags[0]]), count: 3}, {value: '[]', count: 1}], entries: [{value: fixture.tags[0], count: 3}]},
    labels: shared('[]', 4),
  };
}

/** Exact non-bulk registries from metadata/repair_schema.go for these reference kinds. */
export function referenceSingleItemFields(kind: 'movie' | 'episode' | 'song'): readonly RepairFieldSpec[] {
  const t = defaultI18n.t;
  const title: RepairFieldSpec = {field: 'title', label: t('web.metadata.field.title'), group: 'general', type: 'text', maxLength: 300, bulk: false};
  const sort: RepairFieldSpec = {field: 'sortTitle', label: t('web.metadata.field.sortTitle'), group: 'general', type: 'text', maxLength: 300, bulk: false};
  const description: RepairFieldSpec = {field: 'description', label: t('web.saved.summary'), group: 'text', type: 'multiline', maxLength: 20000, bulk: false};
  if (kind === 'song') return [title, sort, description,
    {field: 'trackNumber', label: t('web.metadata.field.trackNumber'), group: 'numbers', type: 'integer', min: 0, max: 9999, bulk: false},
    {field: 'discNumber', label: t('web.metadata.field.discNumber'), group: 'numbers', type: 'integer', min: 0, max: 999, bulk: false}];
  if (kind === 'episode') return [title, sort, description,
    {field: 'releaseDate', label: formatMessage(sharedFixture['next.selection.fixture.airDate']), group: 'general', type: 'date', maxLength: 10, bulk: false},
    {field: 'seasonNumber', label: t('web.metadata.field.seasonNumber'), group: 'numbers', type: 'integer', min: 0, max: 9999, bulk: false},
    {field: 'episodeNumber', label: t('web.metadata.field.episodeNumber'), group: 'numbers', type: 'integer', min: 1, max: 9999, bulk: false}];
  return [title, sort,
    {field: 'originalTitle', label: t('web.metadata.field.originalTitle'), group: 'general', type: 'text', maxLength: 300, bulk: false},
    {field: 'tagline', label: t('web.metadata.field.tagline'), group: 'text', type: 'text', maxLength: 300, bulk: false}, description,
    {field: 'releaseDate', label: t('web.metadata.field.releaseDate'), group: 'general', type: 'date', maxLength: 10, bulk: false}];
}
