import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import {lifecycleHooks, nodes, content} from './helpers/owner-server-settings.mjs';

const t = (key: string) => key;
const tick = async () => {for (let n = 0; n < 4; n++) await Promise.resolve();};
const folders: Record<string, {name: string; path: string}[]> = {'': [{name: 'Films', path: 'Films'}, {name: 'Shows', path: 'Shows'}], Films: [{name: 'Classics', path: 'Films/Classics'}], Shows: [], 'Films/Classics': []};

async function browser(multiple: boolean) {
  const h = lifecycleHooks(), chosen: any[] = [], opened: string[] = [];
  const controller = {folders: async (_id: string, path: string) => {opened.push(path); return folders[path] ?? [];}};
  const module = await screenModule(new URL('../src-next/screens/settings/remote-sources/SourceFolderBrowser.tsx', import.meta.url), {
    react: h.react, 'react-native': {Text: 'Text'}, '../../../kit/controls/Button': {Button: 'Button'},
    './RemoteSourceForms': {Page: 'Page', Problem: 'Problem', remoteSourceError: () => 'error'},
  });
  const render = () => h.render(() => module.SourceFolderBrowser({controller, source: {id: 'rs_1', name: 'Archive'}, t, isCurrent: () => true, multiple, onCancel() {}, onChoose: (value: unknown) => {chosen.push(value);}}));
  const press = async (label: string) => {
    const button = nodes(render()).find((node: any) => node.type === 'Button' && content(node) === label);
    assert.ok(button, `${label} among ${nodes(render()).filter((node: any) => node.type === 'Button').map((node: any) => content(node)).join(', ')}`);
    button.props.onPress(); render(); h.flush(); await tick();
  };
  render(); h.flush(); await tick();
  return {h, chosen, opened, render, press, choose: () => {nodes(render()).find((node: any) => node.type === 'Page').props.footer.props.onPress();}};
}

test('Add library chooses several folders of a source, each opened in turn, and gets them all', async () => {
  const b = await browser(true);
  try {
    await b.press('Films');            // open Films
    await b.press('Films');            // choose the folder now open
    await b.press('Classics');         // open Classics
    await b.press('Classics');         // choose it too
    b.choose();
    assert.deepEqual(b.chosen.at(-1), [{name: 'Films', path: 'Films'}, {name: 'Classics', path: 'Films/Classics'}]);
    assert.deepEqual(b.opened, ['', 'Films', 'Films/Classics']);
  } finally {b.h.unmountAll?.();}
});

test('when Add library takes one folder, choosing another replaces the first', async () => {
  const b = await browser(false);
  try {
    await b.press('Films'); await b.press('Films');
    await b.press('next.remoteSources.back');
    await b.press('Shows'); await b.press('Shows');
    b.choose();
    assert.deepEqual(b.chosen.at(-1), [{name: 'Shows', path: 'Shows'}]);
  } finally {b.h.unmountAll?.();}
});
