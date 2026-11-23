import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {Dimensions, Platform, type View} from 'react-native';
import {compositePatch, settingsRowId, type SettingsPage, type SettingsRow as Row} from '../../../../../../packages/client-core/src/presentation/settings-structure';
import type {PreferenceScope} from '../../../../../../packages/client-core/src/preferences';
import {resetRecommendations} from '../../../../../../packages/client-core/src/recommendation-feedback';
import type {MessageId} from '../../../../../../packages/i18n/src';
import {useTheme} from '../../../kit/theme';
import type {LanguageOption} from '../../../kit/shell';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeSettingsController} from '../../../screens/settings-live/settings-controller';
import type {NativeDevicePreferences} from '../../../screens/settings-live/device-preference-consumer';
import {passthroughFormats} from '../../../platform/playback/passthrough-formats';
import {nativeDynamicRangeMatchingAvailable} from '../../../platform/playback/dynamic-range-matching';
import {nativeEqualizer} from '../../../platform/playback/equalizer';
import {nativeDynamicRangeFocusId, nativeFrameRateFocusId, nativeLossNoticesFocusId, nativePassthroughFocusId, nativeSafeModeAvailable, nativeSafeModeFocusId} from './useDeviceRows';
import type {MutableRefObject} from 'react';

/** playback-info frame 13b draws one hint under each Player choice; the runtime settings
 * structure has no hint field yet, so the drawn copy is attached by the one device row that
 * uses it. The labels remain the spec's own. */
export const playerEngineHints: Readonly<Record<string, MessageId>> = Object.freeze({
  automatic: 'next.settingsSpec.account.device.advanced.groups.3.rows.0.control.options.0.hint',
  native: 'next.settingsSpec.account.device.advanced.groups.3.rows.0.control.options.1.hint',
  mpv: 'next.settingsSpec.account.device.advanced.groups.3.rows.0.control.options.2.hint',
});
/** One Player choice opening: the exact page, route snapshot, controller and device-preference
 * source that offered it, with the choices that were on screen. Every select and close proves
 * this identity, so no retained opener or late item can write or close a successor. */
type EngineChooser = Readonly<{
  token: number; page: string; revision: number;
  route: unknown; controller: unknown; preferences: unknown; mount: object;
  value: string;
  choices: readonly Readonly<{value: string | number | boolean; label: MessageId; labelValues?: unknown}>[];
  options: readonly Readonly<{value: string; label: string; description?: string}>[];
  apply: (value: string) => void;
}>;
type EngineWitness = Readonly<{page: string; controller: unknown; preferences: unknown; mount: object; route: unknown}>;
/** The exact committed player-chooser owner. The opening and its `engineRef` identity are checked
 * first; only once those pass are the published witness, this mount and the live route observed —
 * `navigation.getSnapshot()` is called here, on the actual navigation, at the original position. */
function engineOpeningAlive(
  opening: EngineChooser | undefined,
  engineRef: Readonly<{current: EngineChooser | undefined}>,
  witnesses: Readonly<{live: Readonly<{current: EngineWitness | undefined}>; mounted: Readonly<{current: boolean}>}>,
  controller: NativeSettingsController,
  navigation: NativeSettingsLivePorts['context']['navigation'],
): boolean {
  if (!opening || engineRef.current !== opening) return false;
  const live = witnesses.live.current;
  return witnesses.mounted.current && !!live && live.page === opening.page && live.controller === opening.controller
    && live.preferences === opening.preferences && live.mount === opening.mount && live.route === opening.route
    && opening.route === navigation.getSnapshot() && controller.lifetime.current();
}
export type SettingsPreferencesProps = {aimedRow?: Readonly<{rowId: string; serial: number}>; verticalEdges?: {up?: string; down?: string}; reportedOutputs?: Readonly<{hdr: boolean; surround: boolean}>; awayLimitLabel?: string; onStreaming: () => void; onQuality: () => void; page: SettingsPage; ports: NativeSettingsLivePorts; controller: NativeSettingsController; runtime: NativeShellRuntime; devicePreferences: NativeDevicePreferences; region: string; column: string};

