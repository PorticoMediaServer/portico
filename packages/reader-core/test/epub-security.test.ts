import {test} from 'node:test';
import assert from 'node:assert/strict';
import {markupDeclarationsSafe} from '../src/epub/security.ts';

// W1 F6 (16 Nov walk): every Project Gutenberg EPUB part starts with an XHTML 1.1 DOCTYPE, and the
// reader refused the whole book (Frankenstein would not open).
test('a standard html or svg document type opens; entities and internal subsets never do', () => {
  const body = '<html xmlns="http://www.w3.org/1999/xhtml"><body><p>It was on a dreary night of November.</p></body></html>';
  for (const doctype of ["<!DOCTYPE html PUBLIC '-//W3C//DTD XHTML 1.1//EN' 'http://www.w3.org/TR/xhtml11/DTD/xhtml11.dtd'>",
    '<!DOCTYPE html>', '<!doctype html SYSTEM "about:legacy-compat">',
    '<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd">'])
    assert.equal(markupDeclarationsSafe(`<?xml version="1.0"?>\n${doctype}\n${body}`), true, doctype);
  for (const refused of ['<!DOCTYPE html [<!ENTITY x "boom">]>', '<!DOCTYPE html SYSTEM "x" [<!ENTITY x SYSTEM "file:///etc/passwd">]>',
    '<!DOCTYPE html><!DOCTYPE html>', '<!DOCTYPE foo>', '<!ENTITY x "y">'])
    assert.equal(markupDeclarationsSafe(`${refused}${body}`), false, refused);
});
