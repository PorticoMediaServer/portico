import test from 'node:test';
import assert from 'node:assert/strict';
import {MEMBER_UNREADABLE, memberUnreadable, orUnreadable, unreadablePageSurface, unreadableSectionXhtml} from '../src/unreadable.ts';

// RD-2 (#669): the server rejects one EPUB asset or comic page as content with 422
// `reading_member_unreadable` (retry never) and keeps the reading session open.
const rejected = Object.assign(new Error(MEMBER_UNREADABLE), {name: 'ReadingError', code: MEMBER_UNREADABLE, status: 422});

test('only a member rejected as content is stood in for; every other failure and a missing stand-in still fail', async () => {
  assert.equal(memberUnreadable(rejected), true);
  assert.equal(await orUnreadable(async () => {throw rejected;}, () => 'stand-in'), 'stand-in');
  for (const other of [Object.assign(new Error('source_changed'), {code: 'source_changed'}), Object.assign(new Error('forbidden'), {code: 'forbidden', status: 403}),
    Object.assign(new Error('reading_session_expired'), {code: 'reading_session_expired', status: 410}), new Error('network')]) {
    await assert.rejects(orUnreadable(async () => {throw other;}, () => 'stand-in'), other, 'source, access, session and I/O failures are not stood in for');
  }
  await assert.rejects(orUnreadable(async () => {throw rejected;}, undefined), rejected, 'no stand-in (before the book opens): the failure propagates');
  assert.equal(await orUnreadable(async () => 'bytes', () => 'stand-in'), 'bytes', 'a readable member is read as ever');
});

class FakeElement {
  readonly children: FakeElement[] = []; readonly attributes = new Map<string, string>(); readonly dataset: Record<string, string> = {};
  readonly style: Record<string, string> = {}; textContent = ''; parent?: FakeElement;
  readonly ownerDocument: {createElement(tag: string): FakeElement}; readonly tagName: string;
  constructor(ownerDocument: {createElement(tag: string): FakeElement}, tagName: string) {this.ownerDocument = ownerDocument; this.tagName = tagName;}
  setAttribute(name: string, value: string) {this.attributes.set(name, value);}
  append(child: FakeElement) {child.parent = this; this.children.push(child);}
  remove() {if (this.parent) this.parent.children.splice(this.parent.children.indexOf(this), 1);}
}
const fakeDocument = {createElement: (tag: string): FakeElement => new FakeElement(fakeDocument, tag)};

test('an unreadable comic page is a page surface at its neighbours\' size that says so, and mounts and unmounts like any page', async () => {
  const surface = unreadablePageSurface('This page can’t be read.', {width: 1200, height: 1800});
  assert.equal(surface.width, 1200); assert.equal(surface.height, 1800, 'the spread doesn\'t jump');
  const target = fakeDocument.createElement('div');
  const release = await surface.mount(target as unknown as HTMLElement, 0.5, new AbortController().signal);
  const note = target.children[0]!;
  assert.equal(note.textContent, 'This page can’t be read.'); assert.equal(note.attributes.get('role'), 'img');
  assert.equal(note.attributes.get('aria-label'), 'This page can’t be read.', 'screen readers hear it as the page'); assert.equal(note.dataset.readerUnreadable, 'true');
  release(); assert.equal(target.children.length, 0);
  const aborted = new AbortController(); aborted.abort();
  await assert.rejects(surface.mount(target as unknown as HTMLElement, 1, aborted.signal));
});

test('an unreadable EPUB section is a small well-formed XHTML page whose line is escaped', () => {
  const page = unreadableSectionXhtml("Part <3> & “more” can't be read");
  assert.match(page, /^<\?xml version="1\.0" encoding="utf-8"\?>/);
  assert.match(page, /<html xmlns="http:\/\/www\.w3\.org\/1999\/xhtml">/);
  assert.match(page, /data-reader-unreadable="true"[^>]*>Part &lt;3&gt; &amp; “more” can&apos;t be read<\/p>/);
  assert.doesNotMatch(page, /<script|on\w+=/i);
});
