import {useCallback, useEffect, useRef, useState} from 'react';
import {Linking} from 'react-native';
import {OwnerSignInSettingsClient, type OwnerSignInSettings} from '../../../../../../packages/client-core/src/account-move';
import {serverSignInSettingsURL} from '../../../../../../packages/client-core/src/sign-in';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {useSettingsRequestScope} from '../../../screens/settings-live/use-settings-request-scope';
import {useTheme} from '../../../kit/theme';

/** Server › Sign-in: native's sign-in contract is deliberately read-only. Ownership and sign-in moves continue in
 * this server's authenticated web settings; the one write is Finish now (frame 29), on the owner's own session.
 * Moved from NativeServerSignIn as it was. */
export function useServerSignIn({ports, onOpenPage}: {ports: NativeSettingsLivePorts; onOpenPage: (page: 'people' | 'remote') => void}) {
  const context = ports.context, tv = useTheme().isTV;
  const {api, lifetime, authority} = useSettingsRequestScope(context);
  const [answer, setAnswer] = useState<Readonly<{api: typeof api; value?: OwnerSignInSettings; error?: unknown}>>(), [revision, setRevision] = useState(0);
  const current = useCallback(() => lifetime.current() && context.account.capabilities.manageSignIn && context.account.account.role === 'owner'
    && context.account.account.primaryProfileId === authority.profileId, [lifetime, context, authority.profileId]);
  const clients = useRef<OwnerSignInSettingsClient | undefined>(undefined);
  useEffect(() => {
    if (!current()) return;
    let active = true;
    const client = new OwnerSignInSettingsClient(api, {serverId: authority.serverId, isCurrent: () => active && current(), capabilities: () => context.account.capabilities});
    clients.current = client;
    setAnswer(undefined);
    void client.read().then(value => {if (active && current()) setAnswer({api, value});}, error => {if (active && current()) setAnswer({api, error});});
    return () => {active = false; if (clients.current === client) clients.current = undefined; client.dispose();};
  }, [api, authority, context, lifetime, revision, current]);
  const value = answer?.api === api && current() ? answer.value : undefined;
  const error = answer?.api === api && current() ? answer.error : undefined;
  const refresh = () => {if (current()) setRevision(next => next + 1);};
  const [finishing, setFinishing] = useState(false), [finishBusy, setFinishBusy] = useState(false), [finishError, setFinishError] = useState<unknown>();
  const finish = async () => {
    const client = clients.current;
    if (!client || !current() || finishBusy) return;
    setFinishBusy(true); setFinishError(undefined);
    try {await client.finish(); if (current()) {setFinishing(false); refresh();}}
    catch (failure) {if (current()) setFinishError(failure);}
    finally {setFinishBusy(false);}
  };
  const webURL = serverSignInSettingsURL(context.api.baseUrl);
  const openWeb = () => {
    if (!value || !current()) return;
    // Only the selected server's origin is used; no credential or move token is
    // added, and visiting a local server never opens Portico Services.
    void Linking.openURL(serverSignInSettingsURL(context.api.baseUrl)).catch(failure => {if (current()) setAnswer({api, value, error: failure});});
  };
  const move = value?.move;
  return {tv, value, error, refresh, webURL, openWeb, serverName: context.serverName,
    progress: move && move.total !== undefined ? {total: move.total, done: move.total - move.waiting} : undefined,
    openPage: (page: 'people' | 'remote') => {if (current()) onOpenPage(page);},
    finishing: finishing && !!move, finishBusy, finishError,
    startFinish: () => {if (current()) {setFinishError(undefined); setFinishing(true);}},
    closeFinish: () => {if (!finishBusy) setFinishing(false);}, cancelFinish: () => setFinishing(false),
    finish: () => {void finish();}};
}
