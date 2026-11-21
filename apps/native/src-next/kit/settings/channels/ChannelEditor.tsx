import React,{useEffect,useState,useSyncExternalStore} from 'react';
import {Modal,Text,View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {validBuilder} from '../../../../../../packages/client-core/src/server-admin/owner-channel-draft';
import {defaultI18n,type MessageId} from '../../../../../../packages/i18n/src';
import {Artwork} from '../../artwork';
import {Layer} from '../../shell/Layer';
import {Button,Select,Switch,TextField} from '../../controls';
import {useTheme} from '../../theme';
import {useMotion} from '../../useMotion';
import {FormScrollView} from '../../../platform/keyboard';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeChannelsGroup} from '../../../screens/settings-live/server/channels/model';
import {NativeChannelSelection} from './ChannelSelection';
import {NativeChannelHours} from './ChannelHours';
import type {Editor} from '../../../screens/settings-live/server/channels/source';
const t=defaultI18n.t;
export function NativeChannelEditor({group,runtime,editor}:{group:NativeChannelsGroup;runtime:NativeShellRuntime;editor:Editor}){
 const {source}=group,state=useSyncExternalStore(source.subscribe,source.getSnapshot),theme=useTheme(),insets=useSafeAreaInsets(),motion=useMotion(),[advanced,setAdvanced]=useState(false),[tried,setTried]=useState(false),[previewOpen,setPreviewOpen]=useState<typeof state.preview>(),draft=editor.draft,builder=draft.builder;
 const locked=!!(state.busy||state.uncertain||state.pendingCreate||state.pendingPreset),current=()=>group.current()&&source.getSnapshot().editor===editor;
 const change=(patch:Partial<typeof draft>)=>{if(current())source.edit(editor,value=>({...value,...patch}));};
 let zoneValid=false;try{if(draft.timeZone){new Intl.DateTimeFormat(undefined,{timeZone:draft.timeZone});zoneValid=true;}}catch{/* Keep invalid text editable. */}
 const valid=!!draft.name.trim()&&zoneValid&&validBuilder(builder),preview=state.previewFor===draft?state.preview:undefined;
 useEffect(()=>{if(!valid||locked||editor.conflict)return;const timer=setTimeout(()=>{if(group.current()&&source.getSnapshot().editor===editor)void source.preview(editor);},600);return()=>clearTimeout(timer);},[source,editor,valid,locked,group]);
 const close=()=>{if(current()&&!state.busy){if(previewOpen)setPreviewOpen(undefined);else source.close(editor);}};
 return <Modal visible={current()} animationType={motion.reduce?'fade':'slide'} presentationStyle="fullScreen" onRequestClose={close}><Layer style={{paddingTop:insets.top,paddingBottom:insets.bottom}}>
  <View style={{minHeight:56,paddingHorizontal:14,flexDirection:'row',alignItems:'center',gap:10}}><Button variant="text" size="compact" disabled={state.busy} onPress={close}>{t('builder.cancel')}</Button><Text style={[theme.type.rowHelp,{flex:1,textAlign:'center',color:theme.colors.text}]}>{editor.templateName?t('builder.reviewTemplate',{name:editor.templateName}):t(editor.document?'builder.editTitle':'builder.newTitle')}</Text><Button size="compact" disabled={state.busy||state.uncertain||editor.conflict} loading={state.busy} onPress={()=>{if(!current())return;setTried(true);if(valid)void source.save(editor);}}>{t(state.pendingCreate?'action.tryAgain':editor.document?'builder.save':'builder.create')}</Button></View>
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
   {editor.conflict&&<><Text style={{color:theme.colors.record}}>{t('web.ownerChannels.changed')}</Text><Button disabled={state.busy} onPress={()=>{if(current())source.acceptLocal(editor);}}>{t('web.ownerChannels.keepEdits')}</Button></>}
   {editor.document&&<Button disabled={state.busy} onPress={()=>{if(current())void source.reloadEditor(editor);}}>{t('action.refresh')}</Button>}
  </FormScrollView>
  <View testID="channel-preview-bar" style={{paddingHorizontal:20,paddingVertical:12,borderTopWidth:1,borderColor:theme.colors.lineSoft,backgroundColor:theme.colors.raised,flexDirection:'row',alignItems:'center',gap:12}}><View style={{flex:1}}><Text style={{color:theme.colors.text}}>{state.previewLoading?t('builder.previewing'):preview?t('builder.matches',{count:preview.titles}):t('builder.preview')}</Text>{preview&&<Text style={{color:theme.colors.textSecondary}}>{t('builder.hours',{hours:Number(preview.hours.toFixed(1))})}</Text>}</View><Button size="compact" disabled={!preview||state.previewLoading} onPress={()=>{if(current()&&preview)setPreviewOpen(preview);}}>{t('builder.preview')}</Button></View>
  <Modal visible={current()&&!!preview&&previewOpen===preview} animationType={motion.reduce?'fade':'slide'} presentationStyle="fullScreen" onRequestClose={()=>setPreviewOpen(undefined)}><Layer style={{paddingTop:insets.top,paddingBottom:insets.bottom}}><Button variant="text" onPress={()=>setPreviewOpen(undefined)}>{t('action.back')}</Button><FormScrollView contentContainerStyle={{padding:20,gap:18}}>
   {preview&&<><Text style={{color:theme.colors.text}}>{t('builder.matches',{count:preview.titles})}</Text>{preview.unplaceable>0&&<Text style={{color:theme.colors.textSecondary}}>{t('builder.unresolved',{count:preview.unplaceable})}</Text>}{preview.sample.map(sample=><View key={sample.id} style={{flexDirection:'row',gap:12,alignItems:'center'}}><Artwork source={runtime.artwork(sample.images?.poster)} alt={sample.title} width={60} height={90}/><Text style={{flex:1,color:theme.colors.text}}>{[sample.title,sample.episodeTitle,sample.year].filter(Boolean).join(' · ')}</Text></View>)}<Text style={{color:theme.colors.text}}>{t('builder.firstDay')}</Text>{preview.today.map((row,index)=><Text key={index} style={{color:theme.colors.textSecondary}}>{new Intl.DateTimeFormat(runtime.text.locale,{timeZone:draft.timeZone,hour:'numeric',minute:'2-digit'}).format(new Date(row.startsAt))} · {row.title?.title??t('builder.nothingScheduled')}</Text>)}</>}
  </FormScrollView></Layer></Modal>
 </Layer></Modal>;
}
