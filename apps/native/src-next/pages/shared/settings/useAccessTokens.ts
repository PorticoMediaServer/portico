import {useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {ownerAccessTokenApi} from '../../../../../../packages/client-core/src/owner-account-clients';
import {serverNow} from '../../../../../../packages/client-core/src/server-clock';
import {createAccessToken, getApiAccessOptions, listAccessTokens, revokeAccessToken, type ApiAccessOptions, type ApiAccessTransport, type PersonalAccessToken, type ApiScope} from '../../../../../../packages/client-core/src/api-access';
import {idempotencyKey} from '../../../../../../packages/client-core/src/playback-v1/http';
import {EXPIRY_DAYS, chosenScopes, expiryInstant, type ExpiryDays} from '../../../../../../packages/client-core/src/server-admin/api-words';
import {createOperationIds} from '../../../../../../packages/client-core/src/server-admin/server-forms';
import {copyText} from '../../../platform/clipboard';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsPanelDeps} from '../../../screens/settings-live/settings-panel-deps';
import {createNativeAccessTokenScope, nativeAccessTokensAllowed, type NativeAccessTokenScope} from '../../../screens/settings-live/native-account-security';

export type AccessTokensProps = NativeSettingsPanelDeps & {runtime: NativeShellRuntime};

type TokenOpening = Readonly<{scope: NativeAccessTokenScope; transport: ApiAccessTransport}>;
type TokenView = 'list' | 'create' | 'revoke' | 'receipt';

