import test from 'node:test';
import assert from 'node:assert/strict';
import {MEMBER_UNREADABLE, memberUnreadable, orUnreadable, unreadablePageSurface, unreadableSectionXhtml, unreadableEpubMember, unreadableEpubText, UnreadableSections, settleUnreadableSection} from '../src/unreadable.ts';
import {rasterInfo} from '../src/comic/image.ts';

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

test('#699 review: an unreadable EPUB image is left out as a transparent pixel, not a broken-image "null" body; a section is the placeholder; anything else is empty', async () => {
  const image = unreadableEpubMember('OEBPS/images/plate.png', 'image/png', 'unused');
  const bytes = new Uint8Array(await image.arrayBuffer());
  assert.equal(image.type, 'image/gif');
  assert.notEqual(new TextDecoder().decode(bytes), 'null', 'foliate would wrap a null into a 4-byte "null" image');
  const info = rasterInfo(bytes); assert.equal(info.mime, 'image/gif'); assert.equal(info.width, 1); assert.equal(info.height, 1);
  assert.equal(bytes[bytes.length - 1], 0x3b, 'a complete GIF');
  const section = unreadableEpubMember('OEBPS/ch2.xhtml', 'application/xhtml+xml', 'This part can’t be read.');
  assert.equal(section.type, 'application/xhtml+xml'); assert.match(await section.text(), /data-reader-unreadable="true"[^>]*>This part can’t be read\.</);
  const font = unreadableEpubMember('OEBPS/fonts/a.otf', 'font/otf', 'unused');
  assert.equal(font.size, 0); assert.equal(font.type, 'font/otf');
});

test('#699 review: a Contents #fragment inside a placeholder section opens at its start; placeholders are never searched', () => {
  const sections = new UnreadableSections();
  sections.add('OEBPS/ch2.xhtml');
  assert.equal(sections.fragment('OEBPS/ch2.xhtml', 'part-two'), undefined, 'the placeholder has none of the book\'s ids');
  assert.equal(sections.fragment('OEBPS/ch3.xhtml', 'part-three'), 'part-three', 'a readable section keeps its fragment (a missing id there is still invalid_location)');
  assert.equal(sections.fragment('OEBPS/ch3.xhtml', undefined), undefined);
  assert.equal(sections.has('OEBPS/ch2.xhtml'), true, 'local search skips it'); assert.equal(sections.has('OEBPS/ch3.xhtml'), false); assert.equal(sections.has(undefined), false);
});

test('#699 review: a placeholder\'s line can\'t be selected (so never highlighted) and stays centred whatever the reader\'s text rules say', () => {
  const set: [string, string, string][] = [];
  const note = {style: {setProperty: (name: string, value: string, priority: string) => {set.push([name, value, priority]);}}};
  settleUnreadableSection({querySelectorAll: (selector: string) => selector === '[data-reader-unreadable]' ? [note] : []} as unknown as Document);
  const value = (name: string) => set.find(entry => entry[0] === name);
  assert.deepEqual(value('user-select'), ['user-select', 'none', 'important']);
  assert.deepEqual(value('-webkit-user-select'), ['-webkit-user-select', 'none', 'important']);
  assert.deepEqual(value('text-align'), ['text-align', 'center', 'important']);
});

test('#699 re-check: an unreadable SVG read as text is an empty SVG, not foliate\'s "null" src; markup is the placeholder; anything else is nothing', () => {
  const svg = unreadableEpubText('OEBPS/images/map.svg', 'image/svg+xml', 'unused');
  assert.ok(svg); assert.notEqual(svg, 'null');
  assert.match(svg, /^<svg xmlns="http:\/\/www\.w3\.org\/2000\/svg"[^>]*\/>$/, 'a complete, empty SVG');
  assert.equal(unreadableEpubText('OEBPS/images/map.svg', '', 'unused'), svg, 'by extension when the manifest has no type');
  assert.match(unreadableEpubText('OEBPS/ch2.xhtml', 'application/xhtml+xml', 'Can’t be read.')!, /data-reader-unreadable="true"[^>]*>Can’t be read\.</);
  assert.equal(unreadableEpubText('OEBPS/style.css', 'text/css', 'unused'), null);
});
