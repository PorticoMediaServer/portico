/** G38: Watch Together names a public member by their username, then by the name they
 * chose for this group only when the two differ. Activity-private members stay unnamed. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {memberNames} from '../src-next/screens/settings-live/together-presentation';

const privateLabel = 'Private viewer';

test('a member who keeps activity private is never named', () => {
  assert.deepEqual(memberNames({private: true, name: 'Sam Rivera', username: 'sam'}, privateLabel), {primary: privateLabel});
});

test('a public member whose username matches their group name is named once', () => {
  assert.deepEqual(memberNames({private: false, name: 'sam', username: 'sam'}, privateLabel), {primary: 'sam'});
  assert.deepEqual(memberNames({private: false, name: ' sam ', username: 'sam'}, privateLabel), {primary: 'sam'});
});

test('a public member whose username differs keeps the group name as the quiet second line', () => {
  assert.deepEqual(memberNames({private: false, name: 'Sam Rivera', username: 'sam'}, privateLabel), {primary: 'sam', secondary: 'Sam Rivera'});
});

test('a public member without a username is named by their group name', () => {
  assert.deepEqual(memberNames({private: false, name: 'Sam Rivera'}, privateLabel), {primary: 'Sam Rivera'});
  assert.deepEqual(memberNames({private: false, name: 'Sam Rivera', username: '  '}, privateLabel), {primary: 'Sam Rivera'});
});

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const children = (node: any): any[] => node?.props?.children === undefined ? [] : Array.isArray(node.props.children) ? node.props.children : [node.props.children];
const label = (node: any): string => Array.isArray(node?.props?.children) ? node.props.children.join('') : String(node?.props?.children ?? '');
const t = (key: string, values?: {name?: string}) => values?.name ? `${key}: ${values.name}` : key;

async function pillTree() {
  const host = hooks();
  const react = {...host.react, Fragment: 'Fragment', useLayoutEffect: () => {}};
  const theme = {isTV: true, colors: {text: 'text', textSecondary: 'secondary', textTertiary: 'tertiary', accentStrong: 'accent', raisedHigh: 'high', amber: 'amber', healthy: 'healthy'}};
  const member = (value: object) => ({host: false, you: false, status: 'in_step', private: false, ...value});
  const group = {id: 'grp', name: 'Night', you: {memberId: 'me', host: false, mayControl: false, mayBecomeHost: false},
    host: {present: true}, clock: {ready: 3, waitingFor: 0},
    members: [member({id: 'me', name: 'Me Myself'}), member({id: 'public', name: 'Sam Rivera', username: 'sam'}), member({id: 'private', name: 'Hidden Person', private: true})]};
  const owner = {subscribe: () => () => {}, current: () => true, getSnapshot: () => ({phase: 'live', group}), makeHost: async () => {}};
  const {NativeTVGroupPill} = await screenModule(new URL('../src-next/screens/settings-live/GroupPill.tsx', import.meta.url), {
    react: {...react, default: react}, 'react-native': {Text: 'Text', View: 'View', findNodeHandle: () => undefined},
    '../../../../../packages/i18n/src': {defaultI18n: {t}}, '../../kit/controls': {Button: 'Button', Focusable: 'Focusable'},
    '../../kit/artwork': {ProfileFace: 'ProfileFace'}, '../../kit/Icon': {Icon: 'Icon'}, '../../kit/overlays': {SidePanel: 'SidePanel'},
    '../../kit/theme': {useTheme: () => theme}, '../../kit/browse/presentation': {browseType: () => ({})},
    './group-port': {groupPort: () => owner, subscribeGroupPort: () => () => {}}, './together-presentation': {memberNames},
  });
  return host.render(() => NativeTVGroupPill({}));
}

test('the people sheet shows the username first and the group name beneath it', async () => {
  const tree = await pillTree();
  const column = nodes(tree).find(node => node.type === 'View' && children(node).some(child => child?.type === 'Text' && label(child) === 'Sam Rivera'));
  assert.ok(column, 'the public member is listed with their group name');
  assert.deepEqual(children(column).filter(child => child?.type === 'Text').map(label), ['sam', 'Sam Rivera'],
    'the username is the primary name and the group name is the quieter second line');
  const privateRow = nodes(tree).find(node => node.type === 'View' && children(node).some(child => child?.type === 'Text' && label(child) === 'together.privateViewer'));
  assert.ok(privateRow, 'the private member is still the private viewer');
  assert.deepEqual(children(privateRow).filter(child => child?.type === 'Text').map(label), ['together.privateViewer'], 'a private member has no second name');
  assert.deepEqual(nodes(tree).filter(node => node.type === 'Text').map(label).filter(text => text === 'Hidden Person'), [],
    'the name of a member who keeps activity private is not drawn');
});

test('an unavailable Room occurrence keeps its unavailable queue-sheet title and offers no Watch action', async () => {
  const {roomFixture} = await import('./helpers/room-fixture');
  const protocol = await import('../../../packages/client-core/src/room-protocol');
  const h = hooks(), react = {...h.react, default: h.react};
  const group = roomFixture({queue: [{entryId: 'redacted', available: false}]}), state = {phase: 'live', group};
  const controller = {serverNow: () => Date.now(), getSnapshot: () => state, currentGroup: () => group,
    lifetime: {authority: {}, current: () => true}};
  // The TV room's logic (pages/shared/together/useTogetherTVRoom): the page passes the row and the menu as they are given.
  const {useTogetherTVRoom} = await screenModule(new URL('../src-next/pages/shared/together/useTogetherTVRoom.ts', import.meta.url), {
    react, '../../../../../../packages/i18n/src': {defaultI18n: {t, clock: String, time: String}},
    '../../../../../../packages/client-core/src/room-protocol': protocol,
    '../../../../../../packages/client-core/src/presentation/kit-model': {}, '../../../../../../packages/client-core/src/timer': {repeat: () => ({stop() {}})},
    '../../../kit/focus': {useOptionalFocusRegistry: () => undefined},
    '../../../shell/model': {sameAuthority: () => false}, '../../../shell/tv-focus': {tvFocusIds: () => ({surface: (id: string) => id, rail: 'rail'})},
    '../../../screens/settings-live/group-port': {groupPort: () => undefined}, '../../../screens/settings-live/Screen': {useScreenEntry: () => undefined},
    '../../../screens/settings-live/together-presentation': {memberNames},
  });
  const playback = {subscribe: () => () => {}, getSnapshot: () => ({playback: {}})};
  const media = {subscribe: () => () => {}, getSnapshot: () => ({items: {}}), select: () => {}};
  const runtime = {text: {t}, artwork: () => undefined, getSnapshot: () => ({profile: {}})};
  const ports = {context: {playback, navigation: {getSnapshot: () => ({route: {kind: 'together'}})}}};
  const render = () => h.render(() => useTogetherTVRoom({state, group, controller, media, runtime, ports}));
  const row = render().queueRow(group.queue[0], 0);
  assert.equal(row.title, 'web.together.notAvailable');
  row.onPress();
  const room = render();
  assert.equal(room.queueMenu, 'redacted');
  assert.equal(room.queueMenuTitle, 'web.together.notAvailable');
  assert.deepEqual(room.queueMenuItems.map((item: any) => item.id), ['remove']);
});
