import React from 'react';
import {Text,View} from 'react-native';
import type {LibraryChannel} from '../../../../../../packages/client-core/src/server-admin/owner-library-channels';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../controls';
import {BottomSheet,MenuRow} from '../../overlays';
import {useTheme} from '../../theme';
import type {NativeChannelsGroup} from '../../../screens/settings-live/server/channels/model';
import {useChannelLogo} from '../../../pages/shared/live/channels/useChannelLogo';
const t=defaultI18n.t;
/** A channel's Upload logo row and its sheet: the channel's name, pick an image, its file name, Remove, Save. */
export function NativeChannelLogo({group,channel}:{group:NativeChannelsGroup;channel:LibraryChannel}){
 const theme=useTheme(),logo=useChannelLogo({group,channel}),{state,locked,picking,image}=logo;
 return <><MenuRow variant="grouped" icon="i-image" label={t('web.libraryChannels.uploadLogo')} disabled={locked} onPress={logo.show}/>
  <BottomSheet open={logo.open} onOpenChange={v=>{if(!v)logo.close();}} title={t('web.libraryChannels.uploadLogo')} footer={<View style={{flexDirection:'row',gap:8}}><Button variant="glass" disabled={state.busy} onPress={logo.close}>{t('action.cancel')}</Button><Button disabled={locked||picking||!image} loading={state.busy} onPress={logo.save}>{t('action.save')}</Button></View>}>
   <View style={{paddingHorizontal:theme.isTV?0:20,gap:12}}><Text style={{color:theme.colors.text}}>{channel.name}</Text><Button disabled={locked||picking} loading={picking} onPress={logo.pick}>{t('web.libraryChannels.uploadLogo')}</Button>{image&&<Text style={{color:theme.colors.textSecondary}}>{image.name}</Text>}{channel.logo&&<Button disabled={locked} onPress={logo.remove}>{t('builder.remove')}</Button>}{logo.error&&<Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t('error.unavailable')}</Text>}</View>
  </BottomSheet>
 </>;
}
