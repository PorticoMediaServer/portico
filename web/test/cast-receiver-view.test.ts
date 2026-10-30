import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {transformTsx} from './transform-tsx.mjs';
import type {CastPlayerProps} from '../src/kit/player/types.ts';

test('Cast queue advance cannot reuse another item title, art or callbacks', async t => {
  // This checks the production view's receiver binding, not artwork rendering or pixels.
  const loader = registerHooks({load(url, context, next) {
    if (url.endsWith('/kit/Icon.tsx')) return {format: 'module', source: "export const Icon='Icon';", shortCircuit: true};
    if (url.endsWith('/kit/artwork/index.ts')) return {format: 'module', source: "export const Artwork='Artwork';", shortCircuit: true};
    if (url.endsWith('/kit/controls/index.ts')) return {format: 'module', source: "export const Button='Button';", shortCircuit: true};
    if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
    if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
    return next(url, context);
  }});
  t.after(() => loader.deregister());
  const {CastDock} = await import('../src/kit/player/CastDock.tsx');
  const nodes = (node: any): any[] => !node || typeof node !== 'object' ? [] : [node, ...(Array.isArray(node.props?.children) ? node.props.children : [node.props?.children]).flatMap(nodes)];
  const props: CastPlayerProps = {
    controller: {} as CastPlayerProps['controller'], reportError() {}, phone: false, t: id => id,
    snapshot: {disposed: false, panel: null, airPlayAvailable: false, cast: {available: true, phase: 'casting', positionSeconds: 0, durationSeconds: 0, paused: false, audioTracks: [], textTracks: []},
      remote: {id: 'remote', sequence: 2, phase: 'playing', confirmed: true, itemId: 'next-item', title: 'Receiver next title', deviceName: 'TV', positionKnown: true, positionSeconds: 14, durationSeconds: 254, paused: false}},
    visuals: {itemId: 'old-item', identity: {title: 'Old local title', subtitle: []}, artwork: {id: 'old-art'} as never, skipBackSeconds: 10, skipForwardSeconds: 30},
  };
  const stale = nodes(CastDock(props));
  assert.equal(stale.find(node => node.type === 'b')!.props.children, 'Receiver next title');
  assert.equal(stale.find(node => node.type === 'Artwork')!.props.source, undefined);
  const matched = nodes(CastDock({...props, visuals: {...props.visuals, itemId: 'next-item', identity: {title: 'Actual resolved next title', subtitle: []}}}));
  assert.equal(matched.find(node => node.type === 'b')!.props.children, 'Actual resolved next title');
  assert.equal(matched.find(node => node.type === 'Artwork')!.props.source, props.visuals.artwork);
});
