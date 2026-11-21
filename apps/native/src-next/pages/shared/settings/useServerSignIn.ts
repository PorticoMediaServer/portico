import {useCallback, useEffect, useRef, useState} from 'react';
import {Linking} from 'react-native';
import {OwnerSignInSettingsClient, type OwnerSignInSettings} from '../../../../../../packages/client-core/src/account-move';
import {serverSignInSettingsURL} from '../../../../../../packages/client-core/src/sign-in';
import {useTheme} from '../../../kit/theme';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {useSettingsRequestScope} from '../../../screens/settings-live/use-settings-request-scope';

type Opening = Readonly<{client: OwnerSignInSettingsClient; move: NonNullable<OwnerSignInSettings['move']>}>;

export function useServerSignIn({context}: NativeSettingsLivePorts) {
  const {api, lifetime, authority} = useSettingsRequestScope(context), {isTV} = useTheme();
  const [answer, setAnswer] = useState<Readonly<{client: OwnerSignInSettingsClient; value?: OwnerSignInSettings; error?: unknown}>>();
  const [revision, setRevision] = useState(0);
  const clients = useRef<OwnerSignInSettingsClient | undefined>(undefined);
  const current = useCallback(() => lifetime.current() && context.account.capabilities.manageSignIn && context.account.account.role === 'owner'
    && context.account.account.primaryProfileId === authority.profileId, [lifetime, context, authority.profileId]);
  useEffect(() => {
    if (!current()) return;
    let active = true;
    const client = new OwnerSignInSettingsClient(api, {
      serverId: authority.serverId, isCurrent: () => active && current(), capabilities: () => context.account.capabilities,
    });
    clients.current = client;
    setAnswer(undefined);
    void client.read().then(value => {if (active && current()) setAnswer({client, value});},
      error => {if (active && current()) setAnswer({client, error});});
    return () => {active = false; if (clients.current === client) clients.current = undefined; client.dispose();};
  }, [api, authority, context, lifetime, revision, current]);

  const authenticated = answer && answer.client === clients.current && current();
  const value = authenticated ? answer.value : undefined, error = authenticated ? answer.error : undefined;
  const refresh = () => {if (current()) setRevision(next => next + 1);};
  const [opening, setOpening] = useState<Opening>(), open = useRef<Opening | undefined>(undefined);
  const flight = useRef<Opening | undefined>(undefined);
  const [finishBusy, setFinishBusy] = useState(false), [finishError, setFinishError] = useState<unknown>();
  const live = !!opening && open.current === opening && opening.client === clients.current && opening.move === value?.move && current();
  const closeFinish = () => {
    if (!live || flight.current === opening) return;
    open.current = undefined;
    setOpening(undefined);
  };
  const askFinish = () => {
    const client = clients.current, move = value?.move;
    if (!client || !move?.waiting || !current() || flight.current) return;
    const next = {client, move};
    open.current = next;
    setOpening(next);
    setFinishError(undefined);
  };
  const finish = async () => {
    if (!opening || !live || open.current !== opening || flight.current || !current()) return;
    flight.current = opening;
    setFinishBusy(true); setFinishError(undefined);
    const owned = () => flight.current === opening && open.current === opening && clients.current === opening.client && current();
    try {
      await opening.client.finish();
      if (owned()) {open.current = undefined; setOpening(undefined); refresh();}
    } catch (failure) {if (owned()) setFinishError(failure);}
    finally {
      if (flight.current === opening) {flight.current = undefined; if (current()) setFinishBusy(false);}
    }
  };
  const url = serverSignInSettingsURL(context.api.baseUrl);
  const openWeb = () => {
    const client = clients.current;
    if (!client || !value || !current()) return;
    void Linking.openURL(url).catch(error => {if (current() && clients.current === client) setAnswer({client, value, error});});
  };
  return {value, error, current, refresh, url, openWeb, isTV, finishing: live, finishBusy, finishError, askFinish, closeFinish, finish};
}
