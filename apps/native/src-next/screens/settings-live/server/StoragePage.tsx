import React, {useState} from 'react';
import {View} from 'react-native';
import type {defaultI18n} from '../../../../../../packages/i18n/src';
import {ServerHeader} from '../../../kit/shell/ServerIdentity';
import {SettingsPageFrame} from '../../../kit/frames';
import {useFrameMetrics} from '../../../kit/frames/metrics';
import type {NativeSettingsLivePorts} from '../ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {StorageKeepRows} from './storage/KeepRows';
import {BackupsTab} from './storage/BackupsTab';
import {NativeStorageSpaceUsage} from './storage/SpaceUsage';
import {NativeEmptyCache} from './storage/EmptyCache';

type ViewerRole = 'owner' | 'administrator' | 'member';
/** Storage & backups is one page; Missing titles belongs to Libraries. Every read is the rebuilt server's:
 * what uses space (GET /v1/admin/storage), Empty cache, backups, and Advanced › Keeping. It stands on the Settings page
 * frame (header, Back, server strip, gutters, scroll); `notice` is the screen's own error, shown first. */
export function NativeServerStorage({ports, runtime, back, notice, onOpenPage}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; back?: Readonly<{label: string; onPress: () => void}>; notice?: React.ReactNode; onOpenPage: (page: 'history' | 'troubleshooting') => void}) {
  const frame = useFrameMetrics(), t = runtime.text.t as typeof defaultI18n.t, context = ports.context;
  const [advanced, setAdvanced] = useState(false);
  const viewerRole: ViewerRole = context.account.account.role === 'owner' ? 'owner' : context.account.canManage ? 'administrator' : 'member';
  return <SettingsPageFrame title={t('settings.server.storage')} back={back}
    server={<ServerHeader serverName={context.serverName} viewerRole={viewerRole} audience={viewerRole === 'administrator' ? 'administrators' : 'owner'} variant={frame.form === 'phone' ? 'phone' : 'page'}/>}>
    {notice}<View>
    <View style={{marginTop:14}}><NativeStorageSpaceUsage context={context} runtime={runtime}/></View>
    <NativeEmptyCache context={context} runtime={runtime}/>
    <BackupsTab ports={ports} runtime={runtime} t={t} advancedOpen={advanced} onAdvancedOpenChange={setAdvanced}
      advancedContent={<StorageKeepRows context={context} t={t} onOpenPage={onOpenPage}/>}>
    </BackupsTab>
  </View></SettingsPageFrame>;
}
