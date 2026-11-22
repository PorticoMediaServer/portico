import {createHostedNotificationClient} from '../../../../../../packages/client-core/src/query/hosted-account-transport';
import {useLayoutEffect, useRef, useState} from 'react';
import {AccountNotificationsClient, type NotificationPreferences} from '../../../../../../packages/client-core/src/account-notifications';
import {useTheme} from '../../../kit/theme';
import {nativeAccounts} from '../../../platform/accounts';
import {accountExpectation, nativeAccountRead, nativeAccountWork} from '../../../platform/account-work';
import {nativeNetworkFetch} from '../../../platform/server-connections';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeScreenLifetime} from '../../../screens/settings-live/lifetime';

type Opening = Readonly<{current: () => boolean; save: (next: boolean) => Promise<NotificationPreferences>}>;

/**
 * PX-05, "Server stops answering" (settings.html Notifications, account.html 27): the Portico
 * Account's own notice switch, under the Notification dot on a phone or tablet. Portico Account
 * sign-ins only, and only while the app holds that account's session: the row appears once the
 * account has answered, and a local account never consults the Portico credential owner.
 * Moved from NativeServerOfflineNoticeRow as it was; `shown` is the old early return.
 */
export function useServerOfflineNotice({context, runtime, lifetime}: {context: NativeShellBootstrapContext; runtime: NativeShellRuntime; lifetime: NativeScreenLifetime}) {
  const theme = useTheme(), retirement = runtime.retirementSignal;
  const hostedAccountId = context.account.account.hostedAccountId;
  const [value, setValue] = useState<boolean>(), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const openingRef = useRef<Opening | undefined>(undefined), [opening, setOpening] = useState<Opening>();
  useLayoutEffect(() => {
    setOpening(undefined); setValue(undefined); setBusy(false); setError(undefined);
    if (theme.isTV || !hostedAccountId || retirement?.aborted || !lifetime.current() || !context.current()) return;
    const credentials = nativeAccounts.accountCredentials(), original = credentials.getSnapshot().session;
    if (!original || original.account.id !== hostedAccountId) return;
    const scope = {accountId: original.account.id, familyId: original.familyId};
    const requests = new Set<AbortController>();
    let active = true;
    const current = () => {
      const session = credentials.getSnapshot().session;
      return active && openingRef.current === held && lifetime.current() && context.current() && !retirement?.aborted
        && context.account.account.hostedAccountId === scope.accountId
        && session?.account.id === scope.accountId && session.familyId === scope.familyId;
    };
    const run = async <T,>(write: boolean, work: (client: AccountNotificationsClient, signal: AbortSignal) => Promise<T>): Promise<T> => {
      const request = new AbortController(); requests.add(request);
      const timer = setTimeout(() => request.abort(), 15000);
      try {
        const session = credentials.getSnapshot().session;
        if (!session || !current()) throw Object.assign(new Error('native_account_changed'), {code: 'account_changed'});
        return await (write ? nativeAccountWork : nativeAccountRead)(credentials, live => work(createHostedNotificationClient(credentials, live, nativeNetworkFetch, request.signal), request.signal),
        {expected: accountExpectation(session), signal: request.signal, isCurrent: current});
      } finally {clearTimeout(timer); requests.delete(request);}
    };
    const dispose = (notify = false) => {
      if (!active) return;
      active = false; off(); retirement?.removeEventListener('abort', changed); lifetime.signal.removeEventListener('abort', changed);
      for (const request of requests) request.abort();
      requests.clear();
      if (openingRef.current === held) {openingRef.current = undefined; if (notify) {setOpening(undefined); setValue(undefined); setBusy(false);}}
    };
    const changed = () => {if (!current()) dispose(true);};
    const held: Opening = {current, save: next => run(true, client => client.savePreferences({serverOffline: next}))};
    openingRef.current = held; setOpening(held);
    const off = credentials.subscribe(changed);
    retirement?.addEventListener('abort', changed, {once: true});
    // The Settings composition ends with its lifetime: a request in flight is cancelled then, not at unmount.
    lifetime.signal.addEventListener('abort', changed, {once: true});
    // A failed read draws nothing rather than a switch that can't say where it stands.
    void run(false, (client, signal) => client.preferences(signal)).then(preferences => {if (current()) setValue(preferences.serverOffline);}, () => {});
    return () => dispose();
  }, [context, hostedAccountId, lifetime, retirement, theme.isTV]);
  const shown = !(theme.isTV || value === undefined || !opening?.current());
  const apply = async (next: boolean) => {
    const held = opening;
    if (!held || busy || !held.current()) return;
    setBusy(true); setError(undefined);
    try {const saved = await held.save(next); if (held.current()) setValue(saved.serverOffline);}
    catch (failure) {if (held.current()) setError(failure);}
    finally {if (held.current()) setBusy(false);}
  };
  return {shown, value: value === true, busy, error, choose: (next: boolean) => {void apply(next);}, clearError: () => setError(undefined)};
}
