import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {defaultI18n} from '../../../packages/i18n/src';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];

/** Play from other devices: the TV asks once per sending device; closing the question declines it. */
test('the receiver question names the device, and Allow, Decline and closing each answer once', async () => {
  const host = hooks();
  const {ReceiverApproval} = await screenModule(new URL('../src-next/kit/player/ReceiverApproval.tsx', import.meta.url), {
    react: host.react, 'react-native': {Text: 'Text', View: 'View'}, '../../../../../packages/i18n/src': {defaultI18n},
    '../controls': {Button: 'Button'}, '../overlays': {Dialog: 'Dialog'},
    '../theme': {useTheme: () => ({space: {sm: 16}, type: {body: {}}, colors: {textSecondary: '#aaa'}})},
  });
  const answers: string[] = [];
  const tree = host.render(() => ReceiverApproval({open: true, device: 'Living room phone', onAllow: () => answers.push('allow'),
    onDecline: () => answers.push('decline')}));
  assert.equal(tree.type, 'Dialog');
  assert.equal(tree.props.open, true);
  assert.match(tree.props.title, /Living room phone/);
  const button = (id: string) => nodes(tree).find(node => node.props.testID === id);
  button('tv-receiver-allow').props.onPress();
  button('tv-receiver-decline').props.onPress();
  tree.props.onOpenChange(false);
  tree.props.onOpenChange(true);
  assert.deepEqual(answers, ['allow', 'decline', 'decline']);
});
