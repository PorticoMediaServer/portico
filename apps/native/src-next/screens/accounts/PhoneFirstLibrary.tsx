import React, {useLayoutEffect, useMemo, useRef, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {LibraryContentApi} from '../../../../../packages/client-core/src/library-content';
import {routeBase64} from '../../../../../packages/client-core/src/route-identity';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {NativeScreenLifetime} from '../settings-live/lifetime';
import {NativeOwnerLibrariesController} from '../settings-live/owner-libraries/controller';
import {AddLibraryPage} from '../settings-live/owner-libraries/AddLibraryPage';
import {nativeRouteCrypto} from '../../platform/server-connections';
import {useTheme} from '../../kit/theme';
import {StandardBackdrop} from '../../kit/artwork';

/** Setup reuses the real owner library editor and server folder explorer, after
 * normal session admission. No setup token or pending claim can reach this page. */
export function PhoneFirstLibrary({context, onDone}: {context: NativeShellBootstrapContext; onDone: () => void}) {
  const theme = useTheme(), live = useRef<{context: NativeShellBootstrapContext; active: boolean; flight: boolean; retire: () => void} | undefined>(undefined);
  const value = useMemo(() => {
    const lifetime = new NativeScreenLifetime(context);
    const ticket = {context, active: true, flight: false, retire: () => {}};
    const assertOwner = () => {
      lifetime.assert();
      const account = context.account;
      if (live.current !== ticket || !ticket.active || account.account.role !== 'owner' || account.account.disabled || !account.canManage
        || !account.capabilities.manageServer || account.account.primaryProfileId !== lifetime.authority.profileId)
        throw Object.assign(new Error('owner_required'), {code: 'owner_required'});
    };
    const originalApi = context.api;
    const api: LibraryContentApi = {
      ...(originalApi.acknowledgeUserMutation ? {acknowledgeUserMutation: async (id: string) => {
        assertOwner(); await originalApi.acknowledgeUserMutation(id); assertOwner();
      }} : {}),
      ...(originalApi.awaitUserMutationsReady ? {awaitUserMutationsReady: async () => {
        assertOwner(); await originalApi.awaitUserMutationsReady(); assertOwner();
      }} : {}),
      ...(originalApi.getUserMutationCoordinator ? {getUserMutationCoordinator: () => {
        assertOwner(); const value = originalApi.getUserMutationCoordinator(); assertOwner(); return value;
      }} : {}),
      ...(originalApi.getUserMutationScope ? {getUserMutationScope: () => {
        assertOwner(); const value = originalApi.getUserMutationScope(); assertOwner(); return value;
      }} : {}),
      request: async <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => {
      assertOwner(); const result = await lifetime.request(current => originalApi.request<T>(path, method, body, current), signal);
      assertOwner(); return result;
    }};
    const scope = {serverId: lifetime.authority.serverId, viewerId: JSON.stringify([lifetime.authority.epoch, lifetime.authority.accountId, lifetime.authority.profileId])};
    const controller = new NativeOwnerLibrariesController(api, scope, () => routeBase64(nativeRouteCrypto.random(24)));
    ticket.retire = () => {ticket.active = false; controller.dispose(); lifetime.dispose();};
    return {ticket, lifetime, assertOwner, api, controller};
  }, [context]);
  if (live.current !== value.ticket) {live.current?.retire(); live.current = value.ticket;}
  const current = () => live.current === value.ticket && value.ticket.active && value.ticket.context === context && value.lifetime.current();
  const state = useSyncExternalStore(value.controller.subscribe, value.controller.getSnapshot);
  useLayoutEffect(() => () => {value.ticket.retire(); if (live.current === value.ticket) live.current = undefined;}, [value]);
  const finish = () => {if (!current() || value.ticket.flight) return; value.assertOwner(); value.ticket.retire(); if (live.current === value.ticket) live.current = undefined; onDone();};
  return <View style={{flex: 1}}><StandardBackdrop/>
    <View style={{paddingHorizontal: 20, paddingTop: 30, paddingBottom: 18}}><Text style={[theme.type.title, {color: theme.colors.text}]}>{defaultI18n.t('web.setup.firstLibrary')}</Text></View>
    <AddLibraryPage firstRun api={value.api} serverName={context.serverName} busy={state.busy || value.ticket.flight} error={state.error}
      onCancel={finish} onCreate={input => {
        if (!current() || value.ticket.flight) return; value.assertOwner(); value.ticket.flight = true;
        void value.controller.create(input).then(id => {
          if (!current()) return;
          value.ticket.flight = false; if (id) finish();
        }, () => {value.ticket.flight = false;});
      }}/>
  </View>;
}
