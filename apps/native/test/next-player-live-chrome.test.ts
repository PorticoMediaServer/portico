import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {playbackFence, sameFence} from '../../../packages/player-core/src/identity.ts';
import {followedByScene} from '../../../packages/player-core/src/segment-skip.ts';
import * as landingStoreModule from '../src-next/screens/watch/landing-store.ts';

const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.children)] : [];
const strings = (value: any): string => Array.isArray(value) ? value.map(strings).join(' ') : value?.props ? strings(value.props.children) : value == null || value === false ? '' : String(value);
const noopSubscription = () => () => {};
const scope = {serverId: 'server', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane', authority: 'local'} as const;
const channelFence = playbackFence({kind: 'channel', playback: {scope, run: 1}, sessionId: 'session', channelKind: 'live-source', sourceId: 'source', channelId: 'channel', channelGeneration: 'generation', generation: 1});
const queueFence = playbackFence({kind: 'queue', playback: {scope, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'version', entryId: 'entry', generation: 1});
const t = (key: string, values?: Record<string, unknown>) => `${key}${values ? ` ${Object.values(values).join(' ')}` : ''}`;
const theme = {isTV: false, textScale: 1, colors: {text: '#edf5fa', textSecondary: '#bdccd6', textTertiary: '#96a9b5', record: '#f17979'}, type: {body: {}, bodyStrong: {}, caption: {}, captionStrong: {}}};

async function fixture(input: {compact?: boolean; bar?: boolean; tabletop?: 'column' | 'stacked'; queue?: boolean} = {}) {
  const h = hooks(), calls: unknown[][] = [], controlState: any = {fence: input.queue ? queueFence : channelFence, visible: true};
  const fence = controlState.fence;
  const row = {kind: 'live', id: 'channel', sourceId: 'source', number: '202', name: 'Summit Sports Extra', tuneAvailable: true, recordAvailable: true, mark: {text: 'SSE', seed: 'channel'}, group: '', favorite: false, guide: 'full'};
  const previous = {channel: {...row, id: 'previous', number: '201'}, step: -1, programmes: []};
  const next = {channel: {...row, id: 'next', number: '301'}, step: 1, programmes: []};
  let guideState: any = {fence, phase: 'ready', open: false, busy: false, rows: [previous, {channel: row, step: 0, programmes: []}, next], previous, next, timezone: 'UTC'};
  let actionState: any = {fence, offer: {id: 1, channel: row, programme: {title: 'Cycling: Mountain Stage'}}, busy: false};
  let source: any = {fence, retired: false, dock: {local: {expanded: true}}, queue: {}, capabilities: {play: true, pause: true, seek: true, audioTrack: true, subtitleTrack: true},
    playback: {intent: 'playing', positionSeconds: 30, duration: 60,
      ...(!input.queue ? {linear: {name: row.name, media: {liveEdgeUs: '60000000'}, programme: {title: 'Cycling: Mountain Stage', startMs: 0, endMs: 3600000}}} : {})}};
  const controls = {subscribe: noopSubscription, getSnapshot: () => controlState, reveal: () => {}, canFullWindow: () => false, hide: () => {controlState.visible = false;},
    open: (panel: string) => {calls.push(['panel', panel]);return true;}, toggleVisibility: () => {}, holdOverlay: () => {}};
  const playback = {getSnapshot: () => source, toggle: async (f: unknown) => {calls.push(['toggle', f]);}, seek: async (f: unknown, at: number) => {calls.push(['seek', f, at]);},
    skip: async (f: unknown, delta: number) => {calls.push(['skip', f, delta]);}, previous: async () => {calls.push(['previous']);}, next: async () => {calls.push(['next']);},
    replay: async (f: unknown) => {calls.push(['replay', f]);}, stop: async (f: unknown) => {calls.push(['stop', f]);},
    policySkip: async (f: unknown, id: string) => {calls.push(['policy-skip', f, id]);}, policyUndo: async (f: unknown) => {calls.push(['policy-undo', f]);},
    policyAdvance: async (f: unknown, mode: string) => {calls.push(['policy-advance', f, mode]);}, policyDismiss: (f: unknown) => {calls.push(['policy-dismiss', f]);return true;},
    policyCancelAutoplay: (f: unknown) => {calls.push(['policy-cancel', f]);}};
  const guide = {subscribe: noopSubscription, getSnapshot: () => guideState, restoreProof: () => () => true,
    tune: async (row: unknown, state: unknown) => {calls.push(['tune', row, state]);}, close: () => {}, fullGuide: () => {}};
  const actions = {subscribe: noopSubscription, getSnapshot: () => actionState, restoreProof: () => () => true,
    record: async (state: unknown) => {calls.push(['record', state]);}, openMore: (state: unknown) => {calls.push(['more', state]);}, close: () => {}};
  const {NativeWatchChrome} = await screenModule(new URL('../src-next/screens/watch/WatchChrome.tsx', import.meta.url), {'../../platform/tv-keys': {useTVKeyEvent: () => {}}, 
    react: {...h.react, useLayoutEffect: () => {}}, 'react-native': {View: 'View', Text: 'Text', useTVEventHandler: () => {}},
    'react-native-reanimated': {default: {View: 'Animated'}, Easing: {linear: (x: number) => x}, cancelAnimation() {}, useAnimatedStyle: (run: () => unknown) => run(), useSharedValue: (value: unknown) => h.react.useRef({value}).current, withTiming: (value: unknown) => value},
    'react-native-gesture-handler': {Gesture: {Pan: () => ({enabled: () => ({})})}, GestureDetector: 'GestureDetector'},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'Gradient', Path: 'Path', Rect: 'Rect', Stop: 'Stop', Text: 'SvgText'},
    '../../../../../packages/player-core/src': {sameFence, UNDO_SECONDS: 6, followedByScene}, '../../../../../packages/client-core/src/playback/linear-protocol': {channelSeconds: (value: string) => Number(value) / 1_000_000},
    '../../../../../packages/client-core/src/presentation/kit-model': {channelTint: () => '#7d5a2a', playbackIdentity: () => undefined},
    '../../../../../packages/client-core/src/presentation/kit-model/text': {clockText: String, durationText: (seconds: number) => `${Math.round(seconds / 60)} min`}, '../../../../../packages/i18n/src': {defaultI18n: {t}},
    '../../kit/controls': {Button: 'Button', Focusable: 'Focusable'}, '../../kit/focus': {FocusRegion: 'FocusRegion', useFocusRegistry: () => ({subscribe: noopSubscription, getSnapshot: () => ({}), capture: () => undefined})},
    '../../kit/Icon': {Icon: 'Icon'}, '../../kit/theme': {useTheme: () => theme, nativeType: () => ({fontFamily: 'Manrope-Regular'})},
    '../../kit/useScreenReader': {useScreenReader: () => false},
    '../../kit/useMotion': {useMotion: () => ({reduce: true, timing: () => ({}), duration: () => 0}), useReducedMotion: () => true},
    '../../kit/dock/PlayerMorphBridge': {useInitialPlayerPaint: () => undefined, usePublishedPlayerMorph: () => undefined},
    './TVControlLabel': {TVControlLabel: 'TVControlLabel'}, './landing-store': landingStoreModule, '../settings-live/GroupPill': {NativeTVGroupPill: 'GroupPill'}, './controls': {}, './WatchTimeline': {NativeWatchTimeline: 'Timeline'},
    './useSkipPromptMotion': {useSkipPromptMotion: (value: unknown) => ({value, interactive: !!value, bodyStyle: {}, positionStyle: {}})}, './UpNextCard': {NativeUpNextCard: 'UpNextCard'}, './StillWatchingCard': {NativeStillWatchingCard: 'StillWatchingCard'}, '../../kit/artwork': {Artwork: 'Artwork'},
    './live-guide': {emptyLiveGuide: () => ({}), noLiveGuideSubscription: noopSubscription}, './live-actions': {emptyLiveActions: () => ({}), noLiveActionsSubscription: noopSubscription},
    '../../../../../packages/client-core/src/guide/now-next': {nowNext: () => ({})}, '../../../../../packages/client-core/src/playback-messages': {playbackUserMessage: String},
    './TVMiniGuide': {NativeMiniGuide: 'MiniGuide'}, './TVLiveActions': {TVLiveActions: 'LiveActions'},
  });
  const render = () => h.render(() => NativeWatchChrome({source, controls, playback, text: {locale: 'en-CA', t}, quality: 'Auto 720p',
    width: input.bar ? 1180 : input.compact ? 390 : 844, height: input.bar ? 820 : input.compact ? 219.375 : 390, compact: !!input.compact, bar: input.bar, tabletop: input.tabletop,
    skipBackSeconds: 15, skipForwardSeconds: 45, onMinimize() {}, onGuide: () => {calls.push(['guide']);},
    onCast: (f: unknown, anchor: unknown) => {calls.push(['cast', f, anchor]);}, liveGuide: input.queue ? undefined : guide, liveActions: input.queue ? undefined : actions, reportError: assert.fail}));
  return {render, calls, fence, row, previous, next, source: () => source, guideState: () => guideState, actionState: () => actionState,
    changeSource: (value: any) => {source = {...source, ...value};}, changeGuide: (value: any) => {guideState = {...guideState, ...value};}, changeActions: (value: any) => {actionState = {...actionState, ...value};}};
}

for (const input of [{name: 'compact phone', compact: true}, {name: 'landscape phone'}, {name: 'expanded tablet', bar: true}, {name: 'tabletop', tabletop: 'column' as const}]) {
  test(`${input.name} exposes same-session Guide, adjacent channels and real Record/More actions`, async () => {
    const f = await fixture(input), tree = f.render(), all = nodes(tree);
    const down = all.find(node => node.props.testID === 'watch-channel-down'), up = all.find(node => node.props.testID === 'watch-channel-up');
    assert.ok(down);assert.ok(up);assert.equal(down.props.style.width, input.bar ? 48 : 46);assert.equal(up.props.style.width, input.bar ? 48 : 46);
    const guide = all.find(node => node.props.testID === 'watch-chip-episodes');assert.ok(guide);
    if (input.compact) {
      const pills = all.find(node => node.props.testID === 'watch-live-pills');assert.ok(pills);
      assert.ok(nodes(pills).includes(guide));assert.ok(nodes(pills).some(node => node.props.testID === 'watch-option-quality'));
    }
    guide.props.onPress();down.props.onPress();up.props.onPress();
    assert.deepEqual(f.calls.slice(0, 3), [['guide'], ['tune', f.previous, f.guideState()], ['tune', f.next, f.guideState()]]);
    const actions = all.find(node => node.type === 'LiveActions');assert.ok(actions);assert.ok(actions.props.record);assert.ok(actions.props.more);
    actions.props.record.onPress();actions.props.more.onPress();assert.deepEqual(f.calls.slice(3), [['record', f.actionState()], ['more', f.actionState()]]);
    assert.equal(all.some(node => node.props.testID === 'watch-next' || node.props.testID === 'watch-previous'), false);
    const calls = f.calls.length;f.changeGuide({open: true});
    down.props.onPress();up.props.onPress();guide.props.onPress();actions.props.record.onPress();actions.props.more.onPress();
    assert.equal(f.calls.length, calls, 'retained controls cannot dispatch under the mini guide');
  });
}

test('expanded live tablet shows supplied programme, channel mark/number and time range', async () => {
  const f = await fixture({bar: true}), block = nodes(f.render()).find(node => node.props.testID === 'watch-live-identity');assert.ok(block);
  const text = strings(block);assert.match(text, /Cycling: Mountain Stage/);assert.match(text, /Summit Sports Extra/);assert.match(text, /SSE/);assert.match(text, /202/);assert.match(text, /next\.kit\.range/);
  f.changeSource({playback: {...f.source().playback, linear: {...f.source().playback.linear, programme: undefined}}});
  const fallback = strings(nodes(f.render()).find(node => node.props.testID === 'watch-live-identity'));assert.match(fallback, /Summit Sports Extra/);assert.doesNotMatch(fallback, /next\.kit\.range/);
});

test('mobile live actions omit Record without the current server offer and keep library-channel More', async () => {
  const f = await fixture();f.changeActions({offer: {...f.actionState().offer, channel: {...f.row, kind: 'library', recordAvailable: false}}});
  const actions = nodes(f.render()).find(node => node.type === 'LiveActions');assert.equal(actions.props.record, undefined);assert.ok(actions.props.more);
  f.changeActions({fence: {...f.fence, sessionId: 'retired'}});assert.equal(nodes(f.render()).some(node => node.type === 'LiveActions'), false);
});

test('WatchChrome passes the actual Cast opener ref and rejects a retired playback handler', async () => {
  const f = await fixture({queue: true, bar: true}), button = nodes(f.render()).find(node => node.props.testID === 'watch-cast');
  const view = {measureInWindow() {}};button.props.ref.current = view;button.props.onPress();
  assert.equal(f.calls[0]![0], 'cast');assert.equal(f.calls[0]![1], f.fence);assert.equal((f.calls[0]![2] as any).current, view);
  f.changeSource({fence: {...f.fence, generation: 2}});button.props.onPress();assert.equal(f.calls.length, 1);
});

test('Chrome renders only supplied policy prompts and dispatches canonical skip, Undo, and advance', async () => {
  const f = await fixture({queue: true}), marker = {id: 'intro', kind: 'intro', startSeconds: 10, endSeconds: 30, automaticSafe: true};
  const base = {prompt: null, undo: null, markers: [marker], finished: false, replay: false, attention: null};
  f.changeSource({options: {itemId: 'item', versions: [], markers: [{id: marker.id, type: marker.kind, startMs: 10000, endMs: 30000}]}});
  f.render();f.changeSource({playback: {...f.source().playback, positionSeconds: 29}});
  assert.equal(nodes(f.render()).some(node => node.props.testID === 'watch-skip-undo'), false, 'a position jump is never guessed to be an automatic skip');
  f.changeSource({policy: {...base, prompt: {kind: 'skip', marker}}});nodes(f.render()).find(node => node.props.testID === 'watch-skip-action').props.onPress();assert.deepEqual(f.calls.at(-1), ['policy-skip', f.fence, 'intro']);
  f.changeSource({policy: {...base, undo: {marker, remainingSeconds: 3}}});const undo = nodes(f.render()).find(node => node.props.testID === 'watch-skip-undo');undo.props.onPress();assert.deepEqual(f.calls.at(-1), ['policy-undo', f.fence]);
  f.changeSource({policy: {...base, undo: {marker: {...marker, id: 'other'}, remainingSeconds: 2}}});const count = f.calls.length;undo.props.onPress();assert.equal(f.calls.length, count, 'a retained Undo never undoes a different policy marker');
  const countdown = {seconds: 4, automatic: true}, offer = {id: 'credits', kind: 'credits', marker: {...marker, id: 'credits', kind: 'credits'}}, next = {entryId: 'next', title: 'Next episode'};
  f.changeSource({playback: {...f.source().playback, intent: 'paused'}, queue: {view: {items: [next], next: {entryId: 'next', available: true}, postPlay: {countdownSeconds: 10}}}, policy: {...base, prompt: {kind: 'up-next', offer, countdown, sceneAfterCredits: false}}});
  const card = nodes(f.render()).find(node => node.type === 'UpNextCard');assert.equal(card.props.countdown, countdown);card.props.onWatchCredits();assert.deepEqual(f.calls.at(-1), ['policy-dismiss', f.fence]);card.props.onPlayNow();assert.deepEqual(f.calls.at(-1), ['policy-advance', f.fence, 'manual']);
  const advanced = f.calls.length;f.changeSource({queue: {view: {items: [], next: {entryId: 'other'}, postPlay: {}}}});card.props.onPlayNow();assert.equal(f.calls.length, advanced, 'the retained next card does not start a replacement next entry');
});

test('mobile Record/More renderer keeps actual availability, busy and recording state', async () => {
  const h = hooks(), {TVLiveActions} = await screenModule(new URL('../src-next/screens/watch/TVLiveActions.tsx', import.meta.url), {react: h.react, 'react-native': {View: 'View'}, '../../../../../packages/i18n/src': {defaultI18n: {t}}, '../../kit/controls': {Button: 'Button'}, '../../kit/theme': {useTheme: () => theme}});
  const record = {label: 'Recording', onPress() {}, busy: true, disabled: true, recording: true}, more = {onPress() {}};
  const compact = nodes(h.render(() => TVLiveActions({enabled: true, record, more, size: 46}))), rec = compact.find(node => node.props.testID === 'watch-live-record');
  assert.equal(rec.props.iconOnly, true);assert.equal(rec.props.accessibilityLabel, record.label);assert.equal(rec.props.loading, true);assert.equal(rec.props.disabled, true);assert.equal(rec.props.style.height, 46);
  const wide = nodes(h.render(() => TVLiveActions({enabled: true, record, more, size: 48, compact: false})));assert.equal(wide.find(node => node.props.testID === 'watch-live-record').props.iconOnly, false);
  assert.equal(h.render(() => TVLiveActions({enabled: false, record, more})), null);assert.equal(nodes(h.render(() => TVLiveActions({enabled: true, more}))).some(node => node.props.testID === 'watch-live-record'), false);
});

test('ended and still-watching cards dispatch only their current canonical actions', async () => {
  const f = await fixture({queue: true}), next = {entryId: 'next', title: 'Next episode'}, countdown = {seconds: 7, automatic: true};
  const policy = {prompt: null, undo: null, markers: [], finished: true, replay: false, attention: null, endCountdown: countdown};
  f.changeSource({queue: {view: {items: [next], next: {entryId: 'next', available: true}, postPlay: {countdownSeconds: 10}}}, policy});
  const card = nodes(f.render()).find(node => node.type === 'UpNextCard');
  assert.ok(card, 'end-of-item countdown needs a card even when the policy has no credits prompt');
  assert.equal(card.props.countdown, countdown);assert.equal(card.props.sceneAfter, false);
  card.props.postPlay.onCancel();assert.deepEqual(f.calls.at(-1), ['policy-cancel', f.fence]);
  card.props.postPlay.onReplay();assert.deepEqual(f.calls.at(-1), ['replay', f.fence]);
  card.props.postPlay.onClose();assert.deepEqual(f.calls.at(-1), ['stop', f.fence]);
  card.props.onPlayNow();assert.deepEqual(f.calls.at(-1), ['policy-advance', f.fence, 'manual']);
  f.changeSource({policy: {...policy, endCountdown: undefined, attention: 'still-watching'}});
  const count = f.calls.length;card.props.onPlayNow();card.props.postPlay.onCancel();assert.equal(f.calls.length, count, 'the retained ordinary end card cannot confirm a new still-watching check');
  // phone-player 6e: on a handheld the check is its own centred question, not the Up next card.
  const rendered = nodes(f.render());
  assert.equal(rendered.some(node => node.type === 'UpNextCard'), false);
  const waiting = rendered.find(node => node.type === 'StillWatchingCard');
  assert.ok(waiting); assert.equal(waiting.props.item, next);
  waiting.props.onConfirm();assert.deepEqual(f.calls.at(-1), ['policy-advance', f.fence, 'still-watching']);
  waiting.props.onReplay();assert.deepEqual(f.calls.at(-1), ['replay', f.fence]);
  waiting.props.onClose();assert.deepEqual(f.calls.at(-1), ['stop', f.fence]);
  f.changeSource({dock: {local: {expanded: false}}});const collapsed = f.calls.length;waiting.props.onConfirm();waiting.props.onReplay();waiting.props.onClose();assert.equal(f.calls.length, collapsed, 'a retained check cannot act after the same session collapses');
  f.changeSource({dock: {local: {expanded: true}}, retired: true});waiting.props.onReplay();waiting.props.onClose();assert.equal(f.calls.length, collapsed, 'runtime retirement closes retained actions even if its last fence is still published');
  f.changeSource({retired: false});
  f.changeSource({policy: {...policy, attention: 'playback-error'}});assert.equal(nodes(f.render()).some(node => node.type === 'UpNextCard' || node.type === 'StillWatchingCard'), false);
  f.changeSource({fence: {...f.fence, generation: 2}});const changed = f.calls.length;waiting.props.onConfirm();waiting.props.onReplay();waiting.props.onClose();assert.equal(f.calls.length, changed);
});

test('the existing next card renders end actions without a credits action or an invented countdown', async () => {
  const h = hooks(), calls: string[] = [];
  const {NativeUpNextCard} = await screenModule(new URL('../src-next/screens/watch/UpNextCard.tsx', import.meta.url), {
    react: h.react, 'react-native': {View: 'View', Text: 'Text', StyleSheet: {absoluteFill: {position: 'absolute', inset: 0}}},
    '../../../../../packages/client-core/src/presentation/kit-model/text': {clockText: String, durationText: (seconds: number) => `${Math.round(seconds / 60)} min`}, '../../../../../packages/i18n/src': {defaultI18n: {t}},
    '../../kit/artwork': {Artwork: 'Artwork'}, '../../kit/controls': {Button: 'Button'}, '../../platform/native-blur': {BlurView: 'BlurView', glassRecipe: () => ({nativeBlur: false})}, '../../kit/theme': {useTheme: () => theme},
  });
  const props = {item: {entryId: 'next', title: 'Next episode'}, compact: true, countdown: null, countdownTotal: 10, sceneAfter: false,
    onPlayNow: () => calls.push('play'), onWatchCredits: () => calls.push('watch'), onSkipCredits: () => calls.push('skip'),
    postPlay: {onCancel: () => calls.push('cancel'), onReplay: () => calls.push('replay'), onClose: () => calls.push('close')}};
  const render = (countdown: unknown) => h.render(() => NativeUpNextCard({...props, countdown}));
  const noCountdown = nodes(render(null));
  assert.deepEqual(noCountdown.filter(node => node.type === 'Button').map(node => node.props.testID), ['watch-up-next-play', 'watch-up-next-replay', 'watch-up-next-close']);
  const waiting = render({seconds: 6.75, automatic: true});assert.match(strings(waiting), /player.post.playingIn 7/);
  nodes(waiting).find(node => node.props.testID === 'watch-up-next-cancel').props.onPress();assert.deepEqual(calls, ['cancel']);
  assert.equal(nodes(waiting).some(node => node.props.testID === 'watch-up-next-watch'), false);
  const scene = h.render(() => NativeUpNextCard({...props, compact: false, postPlay: undefined, sceneAfter: true, countdown: {seconds: 90, automatic: false}}));
  assert.ok([...nodes(waiting), ...nodes(scene)].filter(node => node.type === 'Text').every(node => typeof node.props.style.fontFamily === 'string'), 'every inline card label carries its native Manrope family');
});

test('the still-watching check names the next episode and offers only its three actions', async () => {
  const h = hooks(), calls: string[] = [];
  const {NativeStillWatchingCard} = await screenModule(new URL('../src-next/screens/watch/StillWatchingCard.tsx', import.meta.url), {
    react: h.react, 'react-native': {View: 'View', Text: 'Text', StyleSheet: {absoluteFill: {position: 'absolute', inset: 0}}},
    '../../../../../packages/i18n/src': {defaultI18n: {t}}, '../../kit/controls': {Button: 'Button'}, '../../platform/native-blur': {BlurView: 'BlurView'},
    '../../kit/theme': {useTheme: () => theme, nativeType: (value: any) => ({fontFamily: 'Manrope', fontSize: value.size})},
  });
  const tree = nodes(h.render(() => NativeStillWatchingCard({item: {entryId: 'next', title: 'Bad Tradecraft', episode: {seasonNumber: 1, number: 3}},
    onConfirm: () => calls.push('confirm'), onReplay: () => calls.push('replay'), onClose: () => calls.push('close')})));
  const text = strings(tree);
  for (const key of ['player.post.stillWatching', 'player.post.upNext', 'Bad Tradecraft', 'player.post.pausedAfterEpisodes']) assert.match(text, new RegExp(key.replace('.', '\\.')));
  const buttons = tree.filter(node => node.type === 'Button');
  assert.deepEqual(buttons.map(node => node.props.testID), ['watch-still-confirm', 'watch-still-replay', 'watch-still-close']);
  for (const button of buttons) button.props.onPress();
  assert.deepEqual(calls, ['confirm', 'replay', 'close']);
  assert.ok(tree.filter(node => node.type === 'Text').every(node => [node.props.style].flat().some((style: any) => style?.fontFamily)), 'every label carries Manrope');
});
