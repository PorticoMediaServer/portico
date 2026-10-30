import test from 'node:test';
import assert from 'node:assert/strict';
import {renderPdfStructure} from '../src/pdf/struct-tree.ts';

// DOM-boundary fixture: exercises the real structural projection, including
// page-local ownership. Browser/assistive-technology acceptance is separate.
class ElementFixture {
  attributes = new Map<string, string>();
  dataset: Record<string, string> = {};
  childNodes: ElementFixture[] = [];
  parent: ElementFixture | null = null;
  readonly ownerDocument: DocumentFixture;
  readonly tagName: string;
  constructor(ownerDocument: DocumentFixture, tagName = 'span') {this.ownerDocument = ownerDocument; this.tagName = tagName;}
  get id() {return this.attributes.get('id') ?? '';}
  set id(value: string) {this.attributes.set('id', value);}
  setAttribute(name: string, value: string) {this.attributes.set(name, String(value));}
  getAttribute(name: string) {return this.attributes.get(name) ?? null;}
  append(child: ElementFixture) {child.parent = this; this.childNodes.push(child);}
  closest(selector: string): ElementFixture | null {
    assert.equal(selector, '[aria-hidden="true"]');
    return this.getAttribute('aria-hidden') === 'true' ? this : this.parent?.closest(selector) ?? null;
  }
  querySelectorAll(selector: string) {
    assert.equal(selector, '.markedContent[id]');
    return walk(this).filter(element => element !== this && element.getAttribute('class') === 'markedContent' && !!element.id);
  }
}
class DocumentFixture {
  createElement(tag: string) {return new ElementFixture(this, tag);}
}
function walk(element: ElementFixture): ElementFixture[] {return [element, ...element.childNodes.flatMap(walk)];}
function fixture(ids: readonly string[]) {
  const document = new DocumentFixture(), layer = document.createElement('div');
  const marked = ids.map(id => {const element = document.createElement('span'); element.id = id; element.setAttribute('class', 'markedContent'); layer.append(element); return element;});
  return {layer, marked, render: (tree: unknown) => renderPdfStructure(tree, layer as unknown as HTMLElement) as unknown as ElementFixture | null};
}
const content = (id: string) => ({type: 'content', id});
const node = (role: string, children: unknown[] = [], fields: Record<string, unknown> = {}) => ({role, children, ...fields});

test('tagged headings own the real marked text using page-local, unique IDs', () => {
  const first = fixture(['heading']), second = fixture(['heading']);
  const tree = node('Document', [node('H1', [content('heading')])], {lang: 'en-CA'});
  const root = first.render(tree)!;
  assert.equal(root.dataset.readerStructure, '');
  assert.equal(root.getAttribute('lang'), 'en-CA');
  const heading = walk(root).find(element => element.getAttribute('role') === 'heading')!;
  assert.equal(heading.getAttribute('aria-level'), '1');
  assert.equal(heading.childNodes[0].getAttribute('aria-owns'), first.marked[0].id);
  assert.notEqual(first.marked[0].id, 'heading');
  second.render(tree);
  assert.notEqual(first.marked[0].id, second.marked[0].id, 'two mounted pages cannot claim the same DOM ID');
});

test('tagged tables, headers and list items retain their hierarchy', () => {
  const page = fixture(['column', 'body-heading', 'cell', 'item']);
  const tree = node('Document', [node('Table', [
    node('THead', [node('TR', [node('TH', [content('column')])])]),
    node('TBody', [node('TR', [node('TH', [content('body-heading')]), node('TD', [content('cell')])])]),
  ]), node('L', [node('LI', [content('item')])])]);
  const root = page.render(tree)!;
  assert.deepEqual(walk(root).flatMap(element => element.getAttribute('role') ?? []),
    ['table', 'rowgroup', 'row', 'columnheader', 'rowgroup', 'row', 'columnheader', 'cell', 'list', 'listitem']);
  assert.ok(walk(root).every(element => element.getAttribute('role') !== 'rowheader'), 'TBody does not imply row-header scope');
});

test('figure alternate text is literal accessible text and creates no HTML or actions', () => {
  const page = fixture(['figure', 'link']);
  const alt = '<img src="https://example.invalid/remote"> A map\0 of the coast';
  const root = page.render(node('Document', [node('Figure', [content('figure')], {alt}),
    node('Link', [content('link')], {href: 'javascript:alert(1)', onclick: 'alert(1)'}),
    node('Form', [{type: 'annotation', id: 'app-sign-out'}]),
    node('constructor', []),
  ]))!;
  const elements = walk(root), image = elements.find(element => element.getAttribute('role') === 'img')!;
  assert.equal(image.getAttribute('aria-label'), alt.replaceAll('\0', ''));
  assert.ok(elements.every(element => element.tagName === 'span'));
  assert.ok(elements.every(element => !element.getAttribute('href') && !element.getAttribute('onclick') && !element.getAttribute('tabindex')));
  assert.ok(elements.every(element => !['link', 'form'].includes(element.getAttribute('role') ?? '')));
  assert.ok(elements.every(element => element.getAttribute('aria-owns') !== 'app-sign-out'));
});

test('untagged pages keep the ordinary text layer and external, duplicate or hidden IDs are not owned', () => {
  const page = fixture(['duplicate', 'duplicate', 'hidden', 'visible']);
  page.marked[2].setAttribute('aria-hidden', 'true');
  assert.equal(page.render(null), null);
  const root = page.render(node('Document', [node('P', [content('duplicate'), content('hidden'), content('outside-app-node'), content('visible'), content('visible')])]))!;
  assert.equal(walk(root).filter(element => element.getAttribute('aria-owns')).length, 1);
  assert.equal(page.marked[0].id, 'duplicate');
  assert.equal(page.marked[2].id, 'hidden');
  assert.notEqual(page.marked[3].id, 'visible');
});

test('structure projection refuses excessive depth, DOM nodes and alternate-text allocation', () => {
  const cycle = node('Div'); cycle.children.push(cycle);
  assert.throws(() => fixture([]).render(cycle), {code: 'pdf_limit'});
  assert.throws(() => fixture([]).render(node('Document', Array.from({length: 50001}, () => node('P')))), {code: 'pdf_limit'});
  assert.throws(() => fixture([]).render(node('Figure', [], {alt: 'a'.repeat(65537)})), {code: 'pdf_limit'});
});
