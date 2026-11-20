import {useCallback, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {Share} from 'react-native';
import {assistantAddress} from '../../../../../../packages/client-core/src/server-admin/api-words';
import {IntegrationSwitchesSource, type IntegrationSwitchesSnapshot} from '../../../../../../packages/client-core/src/server-admin/integration-switches';
import {copyText} from '../../../platform/clipboard';
import {NativeScreenLifetime} from '../../../screens/settings-live/lifetime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {createNativeApiKeys} from './useServerApiKeys';

export function useServerIntegrations({ports, runtime}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime}) {
  const {context, requestId} = ports, {reportError} = runtime;
  const [tab, setTab] = useState('keys');
  const [keys, setKeys] = useState<ReturnType<typeof createNativeApiKeys>>();
  const [keyCreateRequest, setKeyCreateRequest] = useState(0), keyRequest = useRef(0), keySequence = useRef(0);
  useLayoutEffect(() => {
    const next = createNativeApiKeys(context, requestId, reportError);
    setKeys(next);
    return () => {keyRequest.current = 0; next.dispose();};
  }, [context, requestId, reportError]);
  const current = () => keys?.context === context && keys.current();
  const consumeKeyCreate = useCallback((request: number) => {
    if (keys?.context !== context || !keys.current() || keyRequest.current !== request) return false;
    keyRequest.current = 0;
    setKeyCreateRequest(0);
    return true;
  }, [keys, context]);
  const openCreateKey = () => {
    if (!current()) return;
    const request = ++keySequence.current;
    keyRequest.current = request;
    setKeyCreateRequest(request);
    setTab('keys');
  };
  return {tab, setTab, keys: current() ? keys : undefined, keyCreateRequest, consumeKeyCreate, openCreateKey, current};
}

export function useAssistantAddress({ports, runtime, isCurrent}: {
  ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; isCurrent: () => boolean;
}) {
  const {context} = ports, {t} = runtime.text;
  const address = assistantAddress(context.api.baseUrl);
  const [copied, setCopied] = useState(false), [failed, setFailed] = useState(false);
  const scope = useMemo(() => ({context, address, active: true}), [context, address]);
  useLayoutEffect(() => {
    scope.active = true; setCopied(false); setFailed(false);
    return () => {scope.active = false;};
  }, [scope]);
  const current = () => scope.active && scope.context === context && scope.address === address && context.current() && isCurrent();
  const copy = async () => {
    if (!current()) return;
    try {await copyText(address); if (current()) {setCopied(true); setFailed(false);}}
    catch {if (current()) setFailed(true);}
  };
  const share = async () => {
    if (!current()) return;
    try {await Share.share({title: t('api.mcp.title'), message: address});}
    catch {if (current()) setFailed(true);}
  };
  return {address, copied, failed, copy, share, current};
}

const idle: IntegrationSwitchesSnapshot = Object.freeze({phase: 'loading'});
const noSubscribe = () => () => {};
const getIdle = () => idle;

export function useIntegrationCompatibility({context}: NativeSettingsLivePorts) {
  const [group, setGroup] = useState<{context: typeof context; source: IntegrationSwitchesSource}>();
  useLayoutEffect(() => {
    const lifetime = new NativeScreenLifetime(context);
    const source = new IntegrationSwitchesSource(context.api, lifetime.current);
    const dispose = () => source.dispose();
    lifetime.signal.addEventListener('abort', dispose, {once: true});
    setGroup({context, source});
    void source.load();
    return () => {dispose(); lifetime.signal.removeEventListener('abort', dispose); lifetime.dispose();};
  }, [context]);
  const source = group?.context === context ? group.source : undefined;
  const snapshot = source?.getSnapshot ?? getIdle;
  const state = useSyncExternalStore(source?.subscribe ?? noSubscribe, snapshot, snapshot);
  return {
    state,
    retry: () => {void source?.load();},
    setCompatible: (checked: boolean) => {void source?.set('compatibleRoutes', checked);},
  };
}