export function useAccessTokens({context, lifetime, t, runtime}: AccessTokensProps) {
  const navigation = useSyncExternalStore(context.navigation.subscribe, context.navigation.getSnapshot);
  const [opening, setOpening] = useState<TokenOpening>(), openingRef = useRef<TokenOpening>(undefined);
  const mounted = useRef(false), busyRef = useRef(false), copyRef = useRef(false);
  const operations = useRef(createOperationIds(() => idempotencyKey()));
  const [busy, setBusy] = useState(false), [error, setError] = useState<unknown>(), [notice, setNotice] = useState('');
  const [page, setPage] = useState<Awaited<ReturnType<typeof listAccessTokens>>>(), [options, setOptions] = useState<ApiAccessOptions>();
  const [view, setView] = useState<TokenView>('list'), [revoking, setRevoking] = useState<PersonalAccessToken>();
  const [name, setName] = useState(''), [ticked, setTicked] = useState<ReadonlySet<ApiScope>>(() => new Set()), [days, setDays] = useState<ExpiryDays>(0);
  const [issued, setIssued] = useState<Awaited<ReturnType<typeof createAccessToken>>>(), [copied, setCopied] = useState(false);
  const clear = () => {
    setBusy(false); setError(undefined); setPage(undefined); setOptions(undefined); setView('list'); setRevoking(undefined);
    setName(''); setTicked(new Set()); setDays(0); setIssued(undefined); setCopied(false);
  };
  const close = () => {openingRef.current?.scope.dispose(); openingRef.current = undefined; busyRef.current = false; copyRef.current = false; operations.current.release(); setOpening(undefined); clear();};
  useLayoutEffect(() => {
    mounted.current = true; clear(); setNotice(''); setOpening(undefined);
    return () => {mounted.current = false; openingRef.current?.scope.dispose(); openingRef.current = undefined; busyRef.current = false; copyRef.current = false;};
  }, [context, lifetime, navigation.revision, navigation.route]); // A different Settings opening keeps no list or secret.
  const current = (held: TokenOpening | undefined): held is TokenOpening => !!held && mounted.current && openingRef.current === held && !runtime.retirementSignal?.aborted && held.scope.current();
  const run = async <T,>(held: TokenOpening | undefined, work: (held: TokenOpening) => Promise<T>, accepted: (value: T) => void) => {
    if (!current(held) || busyRef.current) return;
    busyRef.current = true; setBusy(true); setError(undefined);
    try {const value = await work(held); if (current(held)) accepted(value);}
    catch (failure) {if (current(held)) setError(failure);}
    finally {if (current(held)) {busyRef.current = false; setBusy(false);}}
  };
  const load = (held: TokenOpening | undefined, cursor = '', append = false) => {
    void run(held, h => Promise.all([listAccessTokens(h.transport, cursor, h.scope.signal), getApiAccessOptions(h.transport, h.scope.signal)]), ([next, access]) => {
      setOptions(access); setPage(value => append && value ? {...next, items: [...value.items, ...next.items]} : next);
    });
  };
  const open = () => {
    if (openingRef.current || !mounted.current || !lifetime.current() || !context.current() || !nativeAccessTokensAllowed(context)) return;
    const now = context.navigation.getSnapshot();
    if (now.revision !== navigation.revision || now.route !== navigation.route || now.route.kind !== 'shell' || now.route.destination.kind !== 'settings') return;
    close(); setNotice('');
    let scope: NativeAccessTokenScope;
    try {scope = createNativeAccessTokenScope(context, lifetime);} catch (failure) {if ((failure as {name?: string})?.name === 'AbortError') return; throw failure;}
    const transport = ownerAccessTokenApi(scope);
    const held = {scope, transport};
    openingRef.current = held; setOpening(held); load(held);
  };
  const held = opening;
  const editing = () => current(held) && !busyRef.current;
  const edit = (setter: (value: string) => void) => (value: string) => {if (editing()) setter(value);};
  const toggleScope = (scope: ApiScope) => {if (!editing()) return; setTicked(current => {
    const next = new Set(current);
    if (scope === 'admin') {if (next.has('admin')) next.delete('admin'); else {next.clear(); next.add('admin');} return next;}
    next.delete('admin'); if (next.has(scope)) next.delete(scope); else next.add(scope); return next;
  });};
  const begin = () => {if (!editing()) return; setView('create'); setError(undefined);};
  const submit = () => {
    const attempt = held, chosen = chosenScopes(ticked), trimmed = name.trim();
    if (!current(attempt) || busyRef.current || !trimmed || !chosen.length) return;
    const expiresAt = expiryInstant(days, serverNow());
    // One operation id per draft: a retry of the same ask never makes a second token.
    const operationId = operations.current.forPayload(JSON.stringify([trimmed, chosen, expiresAt ?? '']));
    void run(attempt, h => createAccessToken(h.transport, {operationId, name: trimmed, scopes: chosen, ...(expiresAt ? {expiresAt} : {})}, h.scope.signal), receipt => {
      operations.current.release();
      setIssued(receipt); setName(''); setTicked(new Set()); setDays(0); setView('receipt');
    });
  };
  const revoke = () => {
    const attempt = held, target = revoking;
    if (!current(attempt) || !target || busyRef.current) return;
    void run(attempt, h => revokeAccessToken(h.transport, target.id, h.scope.signal), () => {
      setRevoking(undefined); setView('list'); setNotice(t('api.credential.tokenRevoked'));
    }).then(() => load(attempt));
  };
  const copySecret = async () => {
    const secret = issued?.secret;
    if (!secret || !current(held) || copyRef.current) return;
    copyRef.current = true;
    try {await copyText(secret); if (current(held)) {setCopied(true); setError(undefined);}}
    catch (failure) {if (current(held)) setError(failure);}
    finally {if (openingRef.current === held) copyRef.current = false;}
  };
  const receipt = view === 'receipt';
  const retry = () => {if (current(held)) {setError(undefined); load(held);}};
  return {
    allowed: lifetime.current() && context.current() && nativeAccessTokensAllowed(context),
    showing: current(held),
    busy, error, notice, page, options, view, revoking, name, ticked, days, issued, copied, receipt,
    canCreate: !!name.trim() && chosenScopes(ticked).length > 0,
    open, retry, begin, submit, revoke, copySecret, toggleScope, setName: edit(setName),
    setExpiry: (value: string) => {
      const parsed = Number(value);
      if (editing() && EXPIRY_DAYS.some(day => day === parsed)) setDays(parsed as ExpiryDays);
    },
    pickToken: (token: PersonalAccessToken) => {
      if (editing()) {setRevoking(token); setView('revoke'); setError(undefined);}
    },
    more: () => {if (editing() && page?.nextCursor) load(held, page.nextCursor, true);},
    back: () => {if (editing()) {setView('list'); setError(undefined);}},
    cancelRevoke: () => {if (editing()) {setRevoking(undefined); setView('list');}},
    dismiss: () => {if (!receipt && current(held)) close();},
    close: () => {if (current(held)) close();},
  };
}