export function useSettingsPreferences({page, ports, controller, runtime, devicePreferences, awayLimitLabel, aimedRow}: SettingsPreferencesProps) {
  const theme = useTheme(), t = runtime.text.t, state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const [advanced, setAdvanced] = useState(false);
  // The public browser handoff does not grant native password/two-step editing.
  const settingsCapabilities = ports.settingsContext().capabilities;
  const offersWebSignIn = !!settingsCapabilities.localAccount && !settingsCapabilities.porticoMember;
  const managementURL = (() => {
    if (!(theme.isTV && page.id === 'profile' && offersWebSignIn)) return undefined;
    try {return new URL('/settings/profile', ports.context.api.baseUrl).href;} catch {return undefined;}
  })();
  // Frame 23: a TV row with a value opens its choices in the side sheet; switches still flip in place.
  const [choices, setChoices] = useState<Readonly<{title: string; help?: string; value: string; options: readonly Readonly<{value: string; label: string; description?: string}>[]; apply: (value: string) => void}>>();
  const [languages, setLanguages] = useState<Readonly<{testID: string; title: string; help?: string; value: readonly LanguageOption[]; available: readonly LanguageOption[]; limit: number; apply: (value: readonly LanguageOption[]) => void}>>();
  type AccountConfirm = Readonly<{kind: 'signOut' | 'resetRecommendations'; token: number; page: string; revision: number; controller: typeof controller}>;
  const [confirmOpen, setConfirmOpen] = useState<AccountConfirm>(), [actionBusy, setActionBusy] = useState(false), [actionError, setActionError] = useState<unknown>();
  // The confirmation belongs to one opening and one rendered page/route; the flight holds that
  // same object, so no delayed close, result or busy release can reach a successor. Refs are
  // explicit about their empty state, as the current React types require.
  const navigation = ports.context.navigation;
  const route = useSyncExternalStore(navigation.subscribe, navigation.getSnapshot);
  const confirmRef = useRef<AccountConfirm | undefined>(undefined), confirmFlight = useRef<AccountConfirm | undefined>(undefined), confirmToken = useRef(0), confirmScope = useRef<string | undefined>(undefined), confirmOwner = useRef(controller);
  const scope = `${page.id}|${route.revision}`;
  if (confirmScope.current !== scope || confirmOwner.current !== controller) {confirmScope.current = scope; confirmOwner.current = controller; confirmRef.current = undefined; confirmFlight.current = undefined;}
  const shown = confirmOpen && confirmRef.current === confirmOpen && confirmScope.current === scope && confirmOpen.controller === controller
    && confirmOpen.page === page.id && confirmOpen.revision === route.revision ? confirmOpen : undefined;
  useLayoutEffect(() => () => {confirmRef.current = undefined; confirmFlight.current = undefined;}, []);
  // The Player chooser is one owned opening of one committed page, source and mount. The owner
  // is published in the layout turn; a retained opener, select or close is refused when any part
  // of it moved. The mount witness is replaced after a cleanup so a replayed setup revives nothing.
  const [engineMount, setEngineMount] = useState<object>(() => ({}));
  const [engineOpen, setEngineOpen] = useState<EngineChooser>();
  const engineRef = useRef<EngineChooser | undefined>(undefined), engineToken = useRef(0);
  const engineMounted = useRef(true), engineRetired = useRef(false);
  const engineLive = useRef<EngineWitness | undefined>(undefined);
  // The committed owner is published in the layout turn, never during render: an abandoned
  // render cannot replace the witness, and the cleanup clears only the owner it published.
  useLayoutEffect(() => {
    const owner = {page: page.id, controller, preferences: devicePreferences, mount: engineMount, route};
    engineLive.current = owner;
    return () => {if (engineLive.current === owner) engineLive.current = undefined;};
  }, [page.id, controller, devicePreferences, engineMount, route]);
  const engineAlive = (opening: EngineChooser | undefined) => engineOpeningAlive(opening, engineRef, {live: engineLive, mounted: engineMounted}, controller, navigation);
  const engineShown = engineOpen && !state.busy && engineAlive(engineOpen) ? engineOpen : undefined;
  const retireEngine = () => {engineRef.current = undefined; setEngineOpen(undefined);};
  const closeEngine = (opening: EngineChooser) => {if (!engineAlive(opening)) return; retireEngine();};
  useLayoutEffect(() => {
    // A replay's setup publishes a fresh witness after its cleanup retired the old one; the
    // first setup keeps the initializer so the rows drawn in the first render still work.
    engineMounted.current = true;
    if (engineRetired.current) {engineRetired.current = false; setEngineMount({});}
    return () => {engineRetired.current = true; engineMounted.current = false; engineRef.current = undefined;};
  }, []);
  // A page, source, route or unmount change retires the exact opening in the same layout turn.
  useLayoutEffect(() => {
    if (!engineOpen) return;
    if (!state.busy && engineOpeningAlive(engineOpen, engineRef, {live: engineLive, mounted: engineMounted}, controller, navigation)) return;
    retireEngine();
  }, [engineOpen, state.busy, page.id, controller, devicePreferences, route, navigation]);
  const openConfirm = (kind: AccountConfirm['kind']) => {
    // A delayed row action from an old page may not reopen its modal on the successor page.
    if (confirmScope.current !== scope || confirmOwner.current !== controller) return;
    const next: AccountConfirm = {kind, token: ++confirmToken.current, page: page.id, revision: route.revision, controller};
    confirmFlight.current = undefined; confirmRef.current = next; setActionBusy(false); setActionError(undefined); setConfirmOpen(next);
  };
  const closeConfirm = (target: AccountConfirm) => {if (confirmRef.current !== target) return; confirmRef.current = undefined; setConfirmOpen(current => current === target ? undefined : current);};
  const cancel = useRef<View>(null), firstLanguage = useRef<View>(null);
  useSyncExternalStore(devicePreferences.subscribe, () => page.sections.flatMap(section => section.rows).filter(row => row.kind === 'composite' && row.storage === 'device').map(row => devicePreferences.read((row as Extract<Row, {kind: 'composite'}>).id)).join('|'));
  // Closed for each page, before (in the same commit as) a search result opening it again below: as a passive effect
  // this ran after that and shut Advanced on the result's row, which then was never drawn to take focus.
  useLayoutEffect(() => setAdvanced(false), [page.id]);
  const visible = (row: Row) => !('when' in row) || !row.when || state.preferences.effective[row.when.key] === ('equals' in row.when ? row.when.equals : undefined);
  const enabled = (row: Row) => {
    if (!('enableWhen' in row) || !row.enableWhen) return true;
    const value = state.preferences.effective[row.enableWhen.key];
    return value !== undefined && row.enableWhen.in.includes(value);
  };
  const engine = useSyncExternalStore(devicePreferences.subscribe, () => devicePreferences.read('playerEngine'));
  const hasDevicePlayer = Platform.OS === 'android' && page.id === 'device' && engine !== undefined;
  // The drawn Player group sits before Connection (settings-spec advanced group 4)
  // and before Playback fixes. The section id is the source anchor; keep the appended
  // fallback when the page does not carry it.
  const playerSection: SettingsPage['sections'][number] = {
    id: 'device-player',
    advanced: true,
    title: 'next.settingsSpec.account.device.advanced.groups.3.title',
    rows: [{
      kind: 'composite',
      id: 'playerEngine',
      storage: 'device',
      scope: 'device',
      label: 'next.settingsSpec.account.device.advanced.groups.3.rows.0.label',
      control: {
        type: 'choice',
        choices: [
          {value: 'automatic', label: 'next.settingsSpec.account.device.advanced.groups.3.rows.0.control.options.0.label'},
          {value: 'native', label: 'next.settingsSpec.account.device.advanced.groups.3.rows.0.control.options.1.label'},
          {value: 'mpv', label: 'next.settingsSpec.account.device.advanced.groups.3.rows.0.control.options.2.label'},
        ],
      },
    },
    // Frame 11 draws Playback fixes on television only; Android touch keeps the Player row alone.
    ...(theme.isTV ? [{
      kind: 'action' as const,
      id: 'devicePlayerFixes' as const,
      label: 'next.settingsSpec.account.device.advanced.groups.3.rows.1.label' as const,
      value: 'next.settingsSpec.account.device.advanced.groups.3.rows.1.control.label' as const,
    }] : []),
    ],
  };
  const connectionAt = page.sections.findIndex(section => section.id === 'deviceConnection');
  // Television alone moves the injected group before Connection; a phone or tablet keeps
  // the previous appended placement.
  const pageSections: SettingsPage['sections'] = hasDevicePlayer
    ? theme.isTV && connectionAt >= 0
      ? [...page.sections.slice(0, connectionAt), playerSection, ...page.sections.slice(connectionAt)]
      : [...page.sections, playerSection]
    : page.sections;
  // Frame 12: Apple TV's Advanced holds Capabilities, Connection, Storage and Support, so the
  // Connection group (with Insecure connections, P2-69) is on every native platform.
  // A search result aimed at a row under Advanced opens Advanced, once per opening.
  const aimedAdvanced = !!aimedRow && pageSections.some(section => section.advanced && section.rows.some(row => settingsRowId(row) === aimedRow.rowId));
  useLayoutEffect(() => {if (aimedAdvanced) setAdvanced(true);}, [aimedAdvanced, aimedRow?.serial]);
  const sections = pageSections.filter(section => !section.advanced || advanced);
  const equalizer = useSyncExternalStore(nativeEqualizer.subscribe, nativeEqualizer.getSnapshot);
  const showEqualizer = page.id === 'music' && equalizer.supported;
  useEffect(() => {
    if (showEqualizer && !equalizer.ready && !nativeEqualizer.getSnapshot().pending) void nativeEqualizer.refresh().catch(runtime.reportError);
  }, [showEqualizer, equalizer.ready, runtime.reportError]);
  const showSafeMode = theme.isTV && page.id === 'quality' && nativeSafeModeAvailable(devicePreferences);
  const showDynamicRange = theme.isTV && nativeDynamicRangeMatchingAvailable();
  const rowIds = sections.flatMap(section => [...section.rows.filter(visible).map(settingsRowId).filter(id => id !== 'equalizer'),
    ...(showEqualizer && section.id === 'sound' ? ['settings-equalizer'] : []),
    ...(showSafeMode && section.id === 'tvOutputs' ? [nativeSafeModeFocusId] : []),
    ...(section.id === 'limits' && awayLimitLabel ? ['settings-away-limit', ...(!theme.isTV ? ['settings-away-limit-change'] : [])] : []),
    ...(theme.isTV && Platform.OS === 'android' && page.id === 'device' && advanced && section.id === 'about' ? passthroughFormats.map(format => `settings-passthrough-${format}`) : []),
    ...(theme.isTV && page.id === 'device' && section.id === 'device' ? [...(Platform.OS === 'android' ? [nativeFrameRateFocusId, nativePassthroughFocusId] : []), ...(showDynamicRange ? [nativeDynamicRangeFocusId] : []), nativeLossNoticesFocusId] : [])]);
  // A custom panel hosts its own rows under ids that are not these (settings-panel-profiles for
  // 'profiles'), so naming rowIds[0] as the entry finds no host and the entry falls through to the
  // only id that does match: the last row. With panels the region enters at its first mounted row.
  const hostedRows = !sections.some(section => section.rows.some(row => row.kind === 'custom' && visible(row)));
  const skipMatrix = !theme.isTV && theme.platform !== 'phone';
  const change = (row: Extract<Row, {kind: 'preference' | 'composite'}>, value: string | number | boolean | readonly string[]) => {
    if (!enabled(row)) return;
    if (row.kind === 'composite' && row.storage === 'device') {void devicePreferences.write(row.id, value as string | boolean).catch(runtime.reportError); return;}
    const patch = row.kind === 'preference' ? {[row.key]: value} : compositePatch(row.id, value as string | boolean);
    const field = state.preferences.registry.fields.find(value => value.key === Object.keys(patch)[0]);
    if (!field) return;
    const wanted: PreferenceScope = row.scope === 'device' ? 'profile-device' : row.scope === 'deviceClass' ? 'profile-device-class' : 'profile-server';
    // The server says where each setting is saved; a row's own scope only picks among several.
    const scope: PreferenceScope | undefined = field.scopes.includes(wanted) ? wanted : field.scopes.length === 1 ? field.scopes[0] : undefined;
    if (!scope) throw new Error('native_settings_scope_unavailable');
    void controller.apply(scope, patch).catch(runtime.reportError);
  };
  if (pageSections.some(section => section.advanced)) rowIds.push('settings-advanced');
  const device: MessageId = Platform.isTV ? Platform.OS === 'ios' ? 'device.noun.appleTV' : String((Platform.constants as {Manufacturer?: string}).Manufacturer ?? '').toLowerCase() === 'amazon' ? 'device.noun.fireTV' : 'device.noun.androidTV'
    : Platform.OS === 'ios' ? (Platform as {isPad?: boolean}).isPad ? 'device.noun.iPad' : 'device.noun.iPhone' : Math.min(Dimensions.get('screen').width, Dimensions.get('screen').height) >= 600 ? 'device.noun.tablet' : 'device.noun.phone';
  const confirm = () => {
    const opening = shown;
    if (!opening || confirmFlight.current || confirmRef.current !== opening) return;
    // Re-read the route and visibility at the press, not the render that drew the dialog.
    const nav = navigation.getSnapshot();
    if (nav.revision !== opening.revision || !navigation.isCurrent()) return;
    if (runtime.getSnapshot().contentVisible !== true) return;
    if (opening.page !== page.id || opening.controller !== controller || !controller.lifetime.current()) return;
    confirmFlight.current = opening; setActionBusy(true);
    const work = opening.kind === 'signOut' ? ports.accountActions.signOut() : controller.lifetime.request(signal => resetRecommendations(ports.context.api, {serverId: controller.lifetime.authority.serverId, operationId: ports.requestId(), signal}));
    void work.then(
      // Only the opening that started this closes its own dialog; sign-out's own session
      // retirement is intentional, so its late UI writes are skipped rather than retried.
      () => {if (controller.lifetime.current() && confirmRef.current === opening) closeConfirm(opening);},
      error => {if (controller.lifetime.current() && confirmFlight.current === opening) setActionError(error);}
    ).finally(() => {
      if (confirmFlight.current !== opening) return;
      confirmFlight.current = undefined;
      if (!controller.lifetime.current()) return;
      setActionBusy(false);
    });
  };
  const openEngine = (row: Extract<Row, {kind: 'preference' | 'composite'}>, value: unknown,
    offered: EngineChooser['choices'], options: EngineChooser['options']) => {
    const owner = {page: page.id, controller, preferences: devicePreferences, mount: engineMount, route};
    // Prove the committed page, source, mount and route before storing the opening;
    // never a render-side owner or the latest refs. A busy controller refuses too.
    const live = engineLive.current;
    if (controller.getSnapshot().busy || !engineMounted.current || !live || live.page !== owner.page || live.controller !== owner.controller
      || live.preferences !== owner.preferences || live.mount !== owner.mount || live.route !== owner.route
      || owner.route !== navigation.getSnapshot() || !controller.lifetime.current()) return;
    const opening: EngineChooser = {token: ++engineToken.current, ...owner, revision: owner.route.revision,
      value: String(value), choices: offered, options,
      apply: next => {
        const held = engineLive.current;
        if (!engineMounted.current || !held || held.mount !== owner.mount || held.page !== owner.page
          || held.controller !== owner.controller || held.preferences !== owner.preferences || held.route !== owner.route
          || !controller.lifetime.current()) return;
        const choice = offered.find(choice => String(choice.value) === next); if (choice) change(row, choice.value);
      }};
    engineRef.current = opening;
    setEngineOpen(opening);
  };
  const selectEngine = (value: string) => {
    const opening = engineRef.current;
    if (controller.getSnapshot().busy || !opening || opening !== engineShown || !engineAlive(opening)) return;
    const choice = opening.choices.find(choice => String(choice.value) === value);
    if (!choice) return;
    retireEngine();
    opening.apply(String(choice.value));
  };
  return {t, state, isTV: theme.isTV, android: Platform.OS === 'android', apple: Platform.OS === 'ios', navigation, device,
    rowIds, hostedRows, sections, hasAdvanced: pageSections.some(section => section.advanced), advanced, setAdvanced, visible, enabled, change,
    showSafeMode, showDynamicRange, skipMatrix, managementURL, actionError, setActionError, actionBusy, confirmFlight, shown, openConfirm, closeConfirm, confirm, cancel,
    engineShown, openEngine, closeEngine, selectEngine,
    choices, setChoices, languages, setLanguages, firstLanguage};
}
