import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';

const route = readFileSync(new URL('../src/screens/browse/Library.route.tsx', import.meta.url), 'utf8');

test('every library draws its ⋯ from one composition, not only reading or online libraries (survey A 3)', () => {
  assert.match(route, /libraryMenu: withLinkToast\(libraryMenu\(\{life: resources\.life, controller\}, context, library, onlineAttention, save, scan, isReadingLibrary\(library\.kind\) \? readingMarkAll : undefined, copyLibraryLink\)/);
  assert.doesNotMatch(route, /LibraryMetadataMenu/);
  // Scan for new files is leased for every kind the owner manages, and the reading wording follows the kind.
  assert.match(route, /if \(!manageServer\) \{setScanOffer\(undefined\); return;\}/);
  assert.match(route, /<LibraryMenu owner=\{!!owner\} read=\{isReadingLibrary\(library\.kind\)\}/);
});
