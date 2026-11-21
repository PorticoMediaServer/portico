import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

const flat = (node: any): any[] => !node || typeof node !== 'object' ? []
  : Array.isArray(node) ? node.flatMap(flat) : [node, ...flat(node.props?.children), ...flat(node.props?.foot)];

async function sheet(model: Record<string, unknown>) {
  const calls: unknown[] = [], host = hooks();
  const value = {permit: undefined, close: () => calls.push('close'), choose: async (quality: string) => {calls.push(quality);},
    editable: true, policy: 'all', keepNext: 3, changePolicy: (id: string) => calls.push(id), changeKeepNext: (n: number) => calls.push(n),
    options: [{quality: 'original', label: 'Original', available: true, size: 'about 21 GB'}, {quality: '720p', label: '720p', available: false}],
    episodic: true, done: undefined, acceptedSingle: false, problem: undefined, checking: false, refused: undefined, missingDevice: false,
    storage: '120 GB left', viewDownloads: () => calls.push('view'), ...model};
  const request = {title: 'Severance, Season 1'};
  const {NativeDownloadSheet} = await screenModule(new URL('../src-next/pages/touch/downloads/DownloadSheet.tsx', import.meta.url), {
    react: host.react,
    '../../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../../../../../../packages/client-core/src/download-requests': {},
    '../../../kit/controls': {Button: 'Button'}, '../../../kit/frames': {SectionCard: 'SectionCard', SheetFrame: 'SheetFrame'},
    '../../../kit/overlays/SurfacePaintHost': {RetainedOverlay: 'RetainedOverlay'},
    '../../../kit/saved/DownloadChoiceRows': {DownloadChoiceRow: 'Choice', DownloadQualityRow: 'Quality'},
    '../../../kit/states': {StateView: 'StateView'},
    '../../../screens/downloads/ReadingDownloadSheet': {NativeReadingDownloadSheet: 'Reading'},
    '../../../screens/downloads/request': {getNativeDownloadSnapshot: () => request, nativeDownloadRequestKey: () => 'k', subscribeNativeDownload: () => () => {}},
    '../../shared/downloads/useDownloadChoice': {useDownloadChoice: () => value},
  });
  const outer = host.render(() => NativeDownloadSheet({context: {}}));
  const tree = host.render(() => outer.type(outer.props));
  return {nodes: flat(tree), calls};
}

test('a season: Which episodes and Quality as cards; choosing a policy or a quality goes to the hook', async () => {
  const {nodes, calls} = await sheet({});
  const frame = nodes.find(node => node.type === 'SheetFrame');
  assert.equal(frame.props.subtitle, 'Severance, Season 1');
  assert.deepEqual(nodes.filter(node => node.type === 'SectionCard').map(node => node.props.label), ['downloads.whichEpisodes', 'downloads.qualityHeading']);
  nodes.find(node => node.type === 'Choice' && node.props.testID === 'download-policy-next').props.onPress();
  const qualities = nodes.filter(node => node.type === 'Quality');
  assert.deepEqual(qualities.map(node => node.props.available), [true, false]);
  qualities[0].props.onPress();
  assert.deepEqual(calls, ['next', 'original']);
});

test('once added it says so, with Close and View downloads at the foot', async () => {
  const {nodes, calls} = await sheet({done: 'It will download as soon as the server has it ready.', acceptedSingle: true});
  assert.ok(nodes.some(node => node.type === 'StateView' && node.props.title === 'downloads.addedMany'));
  assert.equal(nodes.some(node => node.type === 'SectionCard'), false);
  const buttons = nodes.filter(node => node.type === 'Button');
  buttons[1].props.onPress(); buttons[0].props.onPress();
  assert.deepEqual(calls, ['view', 'close']);
});
