import test from 'node:test';
import assert from 'node:assert/strict';
import type {LibraryAdminJob, ManagedLibrary} from '../../../packages/client-core/src/library-management';
import type {ScanIssue} from '../../../packages/client-core/src/scan-issues';
import {defaultI18n} from '../../../packages/i18n/src';
import {ownerLibraryAudience, ownerLibraryCounts, ownerLibraryInterval, ownerLibraryKind, ownerLibraryScan, ownerLibraryState, ownerSkippedFiles, ownerIssueReason} from '../src-next/screens/settings-live/owner-libraries/presentation.ts';

const library = (patch: Partial<ManagedLibrary> = {}): ManagedLibrary => ({
  id: 'lib-tv', name: 'TV Shows', kind: 'tv', revision: 3, source: {path: '/srv/media/TV', availability: 'available', basis: 'probe'},
  lastScan: null, actions: ['rename', 'scan', 'remove'], ...patch,
} as ManagedLibrary);

const at = (iso: string, status: 'complete' | 'running' | 'paused' | 'failed' = 'complete'): LibraryAdminJob => ({id: `job-${status}-${iso}`, libraryId: 'lib-tv', status, processed: 10,
  createdAt: iso, updatedAt: null, finishedAt: status === 'running' || status === 'paused' ? null : iso, actions: []}) as LibraryAdminJob;

test('the drawn library state follows the server scan, never a guess', () => {
  const now = new Date('2026-10-24T18:00:00Z');
  assert.match(ownerLibraryState(library({lastScan: at('2026-10-24T15:12:00Z')}), {now, locale: 'en-US'}).text, /^Scanned today at /);
  // Midday, so the fixture is still yesterday in the device's own calendar at every runner TZ.
  assert.match(ownerLibraryState(library({lastScan: at('2026-10-23T12:00:00Z')}), {now, locale: 'en-US'}).text, /^Scanned yesterday at /);
  assert.match(ownerLibraryState(library({lastScan: at('2026-10-20T15:12:00Z')}), {now, locale: 'en-US'}).text, /^Scanned /);
  assert.equal(ownerLibraryState(library({lastScan: at('2026-10-24T15:12:00Z', 'running')}), {now, locale: 'en-US'}).live, true);
  assert.equal(ownerLibraryState(library({lastScan: null}), {now, locale: 'en-US'}).text, defaultI18n.t('next.ownerLibraries.neverScanned'));
  assert.equal(ownerLibraryState(library({source: {path: '/gone', availability: 'unavailable', basis: 'probe'}}), {now, locale: 'en-US'}).text, defaultI18n.t('web.libraries.sourceUnavailable'));
  assert.equal(ownerLibraryState(library({lastScan: at('2026-10-24T15:12:00Z')}), {now, locale: 'en-US', short: true}).text, defaultI18n.t('next.ownerLibraries.scannedTodayShort'));
});

test('only a live scan is offered for cancel, and its own state wins', () => {
  const now = new Date('2026-10-24T18:00:00Z');
  assert.equal(ownerLibraryScan(library({lastScan: at('2026-10-24T15:12:00Z', 'running')}))?.status, 'running');
  assert.equal(ownerLibraryScan(library({lastScan: at('2026-10-24T15:12:00Z', 'paused')}))?.status, 'paused');
  assert.equal(ownerLibraryScan(library({lastScan: at('2026-10-24T15:12:00Z')})), undefined);
  assert.equal(ownerLibraryState(library({lastScan: at('2026-10-24T15:12:00Z', 'paused')}), {now, locale: 'en-US'}).text, defaultI18n.t('next.ownerLibraries.paused'));
});

test('counts and audience come from the server summary', () => {
  const summary = {counts: [{kind: 'show', count: 24, atLeast: false}, {kind: 'episode', count: 96, atLeast: false}], coverUrls: ['/a.jpg'], latestArtwork: '', firstScan: false, empty: false, emptyKnown: true};
  assert.deepEqual(ownerLibraryCounts(library({summary})), [defaultI18n.t('lib.count', {count: 24, pivot: 'shows'}), defaultI18n.t('lib.count', {count: 96, pivot: 'episodes'})]);
  // The merged reading summary kinds use the existing counted nouns: ebooks are
  // books, comics take the catalogue's title/titles fallback, never audiobooks.
  const reading = {...summary, counts: [{kind: 'ebook', count: 2, atLeast: false}, {kind: 'comic', count: 1, atLeast: false}]};
  assert.deepEqual(ownerLibraryCounts(library({kind: 'book', summary: reading})),
    [defaultI18n.t('lib.count', {count: 2, pivot: 'books'}), defaultI18n.t('lib.count', {count: 1, pivot: 'titles'})]);
  assert.equal(ownerLibraryAudience(library({audience: {everyone: false, accounts: 5}})), defaultI18n.t('next.ownerLibraries.audience', {count: 5}));
  assert.equal(ownerLibraryAudience(library({audience: {everyone: true, accounts: 5}})), defaultI18n.t('next.ownerLibraries.audienceEveryone'));
  assert.equal(ownerLibraryAudience(library()), undefined);
});

test('the seven kinds keep their drawn names and glyphs', () => {
  assert.deepEqual(['movie', 'tv', 'anime', 'music', 'audiobook', 'book', 'comic'].map(kind => ownerLibraryKind(kind).kind), ['movie', 'tv', 'anime', 'music', 'audiobook', 'book', 'comic']);
  assert.equal(ownerLibraryKind('music').icon, 'i-music');
  assert.equal(ownerLibraryKind('unknown-kind').kind, 'movie');
});

test('folder intervals say what the server will do', () => {
  assert.equal(ownerLibraryInterval(3600), defaultI18n.t('next.ownerLibraries.interval.hour'));
  assert.equal(ownerLibraryInterval(900), defaultI18n.t('next.ownerLibraries.interval.minutes', {count: 15}));
  assert.equal(ownerLibraryInterval(0), defaultI18n.t('next.ownerLibraries.interval.never'));
  assert.equal(ownerLibraryInterval(7200), defaultI18n.t('next.ownerLibraries.interval.hours', {count: 2}));
});

test('files skipped lists only files Portico cannot play, with the server reason', () => {
  const issues: ScanIssue[] = [
    {sequence: 1, line: 1, path: '/tv/a.mkv', reason: 'damaged', category: 'damaged', messageKey: 'scan.issue.damaged', warning: false},
    {sequence: 2, line: 2, path: '/tv/.hidden', reason: 'ignored', messageKey: 'scan.issue.ignored_name', warning: true},
    {sequence: 3, line: 3, path: '/tv/b.iso', reason: 'unsupported', category: 'unsupported', messageKey: 'scan.issue.unsupported', warning: false},
  ];
  assert.deepEqual(ownerSkippedFiles(issues).map(issue => issue.sequence), [1, 3]);
  assert.equal(ownerIssueReason(issues[0]!), defaultI18n.t('scan.issue.damaged'));
  assert.equal(ownerIssueReason({...issues[0]!, messageKey: 'scan.issue.unknown_key'} as ScanIssue), 'damaged');
});
