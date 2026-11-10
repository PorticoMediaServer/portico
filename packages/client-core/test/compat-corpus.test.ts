// O-159 compatibility corpus. Real, scrubbed responses recorded from past server releases
// (test/compat-corpus/<release>/, written by scripts/compat-corpus/record.mjs) are fed to the
// current client parsers. A parser that throws on an older release's answer is a field added since
// that release which the parser now requires.
//
// The unsupported pre-release 648 stand-in retains its historical gap notes.
// Every current recording and parser is required; current gaps cannot be TODOs.
import test from 'node:test';
import assert from 'node:assert/strict';
import {existsSync, readdirSync, readFileSync} from 'node:fs';
import {dirname, join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {findCredentials} from '../../../scripts/compat-corpus/scrub.mjs';
import {validateHomeDocument} from '../src/home.ts';
import {parseLibraryList} from '../src/library-list.ts';
import {parseBrowseCapabilities, parseBrowseResult} from '../src/browse.ts';
import {parseMediaItem} from '../src/media-item.ts';
import {DetailService} from '../src/detail.ts';
import {PlayerOffersService} from '../src/player-offers.ts';
import {parseEntry, parseEvent, parsePage, parseQueue} from '../src/playback-v1/types.ts';
import {
  parseFileInfoFilesPage, parseFileInfoGroupsPage, parseFileInfoHeadTracksPage, parseFileInfoMembersPage, parseFileInfoPreparing,
  parseFileInfoSourcesPage, parseFileInfoTracksPage, type FileInfoOpening,
} from '../src/file-info-pages.ts';
import {withQueries} from './fixtures/viewer-queries.ts';

const root = process.env.PORTICO_COMPAT_CORPUS_ROOT ?? join(dirname(fileURLToPath(import.meta.url)), 'compat-corpus');
const recorded = readdirSync(root, {withFileTypes: true}).filter(entry => entry.isDirectory()).map(entry => entry.name).sort();
/** R-09b: 'main-1.1' is a newer server of the same major. Every object in every recorded main
 * response, at every depth, gains a member this client has never seen. A 1.0 client must read it
 * exactly as it reads main (O-159: additive only within a major). */
const NEWER = 'main-1.1';
const releases = recorded.includes('main') ? [...recorded, NEWER] : recorded;
const newer = (value: unknown): unknown => Array.isArray(value) ? value.map(newer)
  : value !== null && typeof value === 'object' ? {...Object.fromEntries(Object.entries(value).map(([key, entry]) => [key, newer(entry)])), addedInNewerRelease: {since: '1.1'}}
  : value;

type Recorded = Readonly<{request: {method: string; path: string; body?: unknown}; status: number; body: any}>;
const read = (release: string, name: string): Recorded | undefined => {
  if (release === NEWER) {
    const base = read('main', name);
    return base && {...base, body: newer(base.body)};
  }
  const file = join(root, release, `${name}.json`);
  return existsSync(file) ? JSON.parse(readFileSync(file, 'utf8')) as Recorded : undefined;
};

/**
 * Parses that currently throw on a recorded response from an older release, triaged for the lead:
 * `<release>/<case>` -> the field and where the current parser requires it. Each one runs as a
 * `todo` so the gap stays visible without failing the suite; when a parser is made tolerant the
 * todo starts passing and the entry should be removed.
 */
const KNOWN_THROWS: Readonly<Record<string, string>> = {
  // 648 answers get_items_id_file_info in its pre-P2-29 flat shape. parseFileInfoFilesPage requires, in order:
  '648/file-info-files: parseFileInfoFilesPage': 'src/file-info-pages.ts: kind === "file-info-files" (:96; 648 sends the item kind, "song"), detail {scope, revision} (:96/:90; 648 has a top-level revision), basis (:96), window {queryId, limit, returned} (:75-76), surface {fileCount, eligibleFileCount, groupCount, canSplit, hasDiscColumns} (:109-111; 648 has a top-level manage), itemKind (:196), and per file: occurrence (:150), audioCount and subtitleCount (:151; 648 has audio and subtitles arrays)',

};

function check(release: string, name: string, run: (recorded: Recorded) => void | Promise<void>, options: {file?: string} = {}) {
  const recorded = read(release, options.file ?? name);
  // The newer-server release is main plus additions, so main's known gaps are its own.
  const known = KNOWN_THROWS[`${release}/${name}`] ?? (release === NEWER ? KNOWN_THROWS[`main/${name}`] : undefined);
  if (!recorded) {
    if (release === 'main' || release === NEWER) return test(`${release}: ${name} required recording`, () => assert.fail('current producer response missing; re-record the corpus'));
    return test(`${release}: ${name} (not recorded)`, {skip: 'this unsupported prerelease was recorded without it'}, () => {});
  }
  // An older release lacks the route: noted, and what the client does about it is another test's job.
  if (release === '648' && (recorded.status === 404 || recorded.status === 400)) {
    return test(`${release}: ${name} (release lacks this route: ${recorded.status})`, () => assert.ok(recorded.status === 404 || recorded.status === 400));
  }
  return test(`${release}: ${name} parses`, known ? {todo: known} : {}, async () => {
    assert.ok(recorded.status >= 200 && recorded.status < 300, `${name} was recorded with status ${recorded.status}`);
    await run(recorded);
  });
}

for (const release of releases) {
  const home = read(release, 'home')?.body;
  const manifest = JSON.parse(readFileSync(join(root, release === NEWER ? 'main' : release, 'manifest.json'), 'utf8')) as {library: {id: string}; itemId: string};
  const serverId: string = home?.serverId ?? read(release, 'item-detail')?.body?.scope?.serverId ?? 'srv';
  const scope = {serverId, viewerId: 'corpus-viewer'};
  const target = {libraryId: manifest.library.id, itemId: manifest.itemId};
  /** An api that answers one path from the corpus and refuses any other. */
  const apiFor = (answers: Readonly<Record<string, unknown>>) => ({
    async request<T>(path: string): Promise<T> {
      const key = path.split('?')[0];
      if (!(key in answers)) throw new Error(`corpus has no answer for ${key}`);
      return answers[key] as T;
    },
  });
  const itemPath = (suffix = '') => `/v1/items/${target.itemId}${suffix}`;

  // The recorded `/v1/system` answers are the old server's: sign-in now reads the rebuilt server's.
  check(release, 'home: validateHomeDocument', ({body}) => { validateHomeDocument(body); }, {file: 'home'});
  check(release, 'libraries: parseLibraryList', ({body}) => { assert.ok(parseLibraryList(body).items.length > 0); }, {file: 'libraries'});
  check(release, 'browse-capabilities: parseBrowseCapabilities', ({body}) => { parseBrowseCapabilities(body); }, {file: 'browse-capabilities'});
  check(release, 'browse-albums: parseBrowseResult', ({body}) => { assert.ok(parseBrowseResult(body).entries.length > 0); }, {file: 'browse-albums'});
  check(release, 'browse-songs: parseBrowseResult', ({body}) => { assert.ok(parseBrowseResult(body).entries.length > 0); }, {file: 'browse-songs'});
  check(release, 'item: parseMediaItem', ({body}) => { assert.equal(parseMediaItem(body).id, target.itemId); }, {file: 'item'});
  check(release, 'item-detail: DetailService', async ({body}) => {
    const service = new DetailService(withQueries({api: apiFor({[itemPath('/detail')]: body}), scope, requestId: async () => '00000000-0000-4000-8000-000000000000'}));
    await service.select(target, {related: 'none'});
    const snapshot = service.getSnapshot();
    service.dispose?.();
    assert.equal(snapshot.phase, 'ready', `detail did not parse: ${JSON.stringify(snapshot.error)}`);
  }, {file: 'item-detail'});
  check(release, 'player-offers: PlayerOffersService', async ({body}) => {
    const service = new PlayerOffersService({api: apiFor({[itemPath('/playback-offers')]: body}), scope});
    await service.select(target);
    const snapshot = service.getSnapshot();
    service.dispose();
    assert.equal(snapshot.phase, 'ready', `offers did not parse: ${JSON.stringify(snapshot.error)}`);
  }, {file: 'player-offers'});
  check(release, 'queue-create: parseQueue + parseEntry', ({body}) => {
    const queue = parseQueue(body.queue);
    assert.ok(queue.id);
    for (const entry of body.window) parseEntry(entry);
  }, {file: 'queue-create'});
  check(release, 'queue-window: parsePage(parseEntry)', ({body}) => { assert.ok(parsePage(body, parseEntry).items.length > 0); }, {file: 'queue-window'});
  check(release, 'events: event page + parseEvent', ({body}) => {
    // The page checks the poller makes (playback-v1/events.ts) before it hands each event to parseEvent.
    assert.ok(Array.isArray(body.events) && body.events.length <= 100 && typeof body.nextAfter === 'string' && body.nextAfter.length <= 128);
    for (const event of body.events) parseEvent(event);
  }, {file: 'events'});

  // File info. The opening is the detail's own token; the scope and revision are the detail's.
  const detail = read(release, 'item-detail')?.body;
  const opening: FileInfoOpening = {
    token: detail?.fileInfoOpening ?? 'none', scope: {serverId, viewerId: scope.viewerId}, target,
    revision: {catalog: detail?.revision?.catalog ?? 0, viewer: detail?.revision?.viewer ?? 0},
  };
  check(release, 'file-info-files: parseFileInfoFilesPage', ({body}) => { parseFileInfoFilesPage(body, opening); }, {file: 'file-info-files'});
  check(release, 'file-info-files-preparing: parseFileInfoPreparing', ({status, body}) => { assert.equal(status, 202); parseFileInfoPreparing(body, opening); }, {file: 'file-info-files-preparing'});
  check(release, 'file-info-groups: parseFileInfoGroupsPage', ({body}) => { parseFileInfoGroupsPage(body, opening); }, {file: 'file-info-groups'});
  check(release, 'file-info-sources: parseFileInfoSourcesPage', ({body}) => { parseFileInfoSourcesPage(body, opening); }, {file: 'file-info-sources'});
  const groups = read(release, 'file-info-groups')?.body, head = groups?.groups?.[0];
  if (!head && (release === 'main' || release === NEWER)) test(`${release}: required file-info group`, () => assert.fail('record a source with a real file-info group'));
  if (head) {
    check(release, 'file-info-members: parseFileInfoMembersPage', ({body}) => { parseFileInfoMembersPage(body, opening, head.id, true); }, {file: 'file-info-members'});
    check(release, 'file-info-tracks-audio: parseFileInfoTracksPage', ({body}) => { parseFileInfoTracksPage(body, opening, 'audio', head.headOccurrence); }, {file: 'file-info-tracks-audio'});
    check(release, 'file-info-tracks-subtitles: parseFileInfoTracksPage', ({body}) => { parseFileInfoTracksPage(body, opening, 'subtitles', head.headOccurrence); }, {file: 'file-info-tracks-subtitles'});
    check(release, 'file-info-head-tracks-audio: parseFileInfoHeadTracksPage', ({body}) => { parseFileInfoHeadTracksPage(body, opening, 'audio', head.id); }, {file: 'file-info-head-tracks-audio'});
  }
}

// CI guard: nothing shaped like a credential may sit in the corpus. The allowlist is
// IDENTITY_KEY / KEEP_KEYS in scripts/compat-corpus/scrub.mjs; keep it small.
test('compat corpus: no file contains anything shaped like a credential', () => {
  const problems: string[] = [];
  for (const release of recorded) {
    for (const file of readdirSync(join(root, release))) {
      const text = readFileSync(join(root, release, file), 'utf8');
      for (const [label, pattern] of [['JWT', /eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{4,}/], ['Bearer', /Bearer /], ['PEM', /-----BEGIN/], ['/home/ path', /\/home\//]] as const) {
        if (pattern.test(text)) problems.push(`${release}/${file}: ${label}`);
      }
      if (file.endsWith('.json')) for (const found of findCredentials(JSON.parse(text))) problems.push(`${release}/${file}: ${found.reason} at ${found.path}`);
    }
  }
  assert.deepEqual(problems, []);
});

test('compat corpus: the guard catches what it is for', () => {
  const planted = {session: {accessToken: 'abc'}, header: 'Bearer abcdef', jwt: 'eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig', path: '/home/justin/media', blob: 'Zm9vYmFyYmF6cXV4Zm9vYmFyYmF6cXV4Zm9vYmFy', pem: '-----BEGIN KEY-----'};
  const reasons = findCredentials(planted).map(found => found.reason).sort();
  assert.deepEqual(reasons, ['bearer', 'home path', 'jwt', 'long run', 'pem', 'unscrubbed secret field']);
  // Identity fields and the placeholder pass.
  assert.deepEqual(findCredentials({viewerFence: 'a'.repeat(64), accessToken: '<scrubbed>', nextCursor: 'b'.repeat(80)}), []);
});

test('compat corpus: a current producer is recorded', () => assert.ok(releases.includes('main')));


// These are current producer refusals/recovery, recorded over HTTP rather than
// assembled from schemas. Their exact status, retry class and resync semantics
// are release evidence alongside successful presentation documents.
for (const release of releases.filter(r => r !== '648')) {
  for (const [name, status, code] of [['mutation-missing-revision', 428, 'revision_required'], ['mutation-stale-revision', 412, 'revision_mismatch']] as const) {
    test(`${release}: ${name} preserves status and recovery`, () => {
      const response = read(release, name);
      assert.ok(response, 'required mutation recording');
      assert.equal(response.status, status);
      assert.equal(response.body.error.code, code);
      assert.equal(response.body.error.retry, 'after_refresh');
      assert.equal(typeof response.body.error.message, 'string');
      assert.equal(typeof response.body.error.requestId, 'string');
      if (status === 412) assert.ok(response.body.error.current.revision);
    });
  }
  test(`${release}: refused event continuation asks for resync`, () => {
    const response = read(release, 'invalid-event-continuation');
    assert.ok(response);
    assert.equal(response.status, 200);
    assert.equal(response.body.events.length, 1);
    assert.equal(response.body.events[0].type, 'stream.resync');
    parseEvent(response.body.events[0]);
    assert.equal(typeof response.body.nextAfter, 'string');
  });
}
