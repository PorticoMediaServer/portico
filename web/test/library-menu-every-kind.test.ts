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

test('the owner\'s two library actions use desktop-browse\'s words above phone width and the phone frames\' on a phone', () => {
  const header = readFileSync(new URL('../src/kit/browse/LibraryHeader.tsx', import.meta.url), 'utf8');
  assert.match(header, /const phone = usePhone\(\);/);
  assert.match(header, /\{t\(phone \? 'next\.browse\.librarySettings' : 'web\.library\.manage'\)\}/);
  assert.match(header, /\{t\(phone \? 'next\.browse\.scan' : 'web\.library\.scan'\)\}/);
  const catalog = readFileSync(new URL('../../packages/i18n/src/catalog/en-US/web.ts', import.meta.url), 'utf8');
  assert.match(catalog, /'web\.library\.scan': 'Scan library files',/);
  assert.match(catalog, /'web\.library\.manage': 'Manage library',/);
});
