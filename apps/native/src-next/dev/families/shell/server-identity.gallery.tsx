import React from 'react';
import {ScrollView, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src/index';
import {shellNativeFixtures as fixture} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/shell-native-fixtures';
import type {Gallery} from '../../gallery-registry';
import {ServerMark, OwnerTag, ServerBand, ServerHeader} from '../../../kit/shell';
import {useTheme} from '../../../kit/theme';

function ServerIdentityGallery() {
  const theme = useTheme();
  const facts = [{id: 'health', text: fixture.running, tone: 'normal' as const}, {id: 'uptime', text: fixture.uptime}, {id: 'free', text: fixture.free}, {id: 'database', text: fixture.database}];
  return <ScrollView contentContainerStyle={{padding: theme.isTV ? 96 : 20, gap: 24}}>
    <Text style={[theme.type.heading, {color: theme.colors.text}]}>{defaultI18n.t('next.shell.case.ownerState')}</Text>
    <OwnerTag identity={fixture.serverName}/>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 10, width: 350}}>
      <ServerMark/><Text style={[theme.type.captionStrong, {color: theme.colors.text, flex: 1}]}>{defaultI18n.t('next.shell.forOwner')}</Text><OwnerTag variant="badge"/>
    </View>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 10, width: 350}}>
      <ServerMark variant="stateCompact"/><Text style={[theme.type.captionStrong, {color: theme.colors.text, flex: 1}]}>{defaultI18n.t('next.shell.forOwner')}</Text><OwnerTag variant="badge"/>
    </View>
    <View style={{width: theme.isTV ? 1070 : 358, overflow: 'hidden', borderRadius: 14, borderWidth: 1, borderColor: theme.colors.accentStrong}}>
      <ServerHeader serverName={fixture.serverName} viewerRole="owner" audience="owner" variant={theme.isTV ? 'group' : 'phone'}/>
    </View>
    <View style={{minHeight: theme.isTV ? 260 : 210, width: theme.isTV ? 1728 : 700, padding: 20, overflow: 'hidden', backgroundColor: theme.colors.ink}}>
      <ServerBand viewerRole="owner" audience="owner"/>
      <ServerHeader serverName={fixture.serverName} viewerRole="owner" audience="owner" facts={facts}/>
    </View>
    <View style={{minHeight: theme.isTV ? 260 : 210, width: theme.isTV ? 1728 : 700, padding: 20, overflow: 'hidden', backgroundColor: theme.colors.ink}}>
      <ServerBand viewerRole="owner" audience="administrators"/>
      <ServerHeader serverName={fixture.serverName} viewerRole="owner" audience="administrators" facts={facts}/>
    </View>
    <View style={{minHeight: theme.isTV ? 260 : 210, width: theme.isTV ? 1728 : 700, padding: 20, overflow: 'hidden', backgroundColor: theme.colors.ink}}>
      <ServerBand viewerRole="administrator" audience="administrators"/>
      <ServerHeader serverName={fixture.serverName} viewerRole="administrator" audience="administrators" facts={facts}/>
    </View>
  </ScrollView>;
}
const gallery: Gallery = {id: 'shell-server-identity', title: 'next.shell.native.identityGallery', render: ServerIdentityGallery, platforms: ['phone', 'tablet', 'tv']};
export default gallery;
