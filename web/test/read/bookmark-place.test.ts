import assert from 'node:assert/strict';
import test from 'node:test';
import {bookmarkPlace} from '../../src/screens/read/ReaderAnnotations.tsx';

// SRV-A2: a text-less bookmark says its chapter, page or how far in, never an EPUB package path.
const toc = [{id: 'a', label: 'Part One', location: {format: 'epub', cfi: 'epubcfi(/6/2!/4)', progression: 0, section: 'OEBPS/part1.xhtml'},
  children: [{id: 'b', label: 'The Storm', location: {format: 'epub', cfi: 'epubcfi(/6/4!/4)', progression: .2, section: 'OEBPS/chapter3.xhtml'}}]}] as never;
test('a text-less bookmark names its chapter, page or place, never a file path', () => {
  assert.equal(bookmarkPlace({format: 'epub', cfi: 'epubcfi(/6/4!/4/2)', progression: .23, section: 'OEBPS/chapter3.xhtml'} as never, toc), 'The Storm');
  const unlisted = bookmarkPlace({format: 'epub', cfi: 'epubcfi(/6/9!/4)', progression: .5, section: 'OEBPS/notes.xhtml'} as never, toc);
  assert.doesNotMatch(unlisted, /OEBPS|xhtml/); assert.match(unlisted, /50/);
  assert.equal(bookmarkPlace({format: 'cbz', pageIndex: 4, progression: .1} as never, []), 'Page 5');
});
