import assert from 'node:assert/strict';
import test from 'node:test';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer, tree} from './helpers/interaction-harness.mjs';

// P2-29 amendment: the opening preparation may arrive before its Detail is known. The panel shows
// its own shell with quiet version-shaped lines, never a blank, an error or a different frame,
// so nothing jumps when the versions arrive.
async function fileInfo(h) {
  return componentModule(new URL('../src/kit/overlays/FileInfo.tsx', import.meta.url), {
    react: h.react, '@i18n': {defaultI18n: {t: (key, values) => values ? `${key}:${JSON.stringify(values)}` : key}},
    './Dialog': {Dialog: 'Dialog'}, './BottomSheet': {BottomSheet: 'BottomSheet'}, '../controls/Button': {Button: 'Button'},
    '../shell/ServerIdentity': {OwnerTag: 'OwnerTag', ServerMark: 'ServerMark'}, './file-info.module.css': {default: new Proxy({}, {get: (_t, key) => String(key)})},
  });
}
const loaded = {heading: 'next.kit.fileInfo', files: [{id: 'g1', available: true, rows: [], audio: [], subtitles: [], version: {id: 'g1', name: [], detail: []},
  display: {heading: {id: 'version', text: 'Version 1', role: 'identity'}, summary: [], video: [], audio: [], subtitles: []}}]};

for (const layout of ['desktop', 'phone']) test(`${layout}: preparing is the same panel as loaded, with a busy placeholder body and no count`, async () => {
  const h = createRenderer();
  try {
    const mod = await fileInfo(h), shell = layout === 'phone' ? 'BottomSheet' : 'Dialog';
    const draw = props => tree(h.render(() => mod.FileInfo({subject: 'Dune', layout, open: true, onOpenChange() {}, onManage() {}, ...props}))).find(node => node.type === shell);
    const pending = draw({pending: true, model: {heading: 'next.kit.fileInfo', files: []}}), ready = draw({model: loaded});
    assert.ok(pending && ready, `both render the ${shell}`);
    for (const key of ['placement', 'width', 'className', 'detents', 'responsive']) assert.deepEqual(pending.props[key], ready.props[key], `${key} is unchanged, so nothing jumps`);
    const busy = tree(pending.props.children).find(node => node.props?.['aria-busy'] === 'true');
    assert.ok(busy, 'the placeholder body says it is busy');
    assert.ok(!JSON.stringify(tree(pending.props.children).map(node => node.props?.children ?? '')).includes('Version'), 'no invented versions');
    if (layout === 'desktop') assert.ok(!JSON.stringify(tree(pending.props.description)).includes('versionCount'), 'no count before it is known');
  } finally {h.dispose();}
});
