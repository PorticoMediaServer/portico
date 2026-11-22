import React from 'react';
import {Modal,Text,View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {validBuilder} from '../../../../../../packages/client-core/src/server-admin/owner-channel-draft';
import {channelPreviewLength} from '../../../../../../packages/client-core/src/presentation/kit-model/channels';
import {defaultI18n,type MessageId} from '../../../../../../packages/i18n/src';
import {Artwork, missingArt} from '../../artwork';
import {TopBar} from '../../layout/TopBar';
import {Layer} from '../../shell/Layer';
import {Button,Select,Switch,TextField} from '../../controls';
import {textLines, useTheme} from '../../theme';
import {useMotion} from '../../useMotion';
import {useNativeModalPresence} from '../../overlays/native-dismissal';
import {FormScrollView} from '../../../platform/keyboard';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeChannelsGroup} from '../../../screens/settings-live/server/channels/model';
import type {Editor} from '../../../screens/settings-live/server/channels/source';
import {NativeChannelSelection} from './ChannelSelection';
import {NativeChannelHours} from './ChannelHours';
import {useChannelEditor} from '../../../pages/shared/live/channels/useChannelEditor';
const t=defaultI18n.t;
/** The channel editor, a window of its own: Cancel, the title and Save; the channel's fields, its hours and advanced
 * settings; the preview bar, and the preview as a second window. */
