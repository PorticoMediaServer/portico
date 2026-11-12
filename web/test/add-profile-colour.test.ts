// A new profile's colour on the web (settings/account-profile/AddProfileDialog.tsx): untouched, the
// picker shows the colour the server will give (the first unused on the account) and none is sent.
import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule, hooks} from './helpers/component-harness.mjs';

const all = (node: any): any[] => Array.isArray(node) ? node.flatMap(all) : node?.props ? [node, ...all(node.props.children)] : [];
/** A freshly opened Add profile on an account whose profiles are blue and violet. */
async function opened() {
  const host = hooks(), created: unknown[][] = [];
  const document = {canManage: true, capabilities: {manageAccount: true}, profiles: [{id: 'main', name: 'Ada', art: 'blue'}, {id: 'kids', name: 'Kids', art: 'violet'}]};
  const source = {subscribe: () => () => {}, getSnapshot: () => ({document, busy: false}), create: async (...args: unknown[]) => {created.push(args);}};
  const {AddProfileDialog} = await componentModule(new URL('../src/screens/settings/account-profile/AddProfileDialog.tsx', import.meta.url), {
    react: {...host.react, useEffect: () => {}},
    '@design': {profileArtColors: {blue: '#1', violet: '#2', mint: '#3', coral: '#4', gold: '#5', slate: '#6', rose: '#7', sky: '#8'}},
    '@i18n': {defaultI18n: {t: (key: string) => key}},
    '../../../kit/controls': {Button: 'Button', TextField: 'TextField'}, '../../../kit/overlays/Dialog': {Dialog: 'Dialog'}, './profile.module.css': {default: {}},
  });
  // The dialog and its form drawn in one pass, so their hooks keep their places between draws.
  const draw = () => all(host.render(() => {const dialog = AddProfileDialog({source, isCurrent: () => true, onClose: () => {}}); return dialog.type(dialog.props);}));
  let page = draw();
  return {
    created, page: () => page,
    shown: () => page.filter(node => node.props.role === 'radio' && node.props['aria-checked']).map(node => node.props.style['--profile-color']),
    choose: (index: number) => {page.filter(node => node.props.role === 'radio')[index].props.onClick(); page = draw();},
    save: async (name: string) => {page.find(node => node.type === 'TextField').props.onChange({currentTarget: {value: name}}); page = draw();
      await page.find(node => node.type === 'form').props.onSubmit({preventDefault() {}});},
  };
}

test('Add profile shows the colour the server will give and sends none until one is chosen', async () => {
  const untouched = await opened();
  assert.deepEqual(untouched.shown(), ['#3'], 'mint: the first not used on this account, the one the server will give');
  await untouched.save('Cinema');
  assert.deepEqual(untouched.created, [['Cinema', undefined]], 'untouched: no colour is sent');
  const chosen = await opened();
  chosen.choose(4);
  assert.deepEqual(chosen.shown(), ['#5']);
  await chosen.save('Cinema');
  assert.deepEqual(chosen.created, [['Cinema', 'gold']], 'a chosen colour is sent, even one the server would also have given');
  const blue = await opened();
  blue.choose(0); await blue.save('Cinema');
  assert.deepEqual(blue.created, [['Cinema', 'blue']], 'a deliberate blue is sent as blue');
});
