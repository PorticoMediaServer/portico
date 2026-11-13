import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

// Owner, 13 Nov: Play on in the phone and tablet top bars. It always shows for a signed-in server (the sheet always has This
// device and Link a TV), and while a device is chosen or playing it is drawn in the accent colour and says where.
const t = (key: string, values?: Record<string, string>) => values ? `${key}:${key === 'playOn.nextPlaysOn' ? values.device : Object.values(values).join(',')}` : key;
async function button() {
  return screenModule(new URL('../src-next/shell/PlayOnButton.tsx', import.meta.url), {
    react: {default: {createElement: (type: unknown, props: any, ...children: unknown[]) => ({type, props: {...props, children: children.length > 1 ? children : children[0]}})}, useEffect() {}, useRef: () => ({current: null}), useSyncExternalStore: (_subscribe: unknown, read: () => unknown) => read()}, 'react-native': {View: 'View'},
    '../../../../packages/i18n/src': {defaultI18n: {t}}, '../kit/controls': {Focusable: 'Focusable'}, '../kit/Icon': {Icon: 'Icon'},
    '../kit/layout/TopBar': {RoundBarButton: 'RoundBarButton'}, '../kit/theme': {useTheme: () => ({colors: {}})},
  });
}
const base = {open: false, receivers: [], directoryPhase: 'ready', phase: 'idle', sending: false, remoteOpen: false, cast: {cast: {available: false, phase: 'off'}}};
const tv = {id: 'tv', displayName: 'Living room TV', platform: 'tvos', allowed: true};

test('the top bar Play on always shows for a signed-in server, and names the chosen or playing device', async () => {
  const {playOnButtonState} = await button();
  assert.deepEqual(playOnButtonState(undefined), {visible: false}, 'no Play on controller: nobody signed in to a server, or TV');
  assert.deepEqual(playOnButtonState(base), {visible: true}, 'no account device and no Google Cast yet: still shown (owner, 13 Nov)');
  assert.deepEqual(playOnButtonState({...base, receivers: [tv]}), {visible: true});
  assert.deepEqual(playOnButtonState({...base, cast: {cast: {available: true, phase: 'off'}}}), {visible: true}, 'Google Cast offered on this server and network');
  assert.deepEqual(playOnButtonState({...base, chosen: {kind: 'device', device: tv}}), {visible: true, device: 'Living room TV'});
  assert.deepEqual(playOnButtonState({...base, chosen: {kind: 'cast'}}), {visible: true, device: 'playOn.googleCast'});
  assert.deepEqual(playOnButtonState({...base, remote: {device: 'Kitchen iPad'}}), {visible: true, device: 'Kitchen iPad'}, 'what plays elsewhere now wins over the choice');
  assert.deepEqual(playOnButtonState({...base, cast: {cast: {available: true, phase: 'casting', deviceName: 'Den Chromecast'}}}), {visible: true, device: 'Den Chromecast'});
});

test('the button opens the one Play on sheet from itself, round over art and plain on Home', async () => {
  const {PlayOnButton} = await button();
  const opened: unknown[] = [];
  const playOn = {subscribe: () => () => {}, getSnapshot: () => undefined, readDirectory() {}, open: (anchor: unknown) => opened.push(anchor)};
  assert.equal(PlayOnButton({playOn: undefined, variant: 'round'}), null, 'no controller: no button');
  assert.equal(PlayOnButton({playOn: {...playOn, getSnapshot: () => base}, variant: 'round'}).type, 'RoundBarButton', 'nowhere to play yet: still shown');
  let state: any = {...base, receivers: [tv]};
  const live = {...playOn, getSnapshot: () => state};
  const round = PlayOnButton({playOn: live, variant: 'round'});
  assert.deepEqual([round.type, round.props.icon, round.props.active, round.props.label], ['RoundBarButton', 'i-cast', false, 'playOn.topBar']);
  round.props.onPress(); assert.equal(opened.pop(), round.props.ref, 'the sheet opens from the button itself');
  state = {...state, chosen: {kind: 'device', device: tv}};
  const chosen = PlayOnButton({playOn: live, variant: 'round'});
  assert.deepEqual([chosen.props.active, chosen.props.label], [true, 'playOn.nextPlaysOn:Living room TV'], 'a choice keeps the accent but promises the next play');
  state = {...state, remote: {device: 'Kitchen iPad'}};
  assert.equal(PlayOnButton({playOn: live, variant: 'round'}).props.label, 'player.playingOn:Kitchen iPad', 'real remote playback still says Playing on');
  state = {...state, remote: undefined};
  const plain = PlayOnButton({playOn: live, variant: 'plain'});
  assert.deepEqual([plain.type, plain.props.accessibilityLabel], ['Focusable', 'playOn.nextPlaysOn:Living room TV']);
});
