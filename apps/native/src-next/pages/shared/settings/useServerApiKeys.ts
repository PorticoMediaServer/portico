import {serverNow} from '../../../../../../packages/client-core/src/server-clock';
import {useEffect, useLayoutEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {Platform, Share} from 'react-native';
import {OwnerApiKeysSource, type OwnerApiKey} from '../../../../../../packages/client-core/src/server-admin/owner-api-keys';
import {withGuardedRaw} from '../../../../../../packages/client-core/src/home-selection/list-destinations';
import {EXPIRY_DAYS, chosenScopes, expiryInstant, type ExpiryDays} from '../../../../../../packages/client-core/src/server-admin/api-words';
import {type ApiScope} from '../../../../../../packages/client-core/src/api-access';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {copyText} from '../../../platform/clipboard';
import type {MenuEntry} from '../../../kit/overlays';
import {NativeScreenLifetime, screenScopeEnded} from '../../../screens/settings-live/lifetime';
import {nativeStorageOwner} from '../../../screens/settings-live/server/storage/space-source';

export function createNativeApiKeys(context: NativeShellBootstrapContext, operationId: () => string, reportError: (error: unknown) => void) {
  const lifetime = new NativeScreenLifetime(context);
  const current = () => !Platform.isTV && lifetime.current() && nativeStorageOwner(context);
  const api = {request: <T,>(path: string, method?: string, body?: unknown, caller?: AbortSignal): Promise<T> => current()
    ? lifetime.request(signal => context.api.request<T>(path, method, body, signal), caller) : Promise.reject(screenScopeEnded())};
  // The rebuilt keys (GET/POST/DELETE /v1/admin/api-keys); a new key's secret is shown once, at creation.
  const source = new OwnerApiKeysSource(withGuardedRaw(api, context.api, () => {if (!current()) throw screenScopeEnded();}), current, error => {lifetime.dispose();reportError(error);}, operationId);
  return {context, source, current, dispose: () => {source.dispose();lifetime.dispose();}};
}
export type ApiKeyGroup = ReturnType<typeof createNativeApiKeys>;
type Form = Readonly<{source: OwnerApiKeysSource; token: object}>;
type Card = Readonly<{source: OwnerApiKeysSource; issued: Readonly<{credential: OwnerApiKey; secret: string}>}>;
type Selection = Readonly<{source: OwnerApiKeysSource; key: OwnerApiKey}>;

/** Secrets remain in this exact issued card, never in the shared source or a
 * reusable snapshot. Closing a card/form synchronously retires its handlers. */
export function useServerApiKeys({group, runtime, createRequest = 0, consumeCreateRequest}: Readonly<{group: ApiKeyGroup; runtime: NativeShellRuntime; createRequest?: number; consumeCreateRequest: (request: number) => boolean}>) {
  const {source} = group, t = runtime.text.t as typeof defaultI18n.t;
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const [form, setForm] = useState<Form>(), [card, setCard] = useState<Card>(), [menu, setMenu] = useState<Selection>(), [confirmation, setConfirmation] = useState<Selection>();
  const [name, setName] = useState(''), [ticked, setTicked] = useState<ReadonlySet<ApiScope>>(new Set()), [days, setDays] = useState<ExpiryDays>(0), [copied, setCopied] = useState(false), [cardError, setCardError] = useState(false);
  const [cursors, setCursors] = useState<readonly string[]>(['']);
  const view = useMemo(() => ({source, active: true, form: undefined as Form | undefined, card: undefined as Card | undefined, menu: undefined as Selection | undefined, confirmation: undefined as Selection | undefined, io: undefined as object | undefined, request: 0}), [source]);
  useLayoutEffect(() => {view.active = true;return () => {view.active = false;view.form = undefined;view.card = undefined;view.menu = undefined;view.confirmation = undefined;view.io = undefined;};}, [view]);
  useEffect(() => {void source.load();}, [source]);
  const current = () => !Platform.isTV && view.active && view.source === source && group.current() && source.getSnapshot().phase !== 'idle';
  const heldForm = form && view.form === form && current() ? form : undefined;
  const heldCard = card && view.card === card && current() ? card : undefined;
  const heldConfirmation = confirmation && view.confirmation === confirmation && current() ? confirmation : undefined;
  const editing = () => !!heldForm && current() && view.form === heldForm && !source.getSnapshot().busy && !source.getSnapshot().pendingCreate;
  const openForm = () => {
    if (!current() || source.getSnapshot().busy || view.form) return;
    view.card = undefined;view.io = undefined;setCard(undefined);setCopied(false);setCardError(false);
    const next = {source, token: {}};view.form = next;setForm(next);setName('');setTicked(new Set());setDays(0);
  };
  useEffect(() => {
    if (!createRequest || createRequest === view.request || !view.active || !group.current() || state.phase !== 'ready' && !state.pendingCreate || state.busy) return;
    if (!consumeCreateRequest(createRequest)) return;
    view.request = createRequest;
    const next = {source, token: {}};view.card = undefined;view.io = undefined;view.form = next;
    setCard(undefined);setCopied(false);setCardError(false);setForm(next);setName('');setTicked(new Set());setDays(0);
  }, [createRequest, consumeCreateRequest, view, source, group, state.phase, state.busy, state.pendingCreate]);
  const closeForm = () => {if (!heldForm || !current() || view.form !== heldForm || source.getSnapshot().busy) return;view.form = undefined;setForm(undefined);};
  const closeCard = () => {if (!heldCard || view.card !== heldCard) return;view.card = undefined;view.io = undefined;setCard(undefined);setCopied(false);setCardError(false);};
  const issue = async () => {
    const attempt = heldForm;if (!attempt || !current() || view.form !== attempt || source.getSnapshot().busy) return;
    const out = await (source.getSnapshot().pendingCreate ? source.retryCreate() : source.create({name, scopes: chosenScopes(ticked), ...(days ? {expiresAt: expiryInstant(days, serverNow())} : {})}));
    if (!out || !current() || view.form !== attempt) return;
    view.form = undefined;setForm(undefined);const next = {source, issued: {credential: out.key, secret: out.secret ?? ''}};view.card = next;setCard(next);setCopied(false);setCardError(false);setCursors(['']);
  };
  const cardIO = async (kind: 'copy' | 'share') => {
    const captured = heldCard;if (!captured?.issued.secret || !current() || view.card !== captured || view.io) return;
    const io = {};view.io = io;
    try {
      if (!current() || view.card !== captured || view.io !== io) return;
      if (kind === 'copy') await copyText(captured.issued.secret);else await Share.share({title: captured.issued.credential.name, message: captured.issued.secret});
      if (current() && view.card === captured && view.io === io) {setCopied(kind === 'copy');setCardError(false);}
    } catch {if (current() && view.card === captured && view.io === io) setCardError(true);}
    finally {if (view.io === io) view.io = undefined;}
  };
  const revoke = async () => {
    const captured = heldConfirmation;if (!captured || !current() || view.confirmation !== captured || source.getSnapshot().busy || !source.getSnapshot().page?.items.includes(captured.key)) return;
    await source.revoke(captured.key);
    if (current() && view.confirmation === captured) {view.confirmation = undefined;setConfirmation(undefined);}
  };
  const closeConfirmation = () => {if (!current() || view.confirmation !== confirmation || source.getSnapshot().busy) return;view.confirmation = undefined;setConfirmation(undefined);};
  const frozen = state.busy || state.pendingCreate;
  const menuItems: readonly MenuEntry[] = menu && view.menu === menu && current() ? [{id: 'revoke', label: t('web.access.revoke'), icon: 'i-trash', destructive: true, disabled: state.busy || menu.key.state !== 'active', onSelect: () => {
    if (!current() || view.menu !== menu || source.getSnapshot().busy || !source.getSnapshot().page?.items.includes(menu.key)) return;
    view.menu = undefined;setMenu(undefined);const next = {source, key: menu.key};view.confirmation = next;setConfirmation(next);
  }}] : [];
  const openMenu = (key: OwnerApiKey) => {
    if (!current() || source.getSnapshot().busy || !source.getSnapshot().page?.items.includes(key)) return;
    const next = {source, key};
    view.menu = next; setMenu(next);
  };
  const closeMenu = () => {
    if (!menu || view.menu !== menu) return;
    view.menu = undefined; setMenu(undefined);
  };
  const previousPage = () => {
    if (!current() || source.getSnapshot().busy || source.getSnapshot().page !== state.page) return;
    const next = cursors.slice(0, -1);
    setCursors(next); void source.load(next[next.length - 1]);
  };
  const nextPage = () => {
    const cursor = state.page?.nextCursor;
    if (!current() || source.getSnapshot().busy || source.getSnapshot().page !== state.page || !cursor) return;
    setCursors([...cursors, cursor]); void source.load(cursor);
  };
  const changeScope = (scope: ApiScope) => {
    if (!editing()) return;
    setTicked(current => {const next = new Set(current); if (next.has(scope)) next.delete(scope); else next.add(scope); return next;});
  };
  const changeDays = (value: string) => {
    const parsed = Number(value);
    if (editing() && EXPIRY_DAYS.some(day => day === parsed)) setDays(parsed as ExpiryDays);
  };
  return {
    state, frozen, heldForm, heldCard, heldConfirmation, openForm, closeForm, closeCard, closeConfirmation, issue, cardIO, revoke,
    menu: menu && view.menu === menu && current() ? menu : undefined, menuItems, openMenu, closeMenu,
    name, changeName: (value: string) => {if (editing()) setName(value);}, ticked, changeScope, days, changeDays, copied, cardError,
    hasPrevious: cursors.length > 1, previousPage, nextPage, retry: () => {if (current()) void source.load();},
  };
}