export function NativeChannelEditor({group,runtime,editor}:{group:NativeChannelsGroup;runtime:NativeShellRuntime;editor:Editor}){
 const theme=useTheme(),insets=useSafeAreaInsets(),motion=useMotion(),view=useChannelEditor({group,editor});
 const {state,advanced,setAdvanced,tried,previewOpen,setPreviewOpen,draft,builder,locked,current,change,zoneValid,preview,close}=view;
 // Both windows take the shared presence: on iOS a Modal asked for while another is still leaving (the channel's menu,
 // inside the Settings sheet) is dropped without a word, and the net asks again (finisher, iPhone, 22 Nov: 2 of 3 Edits
 // left a dimmed list and no editor).
 const editorShown=current(),previewShown=editorShown&&!!preview&&previewOpen===preview;
 const editorWindow=useNativeModalPresence(editorShown),previewWindow=useNativeModalPresence(previewShown);
 if(!editorWindow.mounted)return null;
 return <Modal key={editorWindow.key} visible={editorWindow.visible} onShow={editorWindow.onShow} onDismiss={editorWindow.onDismiss} animationType={motion.reduce?'fade':'slide'} presentationStyle="fullScreen" onRequestClose={close}><Layer style={{paddingTop:insets.top,paddingBottom:insets.bottom}}>
  <View style={{minHeight:56,paddingHorizontal:14,flexDirection:'row',alignItems:'center',gap:10}}><Button variant="text" size="compact" disabled={state.busy} onPress={close}>{t('builder.cancel')}</Button><Text {...textLines(1)} style={[theme.type.barTitle,{flex:1,textAlign:'center',color:theme.colors.text}]}>{editor.templateName?t('builder.reviewTemplate',{name:editor.templateName}):t(editor.document?'builder.editTitle':'builder.newTitle')}</Text><Button size="compact" disabled={state.busy||state.uncertain||editor.conflict} loading={state.busy} onPress={view.save}>{t(state.pendingCreate?'action.tryAgain':editor.document?'action.save':'builder.createShort')}</Button></View>
  <FormScrollView contentContainerStyle={{padding:20,gap:14,paddingBottom:40}} keyboardShouldPersistTaps="handled" keyboardDismissMode="interactive">
   <TextField label={t('builder.name')} value={draft.name} maxLength={120} editable={!locked} error={tried&&!draft.name.trim()?t('builder.error.name'):undefined} onChangeText={name=>change({name})}/><TextField label={t('builder.description')} value={draft.description} maxLength={2048} editable={!locked} onChangeText={description=>change({description})}/>
   <NativeChannelSelection group={group} value={builder} locked={locked} onChange={value=>change({builder:{...builder,...value}})}/>
   {tried&&!builder.libraries.length&&<Text style={{color:theme.colors.record}}>{t('builder.error.libraries')}</Text>}{tried&&!builder.kinds.length&&<Text style={{color:theme.colors.record}}>{t('builder.error.kinds')}</Text>}{tried&&!validBuilder(builder)&&<Text style={{color:theme.colors.record}}>{t('builder.error.conditions')}</Text>}
   <Switch checked={draft.on} disabled={locked} onCheckedChange={on=>change({on})}>{t('builder.on')}</Switch>
   <NativeChannelHours group={group} value={builder} locked={locked} onChange={builder=>change({builder})}/><Text style={{color:theme.colors.textSecondary}}>{t('builder.audience')}</Text>
   <Button size="compact" variant="text" onPress={()=>setAdvanced(v=>!v)}>{t(advanced?'builder.hideAdvanced':'builder.advanced')}</Button>
   {advanced&&<><TextField label={t('builder.avoidRepeats')} description={t('builder.avoidRepeatsHelp')} value={String(builder.avoidRepeats??5)} editable={!locked} keyboardType="numeric" onChangeText={text=>{const n=Number(text);if(Number.isInteger(n)&&n>=0&&n<=1000)change({builder:{...builder,avoidRepeats:n}});}}/>
    <Select label={t('builder.quality')} value={builder.quality.mode??'auto'} disabled={locked} options={['auto','original','limited'].map(value=>({value,label:t(`builder.quality.${value==='auto'?'automatic':value}` as MessageId)}))} onValueChange={mode=>change({builder:{...builder,quality:{...builder.quality,mode,maxKbps:mode==='limited'?builder.quality.maxKbps||5000:builder.quality.maxKbps}}})}/>
    {builder.quality.mode==='limited'&&<TextField label={t('web.ownerChannels.maxKbps')} value={String(builder.quality.maxKbps??5000)} editable={!locked} keyboardType="numeric" onChangeText={text=>{const n=Number(text);if(Number.isInteger(n)&&n>=500&&n<=200000)change({builder:{...builder,quality:{...builder.quality,maxKbps:n}}});}}/>}
    <TextField label={t('builder.timeZone')} value={draft.timeZone} editable={!locked} error={tried&&!zoneValid?t('builder.error.timeZone'):undefined} onChangeText={timeZone=>change({timeZone})}/>
    <Switch checked={!!builder.logoOnScreen.on} disabled={locked||!editor.document?.logo} onCheckedChange={on=>change({builder:{...builder,logoOnScreen:{...builder.logoOnScreen,on}}})}>{t('web.ownerChannels.overlay')}</Switch>{!editor.document?.logo&&<Text style={{color:theme.colors.textSecondary}}>{t('web.ownerChannels.needsLogo')}</Text>}
    {builder.logoOnScreen.on&&<><Select label={t('web.ownerChannels.corner')} value={builder.logoOnScreen.corner??'topRight'} disabled={locked} options={['topRight','topLeft','bottomRight','bottomLeft'].map(value=>({value,label:t(`web.ownerChannels.${value}` as MessageId)}))} onValueChange={corner=>change({builder:{...builder,logoOnScreen:{...builder.logoOnScreen,corner}}})}/><Select label={t('web.ownerChannels.size')} value={builder.logoOnScreen.size??'medium'} disabled={locked} options={['small','medium','large'].map(value=>({value,label:t(`web.ownerChannels.${value}` as MessageId)}))} onValueChange={size=>change({builder:{...builder,logoOnScreen:{...builder.logoOnScreen,size}}})}/></>}
   </>}
   {!!state.error&&<Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t(state.pendingCreate?'error.unconfirmedChange':'error.unavailable')}</Text>}
   {editor.conflict&&<><Text style={{color:theme.colors.record}}>{t('web.ownerChannels.changed')}</Text><Button disabled={state.busy} onPress={view.acceptLocal}>{t('web.ownerChannels.keepEdits')}</Button></>}
   {editor.document&&<Button disabled={state.busy} onPress={view.reload}>{t('action.refresh')}</Button>}
  </FormScrollView>
  <View testID="channel-preview-bar" style={{paddingHorizontal:20,paddingVertical:12,borderTopWidth:1,borderColor:theme.colors.lineSoft,backgroundColor:theme.colors.raised,flexDirection:'row',alignItems:'center',gap:12}}><View style={{flex:1}}><Text style={{color:theme.colors.text}}>{state.previewLoading?t('builder.previewing'):preview?t('builder.matches',{count:preview.titles}):t('builder.preview')}</Text>{preview&&<Text style={{color:theme.colors.textSecondary}}>{(length=>t(length.id,length.values))(channelPreviewLength(preview))}</Text>}</View><Button size="compact" disabled={!preview||state.previewLoading} onPress={view.showPreview}>{t('builder.preview')}</Button></View>
  {previewWindow.mounted&&<Modal key={previewWindow.key} visible={previewWindow.visible} onShow={previewWindow.onShow} onDismiss={previewWindow.onDismiss} animationType={motion.reduce?'fade':'slide'} presentationStyle="fullScreen" onRequestClose={()=>setPreviewOpen(undefined)}><Layer style={{paddingTop:insets.top,paddingBottom:insets.bottom}}>
   {/* The preview is a page of its own: the kit bar with Back and its title, then what the channel would hold and play. */}
   <TopBar safeArea={false} back={{label:t('action.back'),onPress:()=>setPreviewOpen(undefined),testID:'channel-preview-back'}} title={t('builder.preview')}/>
   <FormScrollView contentContainerStyle={{paddingHorizontal:20,paddingBottom:20,gap:18}}>
   {preview&&<><View style={{gap:4}}>
     <Text style={[theme.type.rowHeading,{color:theme.colors.text}]}>{t('builder.matches',{count:preview.titles})}</Text>
     <Text style={[theme.type.rowHelp,{color:theme.colors.textSecondary}]}>{(length=>t(length.id,length.values))(channelPreviewLength(preview))}</Text>
     {preview.unplaceable>0&&<Text style={[theme.type.rowHelp,{color:theme.colors.textTertiary}]}>{t('builder.unresolved',{count:preview.unplaceable})}</Text>}
    </View>
    {preview.sample.length>0&&<View style={{gap:12}}>
     <Text accessibilityRole="header" style={theme.type.sectionLabel}>{t('builder.samples')}</Text>
     {preview.sample.map(sample=>{const title=[sample.title,sample.episodeTitle,sample.year].filter(Boolean).join(' · ');
      // A title with no poster keeps its place in the column, on the generated cover.
      return <View key={sample.id} style={{flexDirection:'row',gap:12,alignItems:'center'}}><Artwork source={runtime.artwork(sample.images?.poster)} alt={sample.title} width={60} height={90}
       fallback={missingArt({title:sample.title,shape:'poster',width:60,height:90})}/><Text style={[theme.type.rowLabel,{flex:1,color:theme.colors.text}]}>{title}</Text></View>;})}
    </View>}
    <View style={{gap:8}}>
     <Text accessibilityRole="header" style={theme.type.sectionLabel}>{t('builder.firstDay')}</Text>
     {preview.today.map((row,index)=><View key={index} style={{flexDirection:'row',gap:12,alignItems:'baseline'}}>
      <Text style={[theme.type.rowHelp,{color:theme.colors.textTertiary,minWidth:72}]}>{new Intl.DateTimeFormat(runtime.text.locale,{timeZone:draft.timeZone,hour:'numeric',minute:'2-digit'}).format(new Date(row.startsAt))}</Text>
      <Text style={[theme.type.rowLabel,{flex:1,color:row.title?theme.colors.text:theme.colors.textSecondary}]}>{row.title?.title??t('builder.nothingScheduled')}</Text>
     </View>)}
    </View></>}
  </FormScrollView></Layer></Modal>}
 </Layer></Modal>;
}
