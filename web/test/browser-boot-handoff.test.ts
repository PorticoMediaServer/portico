import test from 'node:test';
import assert from 'node:assert/strict';
import {runInNewContext} from 'node:vm';
import {browserCheck} from '../scripts/config.mjs';

function fixture() {
  const events: string[] = [];
  const node = (name: string) => ({name, children: [] as unknown[], style: {} as Record<string, string>, parentNode: undefined as unknown,
    get firstChild() {return this.children[0];}, setAttribute() {},
    appendChild(child: unknown) {this.children.push(child); events.push('append:' + name);},
    removeChild(child: unknown) {this.children.splice(this.children.indexOf(child), 1); events.push('remove:' + name);}});
  const root = node('root'), boot = node('boot'), body = node('body');
  body.children.push(boot, root); boot.parentNode = body;
  const document = {readyState: 'complete', body, getElementById: (id: string) => id === 'root' ? root : id === 'boot' ? boot : null,
    createElement: (tag: string) => node(tag), createTextNode: (text: string) => text};
  return {events, root, boot, body, document};
}

function guard() {
  let source = '';
  browserCheck({'web.browser.unsupportedTitle': 'Update your browser', 'web.browser.unsupportedBody': 'This browser cannot run Portico.'})
    .generateBundle.call({emitFile: (file: {source: string}) => {source = file.source;}});
  return source;
}

test('an unsupported browser draws its real message before removing the static boot layer', () => {
  const f = fixture(), window: {__porticoUnsupportedBrowser?: boolean} = {};
  runInNewContext(guard(), {window, document: f.document});
  assert.equal(window.__porticoUnsupportedBrowser, true);
  assert.equal(f.root.children.length, 1);
  assert.equal(f.body.children.includes(f.boot), false);
  assert.ok(f.events.indexOf('append:root') < f.events.indexOf('remove:body'));
  const page = f.root.children[0] as {children: {children: string[]}[]};
  assert.equal(page.children[0].children[0], 'Update your browser');
  assert.equal(page.children[1].children[0], 'This browser cannot run Portico.');
});

test('a supported browser leaves the first paint for the actual React hand-over', () => {
  const f = fixture(), window = {};
  runInNewContext(guard(), {window, document: {...f.document, createElement: () => ({noModule: false})}, CSS: {supports: () => true}, ResizeObserver: class {}, structuredClone});
  assert.deepEqual(window, {});
  assert.equal(f.root.children.length, 0);
  assert.equal(f.body.children.includes(f.boot), true);
});
