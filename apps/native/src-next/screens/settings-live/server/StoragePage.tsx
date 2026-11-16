import React, {useState} from 'react';
import {ScrollView, Text, View} from 'react-native';
import type {defaultI18n} from '../../../../../../packages/i18n/src';
import {ServerHeader} from '../../../kit/shell/ServerIdentity';
import {useTheme} from '../../../kit/theme';
import type {NativeSettingsLivePorts} from '../ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {StorageKeepRows} from './storage/KeepRows';
import {BackupsTab} from './storage/BackupsTab';
import {NativeStorageSpaceUsage} from './storage/SpaceUsage';
import {NativeEmptyCache} from './storage/EmptyCache';
import {settingsPageHeading, settingsPageInset} from './page-layout';

type ViewerRole = 'owner' | 'administrator' | 'member';
/** Storage & backups is one page; Missing titles belongs to Libraries. Every read is the rebuilt server's:
 * what uses space (GET /v1/admin/storage), Empty cache, backups, and Advanced › Keeping. */
export function NativeServerStorage({ports, runtime, compactHeader = false, onOpenPage}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; compactHeader?: boolean; onOpenPage: (page: 'history' | 'troubleshooting') => void}) {
  const theme = useTheme(), t = runtime.text.t as typeof defaultI18n.t, context = ports.context;
  const [advanced, setAdvanced] = useState(false);
  const viewerRole: ViewerRole = context.account.account.role === 'owner' ? 'owner' : context.account.canManage ? 'administrator' : 'member';
  return <View style={{flex:1}}><ScrollView contentContainerStyle={{...settingsPageInset(theme, compactHeader)}}>
    <ServerHeader serverName={context.serverName} viewerRole={viewerRole} audience={viewerRole === 'administrator' ? 'administrators' : 'owner'} variant="phone" style={{marginBottom:12}}/>
    {!compactHeader && <Text accessibilityRole="header" style={[settingsPageHeading(theme),{color:theme.colors.text}]}>{t('settings.server.storage')}</Text>}
    <View style={{marginTop:14}}><NativeStorageSpaceUsage context={context} runtime={runtime}/></View>
    <NativeEmptyCache context={context} runtime={runtime}/>
    <BackupsTab ports={ports} runtime={runtime} t={t} advancedOpen={advanced} onAdvancedOpenChange={setAdvanced}
      advancedContent={<StorageKeepRows context={context} t={t} onOpenPage={onOpenPage}/>}>
    </BackupsTab>
  </ScrollView></View>;
}
