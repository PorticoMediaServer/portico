import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {readFileSync} from 'node:fs';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from './transform-tsx.mjs';

// FX-M7: show, season, album, artist and book pages reach the metadata editor from their ⋯ menu's
// Owner section, the way leaf titles already did. The real OwnerMetadataGroup runs; the overlay kit
// is replaced by plain elements that record each row's onSelect so the test can press it.
type Row = {label: string; icon?: string; onSelect?: () => void};
const rows: Row[] = [];
(globalThis as {__ownerRows?: Row[]}).__ownerRows = rows;
registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (/\/kit\/overlays\/index\.tsx?$/.test(url)) return {format: 'module', shortCircuit: true, source: `
    import {createElement} from 'react';
    export const MenuSeparator = () => createElement('hr');
    export const MenuGroup = ({label, ownerOnly, children}) => createElement('section', {'data-owner-only': ownerOnly ? '' : undefined}, createElement('h6', null, label), children);
    export const MenuItem = ({icon, onSelect, children}) => { globalThis.__ownerRows.push({label: String(children), icon, onSelect}); return createElement('button', {'data-icon': icon}, children); };`};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {OwnerMetadataGroup} = await import('../src/screens/detail/OwnerMetadataGroup.tsx');

const anchor = {id: 'more'} as unknown as HTMLElement;
function context(edit?: (...args: unknown[]) => Promise<void>, errors: unknown[] = []) {
  return {effects: {...(edit ? {editMetadata: edit} : {}), reportError: (error: unknown) => errors.push(error)}} as never;
}
function render(props: Record<string, unknown>) {
  rows.length = 0;
  return renderToStaticMarkup(createElement(OwnerMetadataGroup as never, {anchor: () => anchor, guard: () => {}, ...props}));
}
const show = {entityKind: 'show', entityId: 'show-1', libraryId: 'tv', title: 'Severance'} as const;

test('an owner\'s ⋯ menu gets Edit metadata… under the Owner heading, and it opens the editor on that exact title', async () => {
  const opened: unknown[][] = [];
  const html = render({context: context(async (...args) => {opened.push(args);}), target: show});
  assert.match(html, /data-owner-only/);
  assert.deepEqual(rows.map(row => [row.label, row.icon]), [['Edit metadata…', 'i-edit']]);
  rows[0]!.onSelect!();
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(opened, [[show, anchor]]);
});

test('a member (no editor effect) sees no Owner section; other owner rows keep it', () => {
  assert.equal(render({context: context(), target: show}), '');
  const html = render({context: context(), target: show, children: createElement('button', null, 'Show settings')});
  assert.match(html, /Show settings/);
  assert.equal(rows.length, 0, 'no Edit metadata… without the editor');
});

test('a row without an editable target (a season group, an unknown row) draws no Edit metadata…', () => {
  render({context: context(async () => {}), target: undefined, children: createElement('button', null, 'Episode display')});
  assert.equal(rows.length, 0);
});

test('a menu left open after its page changed opens nothing', async () => {
  const opened: unknown[] = [], errors: unknown[] = [];
  render({context: context(async (...args) => {opened.push(args);}, errors), target: show, guard: () => {throw new DOMException('Show page changed', 'AbortError');}});
  rows[0]!.onSelect!();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(opened.length, 0);
  assert.equal((errors[0] as DOMException).name, 'AbortError');
});

test('sibling sweep: every parent title page wires the shared Owner section with its own kind', () => {
  const source = (path: string) => readFileSync(new URL(`../src/screens/detail/${path}`, import.meta.url), 'utf8');
  for (const [file, kinds] of [
    ['Show.tsx', ["entityKind: 'show'", "entityKind: 'season'"]],
    ['Album.tsx', ["entityKind: 'album'", "kind: 'song'"]],
    ['Artist.tsx', ["entityKind: 'artist'"]],
    ['audiobooks/More.tsx', ["entityKind: 'book'"]],
    ['audiobooks/Entry.tsx', ["kind: 'audiobook_file'"]],
  ] as const) {
    const text = source(file);
    assert.match(text, /<OwnerMetadataGroup/, `${file} draws the Owner section`);
    for (const kind of kinds) assert.ok(text.includes(kind), `${file} edits ${kind}`);
  }
});
