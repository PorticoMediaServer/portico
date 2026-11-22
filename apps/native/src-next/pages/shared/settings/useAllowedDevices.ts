import {useEffect, useState, useSyncExternalStore} from 'react';
import {receiverAllowedDevices, type NativeAllowedDevices} from '../../../platform/play-on/receiver-allowed-storage';
import type {NativeSettingsPanelDeps} from '../../../screens/settings-live/settings-panel-deps';

const none: readonly Readonly<{signInId: string; name: string}>[] = Object.freeze([]);
const noSubscribe = () => () => {};

type Context = NativeSettingsPanelDeps['context'];
function signInScope(context: Context) {
  const session = context.session();
  return session && [session.viewer.serverId, session.viewer.accountId, session.sessionFamilyId].join(':');
}

export function useAllowedDevices({context, lifetime}: NativeSettingsPanelDeps) {
  const scope = signInScope(context);
  const [loaded, setLoaded] = useState<{scope: string; list: NativeAllowedDevices}>();
  useEffect(() => {
    if (!scope) return;
    let live = true;
    void receiverAllowedDevices(scope).then(list => {
      if (live && lifetime.current() && signInScope(context) === scope) setLoaded({scope, list});
    }).catch(() => {});
    return () => {live = false;};
  }, [context, lifetime, scope]);
  const list = loaded?.scope === scope ? loaded?.list : undefined;
  const devices = useSyncExternalStore(list?.subscribe ?? noSubscribe, list?.list ?? (() => none));
  const forget = (id: string) => {
    if (scope && signInScope(context) === scope && lifetime.current()) list?.forget(id);
  };
  return {devices, forget};
}
