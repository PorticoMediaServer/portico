import {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import type {TextInput, View} from 'react-native';
import type {RoomEnterLifetime} from '../../../../../../packages/client-core/src/room-session';
import type {GroupControl} from '../../../../../../packages/client-core/src/room-protocol';
import type {MediaItem} from '../../../../../../packages/client-core/src';
import {parseMediaItem} from '../../../../../../packages/client-core/src/media-item';
import {readCatalogueItemResponse} from '../../../../../../packages/client-core/src/query/catalogue-reads';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {tvFocusIds} from '../../../shell/tv-focus';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeTogetherController} from '../../../screens/settings-live/together-controller';
import {nativeTogetherLobbyFaces} from '../../../screens/settings-live/together-presentation';
import type {NativeTogetherMedia} from '../../../screens/settings-live/together-media';
import type {NativeTogetherPage} from '../../../screens/settings-live/together-page';

export type TogetherProps = {page: NativeTogetherPage; controller: NativeTogetherController; media: NativeTogetherMedia;
  ports: Pick<NativeSettingsLivePorts, 'context'>; runtime: NativeShellRuntime; onBack?: () => void;
  /** N2-1: a Watch Together link's code: Join with a code opens with it filled in, once for this page. */
  joinCode?: string};

/** Watch Together's lobby (states 27a, 27b): the page and controller opening every start, join and open is scoped to,
 * the Start a group and Join with a code forms, a link's code filled in once, and the way back. */
