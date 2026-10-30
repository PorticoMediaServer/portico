import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {defaultI18n} from '../../../packages/i18n/src';
import {presentError} from '../../../packages/client-core/src/presentation/errors.ts';

function elements(node: any): any[] {
  if (Array.isArray(node)) return node.flatMap(elements);
  return node?.props ? [node, ...elements(node.props.children)] : [];
}

test('ruling 37 naming sheet keeps exact existing fields/copy, glass Cancel and guarded white Save', async () => {
  const host = hooks(); let saved = 0, closed = 0;
  let state: any = {open: true, name: '', busy: false, nameLocked: false};
  const {NativeLibrarySaveViewSheet} = await screenModule(new URL('../src-next/screens/library/SaveViewSheet.tsx', import.meta.url), {
    react: host.react, 'react-native': {Text: 'Text', View: 'View'}, '../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../packages/client-core/src/presentation/errors': {presentError}, '../../kit/controls': {Button: 'Button', TextField: 'TextField'},
    '../../kit/overlays': {Banner: 'Banner', BottomSheet: 'BottomSheet'}, '../../kit/theme': {useTheme: () => ({type: {caption: {}}, colors: {textTertiary: '#aaa'}})},
  });
  const controller = {subscribe() {}, getSnapshot: () => state, close: () => {closed++;}, save: async () => {saved++;}, setName() {}};
  const render = () => host.render(() => NativeLibrarySaveViewSheet({controller}));
  let tree = render(), field = elements(tree).find(node => node.type === 'TextField');
  assert.equal(tree.props.title, defaultI18n.t('lib.saveViewTitle')); assert.equal(tree.props.description, defaultI18n.t('lib.saveViewLede'));
  assert.equal(elements(tree).filter(node => node.type === 'TextField').length, 1, 'there is no Description field');
  assert.equal(field.props.label, defaultI18n.t('lib.saveViewName')); assert.equal(field.props.placeholder, defaultI18n.t('lib.saveViewPlaceholder'));
  assert.equal(field.props.maxLength, 200); assert.equal(field.props.autoFocus, true);
  let buttons = elements(tree.props.footer).filter(node => node.type === 'Button');
  assert.equal(buttons[0].props.variant, 'glass'); assert.equal(buttons[1].props.variant, 'primary'); assert.equal(buttons[1].props.disabled, true);
  state = {...state, name: 'Actual typed name', error: {code: 'network_error'}}; tree = render();
  assert.equal(elements(tree).find(node => node.type === 'TextField').props.value, 'Actual typed name');
  const content = elements(tree);
  assert.ok(content.findIndex(node => node.type === 'Banner') < content.findIndex(node => node.type === 'TextField'));
  const notice = content.find(node => node.type === 'Banner');
  assert.equal(notice.props.variant, 'notice'); assert.equal(notice.props.tone, 'error'); assert.equal(notice.props.placement, 'inline');
  buttons = elements(tree.props.footer).filter(node => node.type === 'Button'); buttons[1].props.onPress(); assert.equal(saved, 1);
  field.props.onSubmitEditing(); assert.equal(saved, 2, 'Enter uses the same controller save guard');
  state = {...state, busy: true}; tree = render(); buttons = elements(tree.props.footer).filter(node => node.type === 'Button');
  assert.equal(buttons[0].props.disabled, true); assert.equal(buttons[1].props.loading, true); assert.equal(buttons[1].props.disabled, true);
  assert.equal(tree.props.dismissOnBack, false); assert.equal(closed, 0);
});
