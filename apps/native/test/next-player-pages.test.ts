import assert from 'node:assert/strict';
import test from 'node:test';
import {defaultI18n} from '../../../packages/i18n/src';
import {screenModule} from './helpers/screen-harness.mjs';

type Node = {type: string; props: Record<string, any>};
const react = {default: {Fragment: 'Fragment', createElement: (type: string, props: object, ...children: Node[]): Node => ({type, props: {...props, children}})}};

test('the upright player keeps the host picture and routes Quality, full screen and Minimize', async () => {
  const {PlayerPage} = await screenModule(new URL('../src-next/pages/touch/player/PlayerPage.tsx', import.meta.url), {
    react, '../../../kit/frames': {PlayerChromeFrame: 'PlayerChromeFrame'},
    '../../../kit/player': {PlayerControl: 'PlayerControl', PlayerPill: 'PlayerPill'},
    '../../../../../../packages/i18n/src': {defaultI18n},
  });
  const actions: string[] = [];
  const frame = PlayerPage({picture: 'picture', chromeVisible: true, quality: 'Original', controlsEnabled: true,
    onMinimize: () => actions.push('minimize'), onQuality: () => actions.push('quality'), onFullWindow: () => actions.push('full')}) as Node;
  assert.equal(frame.props.picture, 'picture');
  assert.equal(frame.props.form, 'upright');
  frame.props.trailing.props.children[0].props.onPress();
  frame.props.trailing.props.children[1].props.onPress();
  frame.props.close.props.onPress();
  assert.deepEqual(actions, ['quality', 'full', 'minimize']);
});

test('TV video keeps the full title, remote focus and held-seek content in one frame', async () => {
  const {TVPlayerPage} = await screenModule(new URL('../src-next/pages/tv/player/PlayerPage.tsx', import.meta.url), {
    react, '../../../kit/frames': {PlayerChromeFrame: 'PlayerChromeFrame'}, '../../../kit/player': {PlayerTitle: 'PlayerTitle'},
  });
  const frame = TVPlayerPage({identity: {title: 'Full episode title'}, focus: {transport: {entry: 'watch-toggle'}}, scrubbing: true, hints: 'remote hints'}) as Node;
  assert.equal(frame.props.form, 'tv');
  assert.equal(frame.props.title.props.title, 'Full episode title');
  assert.equal(frame.props.focus.transport.entry, 'watch-toggle');
  assert.equal(frame.props.hints, 'remote hints');
});