export function useTogetherLobby({controller, ports, runtime, page, onBack, joinCode}: TogetherProps) {
  const theme = useTheme(), t = runtime.text.t, phone = theme.platform === 'phone';
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const ids = useMemo(() => tvFocusIds(controller.lifetime.authority), [controller]);
  const region = ids.surface('together');
  const group = state.group;
  type Form = {kind: 'start' | 'join'; retired: boolean};
  const [form, setForm] = useState<Form>(), [name, setName] = useState(''), [code, setCode] = useState('');
  const [typing, setTyping] = useState<'name' | 'code' | 'who'>(), [typingRevision, setTypingRevision] = useState(0), [typingIntent, setTypingIntent] = useState<'enter' | 'restore'>('enter');
  const firstField = useRef<TextInput>(null);
  const firstTVField = useRef<View>(null);
  const profile = ports.context.account.profiles.find(value => value.id === controller.lifetime.authority.profileId);
  const faces = nativeTogetherLobbyFaces(state.group, runtime.getSnapshot().profile);
  const [who, setWho] = useState(profile?.name ?? ''), [authority, setAuthority] = useState<GroupControl>('host');
  const startRef = useRef<View>(null), joinRef = useRef<View>(null);
  const sheet = form?.kind;
  const view = useMemo(() => ({active: true, binding: {controller, page}, form: undefined as Form | undefined, pending: undefined as {abort: AbortController; form?: Form} | undefined}), [controller, page]);
  const viewCurrent = useCallback((expected: Form | undefined) => {
    const local = () => view.active && view.form === expected && !expected?.retired && !page.signal.aborted && !controller.lifetime.signal.aborted;
    return local() && page.current() && local() && controller.lifetime.current() && local();
  }, [controller, page, view]);
  const roomCurrent = useCallback(() => viewCurrent(view.form), [viewCurrent, view]);
  // SUP-006: only the server's wrong-code refusal for this join belongs on the field.
  const codeFailure = !theme.isTV && sheet === 'join' && state.joinFailure?.code === 'code_invalid' && !!state.error;
  const closeForm = () => {
    if (!form || !viewCurrent(form)) return;
    form.retired = true; view.form = undefined;
    setForm(undefined); setTyping(undefined); controller.clearJoinFailure();
    if (view.pending?.form === form) {const original = view.pending; view.pending = undefined; original.abort.abort();}
  };
  const enter = async (work: (lifetime: RoomEnterLifetime) => Promise<boolean>, expected: Form | undefined) => {
    if (!viewCurrent(expected) || view.pending) return false;
    const flight = {abort: new AbortController(), form: expected}; view.pending = flight;
    const current = () => view.pending === flight && !flight.abort.signal.aborted && viewCurrent(expected)
      && view.pending === flight && !flight.abort.signal.aborted;
    const stop = () => {if (!current()) flight.abort.abort();};
    const offNavigation = ports.context.navigation.subscribe(stop), offAuthority = ports.context.subscribeAuthority(stop);
    const retire = () => flight.abort.abort(); page.signal.addEventListener('abort', retire, {once: true});
    try {if (!current()) return false; const accepted = await work({signal: flight.abort.signal, isCurrent: current}); return current() && accepted;}
    finally {page.signal.removeEventListener('abort', retire); offNavigation(); offAuthority(); if (view.pending === flight) view.pending = undefined;}
  };
  useLayoutEffect(() => {view.active = true; return () => {
    view.active = false; if (view.form) view.form.retired = true;
    const original = view.pending; view.pending = undefined; original?.abort.abort();
  };}, [view]);
  const run = (work: () => Promise<unknown>) => {if (!viewCurrent(form)) return; void work().catch(error => {if (viewCurrent(form)) runtime.reportError(error);});};
  // The refresh belongs to this page/controller opening, not to the form: it must not run again
  // when a field changes or a new handler identity is created. `view` is the private binding for
  // that opening and its `form` field is the exact form lifetime the opening holds, so the guard
  // reads the committed form without adding either the form state or a render ref to the trigger.
  useEffect(() => {
    const expected = view.form;
    if (!viewCurrent(expected)) return;
    void controller.refresh().catch(error => {if (viewCurrent(expected)) runtime.reportError(error);});
  }, [controller, runtime, view, viewCurrent]);
  const room = group && !['idle', 'left', 'joining'].includes(state.phase);
  // N2-1: a link's code is filled into Join with a code once; the viewer still confirms their name and joins.
  const linkCode = useRef(joinCode);
  useEffect(() => {
    const value = linkCode.current;
    if (!value || room || view.form || view.pending || !viewCurrent(undefined)) return;
    linkCode.current = undefined;
    const next: Form = {kind: 'join', retired: false}; view.form = next; setForm(next); setCode(value.toUpperCase().slice(0, 12));
    if (theme.isTV) {setTyping('code'); setTypingIntent('enter'); setTypingRevision(revision => revision + 1);}
  }, [room, view, viewCurrent, theme.isTV]);
  const back = () => {
    if (!viewCurrent(form)) return;
    if (form) {closeForm(); return;}
    const original = view.pending; view.pending = undefined; original?.abort.abort(); if (viewCurrent(form)) onBack?.();
  };
  const busy = state.busy || state.phase === 'joining';
  const fieldsRegion = ids.surface('together.form'), keyboard = ids.surface('together.keyboard');
  const type = (field: 'name' | 'code' | 'who') => {if (viewCurrent(form) && theme.isTV) {setTypingIntent(typing === field ? 'restore' : 'enter'); setTyping(field); setTypingRevision(value => value + 1);}};
  const openForm = (kind: Form['kind']) => {
    if (!viewCurrent(undefined) || view.pending) return;
    const defaultName = kind === 'start' && !name.trim() ? t('web.together.defaultName', {name: runtime.getSnapshot().profile.name}) : undefined;
    if (!viewCurrent(undefined)) return;
    const next: Form = {kind, retired: false}; view.form = next; setForm(next);
    if (defaultName !== undefined) setName(defaultName);
    setTyping(theme.isTV && kind === 'join' ? 'code' : undefined);
    if (theme.isTV && kind === 'join') {setTypingIntent('enter'); setTypingRevision(value => value + 1);}
  };
  return {phone, state, ids, region, group, form, setForm, name, setName, code, setCode, typing, setTyping, typingRevision, setTypingRevision, typingIntent, setTypingIntent, firstField, firstTVField, profile, faces, who, setWho, authority, setAuthority, startRef, joinRef, sheet, view, viewCurrent, roomCurrent, codeFailure, closeForm, enter, run, room, linkCode, back, busy, fieldsRegion, keyboard, type, openForm};
}

/** What is on in the viewer's group, for the lobby's wash: one authorized current title, never a demo backdrop or a
 * catalogue scan. */
export function useTogetherLobbyArt({itemId, controller, ports, runtime}: {itemId?: string; controller: NativeTogetherController;
  ports: Pick<NativeSettingsLivePorts, 'context'>; runtime: NativeShellRuntime}) {
  const [item, setItem] = useState<MediaItem>();
  useEffect(() => {
    setItem(undefined);
    if (!itemId) return;
    const abort = new AbortController();
    void controller.lifetime.request(signal => readCatalogueItemResponse(ports.context.api, itemId, signal).then(parseMediaItem), abort.signal)
      .then(value => {if (!abort.signal.aborted && controller.lifetime.current() && value.id === itemId && value.available) setItem(value);}, () => {});
    return () => abort.abort();
  }, [itemId, controller, ports]);
  return item && item.id === itemId ? runtime.ambient(item.backdropUrl) : undefined;
}
