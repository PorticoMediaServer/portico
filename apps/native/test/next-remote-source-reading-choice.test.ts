import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import {fabricHost, React} from './helpers/fabric-host.mjs';
import * as choices from '../../../packages/client-core/src/remote-source-choice.ts';
import * as presentation from '../src-next/screens/settings/remote-sources/reading-presentation.ts';

const t = (key: string) => key;
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.type ? [node, ...nodes(node.children)] : [];
const words = (node: any): string => [node.props?.text ?? '', ...(node.children ?? []).map(words)].join(' ').trim();
const source = {id: 'archive', generation: 1, name: 'Archive'};
const reading = {scanDepth: 'basic', wholeFileReads: false, connections: 4, requestsPerSecond: 4, bytesPerSecond: 0};
const advancedValues = {scanIntervalSeconds: 21600, watchForChanges: false, scanOnStart: true, fullRecheckDays: 0, cacheBytes: 8 * 1024 ** 3, cacheFloorBytes: 1024 ** 3, seekPreviews: false, chapterImages: false, waveforms: false, loudness: false, checksums: false, introsMode: 'off', doubleCheckFolders: false, dirCacheSeconds: 300, readBufferBytes: 0, connectTimeoutSeconds: 30, idleTimeoutSeconds: 60, retries: 3, lowLevelRetries: 3};

async function fixture() {
  const host = await fabricHost(), writes: any[] = [], adopted: any[] = [], recoveries: any[] = [];
  const definitions = ['list', 'once', 'everything', 'ask'].map(name => ({name, definitionRevision: 1, reading, advanced: {...advancedValues, scanIntervalSeconds: name === 'ask' ? 0 : 21600}}));
  const profile = {sourceId: source.id, generation: '1', revision: 1, saved: true, values: {...reading, allowAdvancedMediaAnalysis: true}, configurationGeneration: '1', choiceDefinitions: definitions, choiceApplicableFields: choices.sourceChoiceFields};
  const advanced = {sourceId: source.id, generation: '1', revision: 1, saved: true, values: advancedValues};
  let intent: any, pendingResult: any;
  const controller = {readProfile: async () => profile, readAdvanced: async () => advanced, load: async () => {},
    getSnapshot: () => ({sources: {sources: [source], receipt: null, ambiguous: false}}),
    saveProfile: async (original: unknown, values: unknown) => {writes.push({original, values}); return profile;},
    saveProfileStartChoice: async (original: any, originalAdvanced: any, offered: any, opening: any) => {
      assert.ok(original === profile); assert.ok(originalAdvanced === advanced); assert.ok(definitions.includes(offered)); assert.equal(opening.consentDirty, false); assert.equal(opening.isCurrent(), true);
      writes.push({offered, opening}); const command = choices.sourceChoiceCommand(original, originalAdvanced, offered)!; assert.ok(command);
      intent = {operationId: 'issued-intent', body: {}, command};
      const settings = {...profile, revision: 2, choiceBaseline: {sourceId: source.id, generation: '1', configurationGeneration: '1', name: offered.name, definitionRevision: 1, readRevision: 2, advancedRevision: 2, reading: offered.reading, advanced: offered.advanced, currentAdvanced: offered.advanced, applicableFields: choices.sourceChoiceFields, changedFields: []}};
      const accepted = choices.sourceChoiceAccepted(settings as any, command)!; assert.ok(accepted);
      pendingResult = {kind: 'applied', operationId: intent.operationId, settings, accepted, comparison: choices.sourceChoiceComparison(settings as any)};
      return {kind: 'unknown', intent, error: {code: 'offline'}};
    },
    recoverStartChoice: async (original: any) => {assert.equal(original, intent); recoveries.push(original); return pendingResult;},
  };
  const core = '../../../../../../packages/client-core/src/';
  const module = await screenModule(new URL('../src-next/screens/settings/remote-sources/RemoteSourceForms.tsx', import.meta.url), {'../../../platform/keyboard': {FormScrollView: 'ScrollView', KeyboardProvider: ({children}: {children: unknown}) => children, useKeyboardHeight: () => 0, touchKeyboard: () => true}, '../../../kit/artwork': {StandardBackdrop: 'StandardBackdrop'}, 
    react: {...React, default: React}, 'react-native': {View: 'View', Text: 'Text', ScrollView: 'ScrollView'},
    '../../../../../../packages/i18n/src': {defaultI18n: {t}}, [core + 'remote-source-choice']: choices,
    '../../../kit/controls/Button': {Button: 'Button'}, '../../../kit/controls/TextField': {TextField: 'TextField'}, '../../../kit/controls/Switch': {Switch: 'Switch'},
    '../../../kit/theme': {useTheme: () => ({colors: {}, type: {}})}, './SourceAnalysisConsent': {SourceAnalysisConsent: 'Consent'},
    './SourceReadingControls': {SourceReadingControls: 'ReadingControls'}, './ConnectionTestCard': {}, './reading-presentation': presentation,
    '../../settings-live/owner-libraries/controls': {},
  });
  await host.render(React.createElement(module.ReadingEditor, {controller, source, t, onCancel() {}, onSaved() {}, onAccepted: (value: any) => adopted.push(value), isCurrent: () => true}));
  return {host, writes, adopted, recoveries, profile, definitions, get: (type: string) => nodes(host.root).find(node => node.type === type)!,
    button: (label: string) => nodes(host.root).find(node => node.type === 'Button' && words(node) === label)!};
}

test('Reading dispatches the original offered choice once and recovers its exact unknown intent', async () => {
  const f = await fixture();
  try {
    const select = f.get('ReadingControls').props.onSelect;
    select(f.definitions[3]); select(f.definitions[3]); await f.host.settle();
    assert.equal(f.writes.length, 1); assert.equal(f.writes[0].offered, f.definitions[3]);
    assert.equal(f.get('ReadingControls').props.disabled, true);
    f.button('next.remoteSources.checkResult').props.onPress(); await f.host.settle();
    assert.equal(f.recoveries.length, 1); assert.deepEqual(f.adopted, [source]); assert.equal(f.writes.length, 1);
  } finally {await f.host.unmount();}
});

test('an unedited save omits the recorded consent so it is neither granted nor revoked', async () => {
  const f = await fixture();
  try {
    f.button('next.remoteSources.save').props.onPress(); await f.host.settle();
    assert.equal(f.writes.length, 1);
    assert.equal(Object.hasOwn(f.writes[0].values, 'allowAdvancedMediaAnalysis'), false);
    assert.equal(f.writes[0].values.connections, 4);
  } finally {await f.host.unmount();}
});

test('edited consent blocks choices and is saved explicitly without granting or resetting it through a preset', async () => {
  const f = await fixture();
  try {
    f.get('Consent').props.onChange(false); await f.host.settle();
    assert.equal(f.get('ReadingControls').props.choicesDisabled, true);
    assert.equal(f.button('next.remoteSources.save').props.disabled, true);
    f.button('next.remoteSources.save').props.onPress(); await f.host.settle(); assert.equal(f.writes.length, 0, 'consent is resolved through its separate explicit action');
    f.get('ReadingControls').props.onSelect(f.definitions[3]); await f.host.settle(); assert.equal(f.writes.length, 0);
    f.button('next.remoteSources.saveAnalysisConsent').props.onPress(); await f.host.settle();
    assert.equal(f.writes.length, 1); assert.equal(f.writes[0].original, f.profile);
    assert.equal(f.writes[0].values.allowAdvancedMediaAnalysis, false); assert.equal(f.writes[0].values.connections, 4);
    assert.deepEqual(f.adopted, [source]);
  } finally {await f.host.unmount();}
});
