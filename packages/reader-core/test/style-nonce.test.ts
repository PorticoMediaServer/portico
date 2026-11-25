import test from 'node:test';
import assert from 'node:assert/strict';
import {createNonceStyle, styleNonce} from '../src/style-nonce.ts';
import {mountFixed} from '../src/pdf/fixed-view.ts';

function fakeDoc(nonce?: string) {
  const appendedHead: {tagName: string; nonce?: string; textContent: string; removed?: boolean}[] = [];
  const makeEl = (tag: string) => {
    const el: Record<string, unknown> = {
      tagName: tag.toUpperCase(),
      style: {},
      dataset: {},
      children: [] as unknown[],
      tabIndex: 0,
      textContent: '',
      setAttribute() {},
      removeAttribute() {},
      addEventListener() {},
      removeEventListener() {},
      append(...nodes: unknown[]) {(this.children as unknown[]).push(...nodes);},
      replaceChildren(...nodes: unknown[]) {this.children = [...nodes];},
      remove() {this.removed = true;},
      querySelector() {return null;},
      querySelectorAll() {return [];},
      getBoundingClientRect() {return {left: 0, top: 0, width: 800, height: 600, bottom: 600, right: 800};},
    };
    return el;
  };
  const doc = {
    documentElement: {dataset: {}},
    head: {append(node: {tagName: string; nonce?: string; textContent: string; removed?: boolean}) {appendedHead.push(node);}},
    createElement: makeEl,
    querySelector(selector: string) {
      if (selector === 'meta[name="portico-style-nonce"]' && nonce !== undefined) return {content: nonce};
      return null;
    },
  };
  return {doc, appendedHead};
}

test('styleNonce ignores empty strings and unreplaced placeholder, and trims valid nonces', () => {
  assert.equal(styleNonce(fakeDoc().doc as never), undefined);
  assert.equal(styleNonce(fakeDoc('').doc as never), undefined);
  assert.equal(styleNonce(fakeDoc('   ').doc as never), undefined);
  assert.equal(styleNonce(fakeDoc('__PORTICO_STYLE_NONCE__').doc as never), undefined);
  assert.equal(styleNonce(fakeDoc('abc123').doc as never), 'abc123');
  assert.equal(styleNonce(fakeDoc('  abc123  ').doc as never), 'abc123');
  assert.equal(styleNonce({} as never), undefined);
});

test('createNonceStyle assigns app nonce to target style and omits it for placeholder', () => {
  const target = fakeDoc().doc;
  const app = fakeDoc('nonce-xyz').doc;
  const styled = createNonceStyle(target as never, 'body{color:red}', app as never) as unknown as {nonce?: string; textContent: string};
  assert.equal(styled.nonce, 'nonce-xyz');
  assert.equal(styled.textContent, 'body{color:red}');

  const placeholder = createNonceStyle(target as never, 'body{}', fakeDoc('__PORTICO_STYLE_NONCE__').doc as never) as unknown as {nonce?: string; textContent: string};
  assert.equal(placeholder.nonce, undefined);
  assert.equal(placeholder.textContent, 'body{}');
});

test('mountFixed applies document CSP style nonce to its injected head style', async () => {
  const {doc, appendedHead} = fakeDoc('pdf-nonce-42');
  const container = {ownerDocument: doc, append() {}};
  const view = await mountFixed('pdf', 1, 'ltr', async () => ({
    width: 400, height: 600,
    async mount() {return () => {};},
    dispose() {},
  }), {
    container: container as never,
    preferences: {twoPages: false, firstPageAlone: true, comicFit: 'auto', comicDirection: 'file', pdfView: 'page'},
    viewport: {width: 800, height: 600},
    signal: new AbortController().signal,
    onLocation() {},
    onSelection() {},
  });
  try {
    assert.equal(appendedHead.length, 1);
    assert.equal(appendedHead[0].tagName, 'STYLE');
    assert.equal(appendedHead[0].nonce, 'pdf-nonce-42');
  } finally {
    await view.dispose();
  }
});
