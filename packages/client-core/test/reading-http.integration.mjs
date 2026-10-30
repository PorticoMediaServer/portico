import assert from 'node:assert/strict';
import {HttpLocalApi, ReadingError} from '../src/index.ts';
import {createReadingPublication, readingLocationFromReader} from '../../reader-core/src/index.ts';

// Credentials originate inside this runner's fresh Go fixture and travel only
// through this child stdin. Never print the input, API object or raw responses.
const chunks = [];
for await (const chunk of process.stdin) chunks.push(chunk);
const input = JSON.parse(Buffer.concat(chunks).toString('utf8'));
const api = new HttpLocalApi(input.origin, input.token);
const client = api.reading({viewer: input.viewer, isCurrent: () => true});
let first, second, one, two;
try {
  const list = await client.list({libraryId: input.libraryId, limit: 1});
  assert.equal(list.items.length, 1); assert.equal(list.items[0].id, input.itemId); assert.equal(list.items[0].sourceId, input.sourceId);
  const versions = await client.versions(input.itemId);
  assert.equal(versions.versions.length, 1); assert.equal(versions.versions[0].sourceId, input.sourceId);
  first = await client.open({itemId: input.itemId, sourceId: input.sourceId});
  one = await createReadingPublication(first);
  assert.equal(one.source.id, input.sourceId); assert.equal(one.source.revision, first.manifest.revision);
  assert.equal(one.format, input.format); assert.equal(one.metadata.title, first.manifest.metadata.title); assert.equal(one.metadata.author, undefined);
  const read = async publication => {
    const signal = publication.signal;
    if (publication.format === 'pdf') {
      assert.equal(new TextDecoder().decode(await publication.source.read({start: 0, endExclusive: 5}, signal)), '%PDF-');
    } else if (publication.format === 'epub') {
      assert.equal(publication.source.read, undefined);
      const entry = publication.source.resources.entries.find(entry => entry.path === first.manifest.spine[0]);
      assert.ok(entry);
      const bytes = await publication.source.resources.read(entry.path, signal);
      assert.equal(bytes.length, entry.size); assert.match(new TextDecoder().decode(bytes), /Original chapter/);
    } else {
      assert.equal(publication.source.read, undefined); assert.equal(publication.source.resources, undefined);
      const provider = await publication.comicPages(publication.source, signal);
      const page = await provider.page(0, {width: 32, signal});
      assert.equal(page.name, first.manifest.pageNames[0]); assert.equal(page.index, 0);
      assert.equal(page.image.mime, 'image/png'); assert.equal(page.image.width, 32); assert.equal(page.image.height, 64);
      await provider.dispose();
    }
  };
  await read(one);
  const empty = await first.progress(); assert.equal(empty.progress, null); assert.equal(one.resumeLocation(empty), null);
  // A known position in this original test publication, not a renderer claim.
  const location = input.format === 'epub'
    ? {format: 'epub', cfi: 'epubcfi(/6/2!/4/2[chapter]/1:0)', section: first.manifest.spine[0], progression: .25}
    : {format: input.format, page: 0, progression: .25};
  const saved = await first.saveProgress({expectedRevision: 0, location: readingLocationFromReader(location)});
  assert.equal(saved.saved, true); assert.equal(saved.progress.revision, 1); assert.ok(saved.progress.observedAt > 0);
  assert.deepEqual(one.resumeLocation(await first.progress()), location);
  await assert.rejects(first.saveProgress({expectedRevision: 0, location: readingLocationFromReader(location)}), error => error instanceof ReadingError && error.status === 412);
  assert.equal(first.signal.aborted, false);
  second = await client.open({itemId: input.itemId, sourceId: input.sourceId});
  assert.notEqual(first.manifest.sessionId, second.manifest.sessionId);
  assert.equal(first.manifest.sourceId, second.manifest.sourceId); assert.equal(first.manifest.revision, second.manifest.revision);
  two = await createReadingPublication(second);
  one.dispose(); assert.equal(one.signal.aborted, true); assert.equal(first.signal.aborted, false);
  await first.close(); await read(two);
  await second.close(); assert.equal(two.signal.aborted, true);
  const anonymous = new HttpLocalApi(input.origin).reading({viewer: input.viewer, isCurrent: () => true});
  try { await assert.rejects(anonymous.open({itemId: input.itemId, sourceId: input.sourceId}), error => error instanceof ReadingError && error.status === 401); }
  finally { anonymous.dispose(); }
  console.log(input.format + ': real HTTP publication bytes, location/CAS, independent close and auth denial PASS');
} finally {
  one?.dispose(); two?.dispose();
  for (const session of [first, second]) { try { await session?.close(); } catch {} }
  client.dispose();
}
