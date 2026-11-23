import test from 'node:test';
import assert from 'node:assert/strict';
import {tvAccountSpec} from '../../../packages/design/src/index.ts';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];

// Android TV: a TV account form's actions sit under the start of its wide fields, so Down from a field landed on the button
// nearest the field's middle ("Use a local server": Back, not Continue). The row is one autoFocus guide, so focus coming
// in lands on its first action (Continue) or on the one last focused there.
test('TV account forms: the actions are one autoFocus row, primary first, so Down from a field enters on Continue', async () => {
  const host = hooks();
  const theme = {textScale: 1, colors: {text: '#fff', textSecondary: '#aaa', textTertiary: '#777'}, layout: {gutter: {tvAuth: 120}},
    type: {accountTitle: {}, accountLede: {}, accountEyebrow: {}, accountHint: {}}};
  const layout = await screenModule(new URL('../src-next/kit/frames/TVAuthLayout.tsx', import.meta.url), {
    react: host.react, '@portico/design': {tvAccountSpec}, 'react-native-reanimated': {}, '../useMotion': {},
    'react-native': {Image: 'Image', ScrollView: 'ScrollView', StyleSheet: {flatten: (style: unknown) => style}, Text: 'Text', TVFocusGuideView: 'TVFocusGuideView', View: 'View',
      useWindowDimensions: () => ({width: 1920, height: 1080})},
    '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}}, '../artwork': {}, '../theme': {useTheme: () => theme},
    '../../shell/model': {shellLayout: () => ({left: 0, top: 0, width: 1920, height: 1080, scale: 1})},
    '../dev-observation': {useNativeObservation: () => ({ref: undefined, textProps: {}})},
    '../states/StateActionButton': {StateActionButton: 'StateActionButton'}, '../focus': {FocusRegion: 'FocusRegion'},
  });
  const primary = {id: 'inspect', label: 'Continue', onPress() {}}, back = {id: 'back', label: 'Back', onPress() {}};
  const draw = (primaryAction: any) => host.render(() => layout.TVAuthLayout({title: 'Use a local server', testID: 'accounts-entry', primaryAction, secondaryAction: back,
    beforeActions: 'FIELDS'}));
  const tree = draw(primary);
  const row = nodes(tree).find(node => node.type === 'TVFocusGuideView');
  assert.ok(row, 'the actions are drawn in one focus guide');
  assert.equal(row.props.autoFocus, true);
  assert.deepEqual(nodes(row.props.children).filter(node => node.type === 'StateActionButton').map(node => [node.props.action.id, node.props.variant]),
    [['inspect', 'primary'], ['back', 'glass']], 'Continue comes first, so it takes focus when focus first comes in');
  // tvOS: a fresh guide with nothing remembered fell back to the nearest button (Back) on every open after the first, so the
  // guide is aimed at the primary action while it can take focus.
  const continueHost = {tag: 'continue'};
  nodes(row.props.children).find(node => node.props.action?.id === 'inspect').props.ref(continueHost);
  const aimed = nodes(draw(primary)).find(node => node.type === 'TVFocusGuideView');
  assert.deepEqual(aimed.props.destinations, [continueHost], 'the guide sends focus coming in to Continue');
  const disabled = nodes(draw({...primary, disabled: true})).find(node => node.type === 'TVFocusGuideView');
  assert.deepEqual(disabled.props.destinations, [], 'a disabled Continue is never the aim (Back takes focus)');
});
