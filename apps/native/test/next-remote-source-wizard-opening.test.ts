import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import {fabricHost, React} from './helpers/fabric-host.mjs';
import * as flow from '../src-next/screens/settings/remote-sources/source-flow.ts';

const t = (key: string) => key;
const source = {id: 'archive', kind: 'webdav', name: 'Archive', generation: 1};
const folder = {name: 'Movies', path: '/media/Movies', relative: 'Movies'};
const hosts = (node: any): any[] => Array.isArray(node) ? node.flatMap(hosts) : node?.type ? [node, ...hosts(node.children)] : [];

test('one parent opening retains folders and assignment drafts across Back, Continue and accepted generations', async () => {
  const host = await fabricHost(), context = React.createContext(undefined);
  const drawn = (name: string) => (props: any) => React.createElement(name, {...props, wizard: React.useContext(context)}, props.children);
  const state = {allowed: true, sources: {sources: [source], receipt: null, busy: false, loading: false}};
  const module = await screenModule(new URL('../src-next/screens/settings/remote-sources/NativeRemoteSourcesScreen.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    '../../../../../../packages/i18n/src': {defaultI18n: {t}},
    '../../../../../../packages/client-core/src/server-admin/storage-words': {sourceStateText: () => 'Ready'}, '../../../../../../packages/client-core/src/presentation/errors': {},
    '../../../kit/controls/Button': {Button: 'Button'}, '../../../kit/shell/ServerIdentity': {ServerHeader: 'ServerHeader'}, '../../../kit/states': {StateView: 'StateView'},
    '../../../kit/shell/visual': {shellType: () => ({})}, '../../../kit/theme': {useTheme: () => ({colors: {}, type: {}})},
    './controller': {NativeRemoteSourcesController: class {load = async () => {}; dispose() {} subscribe = () => () => {}; getSnapshot = () => state;}},
    './RemoteSourceForms': {Page: drawn('Page'), SourceWizardContext: context, ConnectionEditor: drawn('Connection'), ReadingEditor: drawn('Reading'), receiptCheckRequired: () => false},
    './SourceLibraryAssignment': {SourceLibraryAssignment: drawn('Assignment')}, './SourceAdvancedEditor': {}, './SourceFolderBrowser': {SourceFolderBrowser: drawn('Folders')},
    './SourceStatusPanel': {}, './AccountConfigEditor': {}, './ImportedSourceEditor': {}, './source-flow': flow, './SourceCompletion': {},
  });
  const get = (type: string) => hosts(host.root).find(node => node.type === type)!;
  try {
    await host.render(React.createElement(module.NativeRemoteSourcesScreen, {ports: {serverName: () => 'Server'}, runtime: {text: {t}}, initialCreate: true}));
    const connect = hosts(host.root).find(node => node.type === 'Button' && node.children.some((text: any) => text.props?.text === 'next.remoteSources.connectWebdav'));
    // Native text is represented as RawText by Fabric; the first wizard choice is WebDAV.
    (connect ?? hosts(host.root).find(node => node.type === 'Button'))!.props.onPress(); await host.settle();
    get('Connection').props.onSaved(source); await host.settle();
    get('Reading').props.onSaved(source); await host.settle();
    get('Folders').props.onSelectionChange([folder]);
    get('Folders').props.wizard.back(); await host.settle();
    get('Reading').props.onSaved(source); await host.settle();
    assert.deepEqual(get('Folders').props.initial, [folder]);
    get('Folders').props.onChoose([folder]); await host.settle();
    const draft = {mode: 'new', name: 'My films', kind: 'anime', target: ''};
    get('Assignment').props.onDraftChange(draft); get('Assignment').props.onChooseFolders(); await host.settle();
    const music = {name: 'Music', path: '/media/Music', relative: 'Music'};
    get('Folders').props.onSelectionChange([folder, music]); get('Folders').props.wizard.back(); await host.settle();
    assert.deepEqual(get('Assignment').props.initialDraft, draft);
    assert.deepEqual(get('Assignment').props.folders, [folder, music]);
    get('Assignment').props.wizard.back(); await host.settle(); get('Folders').props.wizard.back(); await host.settle();
    const next = {...source, generation: 2}; state.sources.sources = [next];
    get('Reading').props.onSaved(next); await host.settle();
    assert.deepEqual(get('Folders').props.initial, [folder, music]); assert.equal(get('Folders').props.source, next);
  } finally {await host.unmount();}
});
