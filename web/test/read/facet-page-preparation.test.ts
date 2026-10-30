import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '@i18n';
import {componentModule} from '../helpers/component-harness.mjs';
import {createRenderer, tree, one, textOf} from '../helpers/interaction-harness.mjs';
const settle = async () => {for (let n = 0; n < 15; n++) await Promise.resolve();};
const page = {field: 'author', total: 2, values: [{value: 'Author', label: 'Author', count: 2}]};
async function fixture() {
  const host = createRenderer(), reads: {resolve: (value: unknown) => void; reject: (error: unknown) => void; signal: AbortSignal}[] = [];
  const {FacetPage} = await componentModule(new URL('../../src/screens/read/FacetPage.tsx', import.meta.url), {
    react: host.react, '@i18n': {defaultI18n}, '../../kit/controls': {Button: 'Button'}, '../../kit/states': {Skeleton: 'Skeleton', StateView: 'StateView'},
    './facet-page.module.css': {default: {}}, './facets': {loadFacetPage: (_request: unknown, _options: unknown, signal: AbortSignal) => new Promise((resolve, reject) => {reads.push({resolve, reject, signal});})},
  });
  let request = Object.assign(async () => page, {isCurrent: () => true});
  const draw = () => host.render(() => FacetPage({libraryId: 'lib', field: 'author', pivot: 'books', request}));
  return {host, reads, draw, replace: () => {request = Object.assign(async () => page, {isCurrent: () => true});}};
}

test('Reading facet retry retains its resident values during replacement preparation', async () => {
  const f = await fixture();
  try {
    f.draw(); f.reads[0]!.resolve(page); await settle(); let shown = f.draw(); assert.match(textOf(shown), /Author/);
    one(tree(shown), 'Button').props.onClick(); f.reads[1]!.reject(new Error('offline')); await settle();
    shown = f.draw(); assert.match(textOf(shown), /Author/, 'a failed continuation retains the resident facet');
    one(tree(shown), 'Button').props.onClick();
    f.draw(); shown = f.draw(); assert.match(textOf(shown), /Author/, 'retry keeps the existing facet while its first replacement is pending');
    assert.equal(one(tree(shown), 'Button').props.disabled, true, 'load-more cannot race the preparing replacement');
    f.reads[2]!.resolve({...page, values: [{value: 'New', label: 'New', count: 2}]}); await settle(); assert.match(textOf(f.draw()), /New/);
  } finally {f.host.dispose();}
});

test('a different captured request never renders the previous profile’s resident facet', async () => {
  const f = await fixture();
  try {
    f.draw(); f.reads[0]!.resolve(page); await settle(); assert.match(textOf(f.draw()), /Author/);
    f.replace(); const first = f.draw(); assert.equal(one(tree(first), 'Skeleton').props.height, 320); assert.doesNotMatch(textOf(first), /Author/);
    assert.equal(f.reads[0]!.signal.aborted, true); assert.equal(one(tree(f.draw()), 'Skeleton').props.height, 320);
  } finally {f.host.dispose();}
});
