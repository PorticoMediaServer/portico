import {clonePortableDto} from '../../../../../../../packages/client-core/src';
import React,{useCallback,useLayoutEffect,useMemo,useState,useSyncExternalStore} from 'react';
import {Text,View} from 'react-native';
import type {LibraryChannelBlock,LibraryChannelConfig,LibraryChannelRule} from '../../../../../../../packages/client-core/src/library-channels';
import type {BlockPresetsSource} from '../../../../../../../packages/client-core/src/server-admin/block-presets-source';
import {defaultI18n} from '../../../../../../../packages/i18n/src';
import {Button,Select,Switch,TextField} from '../../../../kit/controls';
import {BottomSheet} from '../../../../kit/overlays';
import {shellType} from '../../../../kit/shell/visual';
import {useTheme} from '../../../../kit/theme';
import {nativeChannelOperationId} from './model';
const minuteText=(value:number)=>`${String(Math.floor(value/60)).padStart(2,'0')}:${String(value%60).padStart(2,'0')}`;
function minuteValue(value:string):number|undefined{const match=/^([01]?\d|2[0-3]):([0-5]\d)$/.exec(value);return match?Number(match[1])*60+Number(match[2]):undefined;}
const dayLabel=(day:number)=>new Intl.DateTimeFormat(defaultI18n.locale,{weekday:'short',timeZone:'UTC'}).format(Date.UTC(2023,0,1+day));
type Draft=Readonly<{base:LibraryChannelConfig;original?:LibraryChannelBlock;block:LibraryChannelBlock;from:string;to:string;copiedRule?:LibraryChannelRule;abort:AbortController}>;
export function NativeChannelHours({config,presets,locked,isCurrent,onChange,onEditing,t}:{config:LibraryChannelConfig;presets:BlockPresetsSource;locked:boolean;isCurrent:()=>boolean;onChange:(update:(old:LibraryChannelConfig)=>LibraryChannelConfig)=>boolean;onEditing:(editing:boolean)=>void;t:typeof defaultI18n.t}) {
  const theme=useTheme(),presetState=useSyncExternalStore(presets.subscribe,presets.getSnapshot,presets.getSnapshot),[draft,setDraft]=useState<Draft>(),[error,setError]=useState(false),[applying,setApplying]=useState(false);
  const view=useMemo(()=>({active:true,draft:undefined as Draft|undefined,applying:undefined as AbortController|undefined}),[]);
  useLayoutEffect(()=>{view.active=true;return()=>{view.active=false;view.draft?.abort.abort();view.applying?.abort();view.draft=undefined;view.applying=undefined;};},[view]);
  const retire=useCallback((captured:Draft)=>{if(view.draft!==captured)return;view.draft=undefined;captured.abort.abort();view.applying?.abort();view.applying=undefined;setDraft(undefined);setApplying(false);setError(false);onEditing(false);},[view,onEditing]);
  useLayoutEffect(()=>{const captured=view.draft;if(captured&&captured.base!==config)retire(captured);},[config,view,retire]);
  const current=()=>view.active&&isCurrent()&&!locked;
  const held=draft&&view.draft===draft&&draft.base===config&&!draft.abort.signal.aborted&&current()?draft:undefined;
  const open=(original?:LibraryChannelBlock)=>{
    if(!current()||view.applying||original&&!config.blocks.includes(original))return;
    try{const block:LibraryChannelBlock=original?clonePortableDto(original):{id:'block-'+nativeChannelOperationId().slice(0,24),weekdays:[],startMinute:0,endMinute:0,priority:0,ruleId:config.defaultRuleId,fallbackRuleId:config.defaultRuleId,anchor:'channel-cursor',overrun:'finish'};
      view.draft?.abort.abort();const next:Draft={base:config,...(original?{original}:{}),block,from:original?minuteText(block.startMinute):'',to:original?minuteText(block.endMinute):'',abort:new AbortController()};view.draft=next;setDraft(next);setError(false);onEditing(true);
    }catch{setError(true);}
  };
  const update=(next:Draft)=>{if(!held||!current()||view.draft!==held||view.applying)return;view.draft=next;setDraft(next);};
  const close=()=>{if(held&&current()&&view.draft===held)retire(held);};
  const done=(remove=false)=>{
    const captured=held;if(!captured||!current()||view.draft!==captured||view.applying)return;
    const start=minuteValue(captured.from),end=minuteValue(captured.to),rules=captured.copiedRule?[...config.rules,captured.copiedRule]:config.rules;
    if(!remove&&(start===undefined||end===undefined||!captured.block.weekdays.length||!rules.some(rule=>rule.id===captured.block.ruleId)||captured.block.fallbackRuleId&&!rules.some(rule=>rule.id===captured.block.fallbackRuleId))){setError(true);return;}
    const block={...captured.block,startMinute:start??captured.block.startMinute,endMinute:end??captured.block.endMinute};
    if(onChange(old=>({...old,rules:!remove&&captured.copiedRule?[...old.rules,captured.copiedRule]:old.rules,blocks:remove?old.blocks.filter(value=>value.id!==block.id):captured.original?old.blocks.map(value=>value.id===block.id?block:value):[...old.blocks,block]})))retire(captured);
  };
  const choosePreset=async(record:(typeof presetState.items)[number])=>{
    const captured=held;if(!captured||!presets.getSnapshot().items.some(value=>value===record&&value.revision===record.revision)||!current()||view.draft!==captured||view.applying)return;
    const read=new AbortController();view.applying=read;setApplying(true);setError(false);
    const owns=()=>current()&&view.draft===captured&&view.applying===read&&!read.signal.aborted&&!captured.abort.signal.aborted;
    const abort=()=>read.abort();captured.abort.signal.addEventListener('abort',abort,{once:true});
    try{const result=await presets.apply(record,config,captured.block,read.signal,owns);if(!owns())return;const copiedRule=result?.rules.find(rule=>!config.rules.some(value=>value.id===rule.id));if(!copiedRule){setError(true);return;}
      const from=captured.from||(record.body.startMinute>=0?minuteText(record.body.startMinute):''),start=minuteValue(from),to=captured.to||(start===undefined?'':minuteText((start+record.body.durationMinutes)%1440)),days=['sunday','monday','tuesday','wednesday','thursday','friday','saturday'];
      const weekdays=captured.block.weekdays.length?captured.block.weekdays:record.body.days.length?record.body.days.map(value=>days.indexOf(value)):[0,1,2,3,4,5,6];
      const next:Draft={...captured,copiedRule,from,to,block:{...captured.block,ruleId:copiedRule.id,weekdays}};view.draft=next;setDraft(next);
    }finally{captured.abort.signal.removeEventListener('abort',abort);if(view.applying===read){view.applying=undefined;if(view.active){setApplying(false);}}}
  };
  const rules=held?.copiedRule?[...config.rules,held.copiedRule]:config.rules,ruleLabel=(id:string)=>id===config.defaultRuleId?t('builder.block.default'):rules.find(rule=>rule.id===id)?.name??t('error.unavailable');
  return <View style={{gap:10}}><Text style={[shellType(17,800),{color:theme.colors.text}]}>{t('builder.schedule.hours')}</Text><Text style={{color:theme.colors.textSecondary}}>{t('builder.schedule.help')}</Text><Text style={{color:theme.colors.textSecondary}}>{t(config.blocks.length?'builder.schedule.scheduled':'builder.schedule.always')}</Text>{config.blocks.map(block=><Button key={block.id} size="compact" variant="glass" disabled={locked} onPress={()=>open(block)}>{block.weekdays.map(dayLabel).join(', ')} · {minuteText(block.startMinute)}–{minuteText(block.endMinute)} · {ruleLabel(block.ruleId)}</Button>)}<Button size="compact" variant="glass" disabled={locked||config.blocks.length>=64} onPress={()=>open()}>{t('builder.schedule.add')}</Button><Text style={{color:theme.colors.textSecondary}}>{t('builder.schedule.otherwise')}</Text>
    <BottomSheet open={!!held} onOpenChange={value=>{if(!value)close();}} title={t('builder.block.edit')} footer={<View style={{flexDirection:'row',gap:8}}><Button variant="glass" onPress={close}>{t('action.cancel')}</Button>{held?.original&&<Button variant="glass" disabled={applying} onPress={()=>done(true)}>{t('builder.block.remove')}</Button>}<Button disabled={applying} loading={applying} onPress={()=>done()}>{t('action.done')}</Button></View>}><View style={{gap:12}}>{held&&<>{[1,2,3,4,5,6,0].map(day=><Switch key={day} checked={held.block.weekdays.includes(day)} disabled={applying} onCheckedChange={checked=>update({...held,block:{...held.block,weekdays:checked?[...held.block.weekdays.filter(value=>value!==day),day]:held.block.weekdays.filter(value=>value!==day)}})}>{dayLabel(day)}</Switch>)}<TextField label={t('builder.preset.starts')} value={held.from} maxLength={5} editable={!applying} placeholder="00:00" onChangeText={from=>update({...held,from})}/><TextField label={t('next.kit.filter.between')} value={held.to} maxLength={5} editable={!applying} placeholder="00:00" onChangeText={to=>update({...held,to})}/><Select label={t('builder.block.plays')} value={held.block.ruleId} disabled={applying} options={rules.map(rule=>({value:rule.id,label:ruleLabel(rule.id)}))} onValueChange={ruleId=>{if(rules.some(rule=>rule.id===ruleId))update({...held,copiedRule:held.copiedRule?.id===ruleId?held.copiedRule:undefined,block:{...held.block,ruleId}});}}/><Select label={t('builder.block.fallback')} value={held.block.fallbackRuleId} disabled={applying} options={[{value:'',label:t('builder.whenDone.slate')},...rules.map(rule=>({value:rule.id,label:ruleLabel(rule.id)}))]} onValueChange={fallbackRuleId=>{if(!fallbackRuleId||rules.some(rule=>rule.id===fallbackRuleId))update({...held,block:{...held.block,fallbackRuleId}});}}/><Select label={t('builder.block.anchor')} value={held.block.anchor} disabled={applying} options={[{value:'channel-cursor',label:t('builder.block.resume')},{value:'block-start',label:t('builder.block.restart')}]} onValueChange={anchor=>{if(anchor==='channel-cursor'||anchor==='block-start')update({...held,block:{...held.block,anchor}});}}/><Select label={t('builder.block.overrun')} value={held.block.overrun} disabled={applying} options={[{value:'finish',label:t('builder.block.finish')},{value:'cut',label:t('builder.block.cut')},{value:'fit',label:t('builder.block.fit')}]} onValueChange={overrun=>{if(overrun==='finish'||overrun==='cut'||overrun==='fit')update({...held,block:{...held.block,overrun}});}}/>{presetState.loaded&&presetState.items.length>0&&<Select label={t('builder.schedule.presets')} value="" disabled={applying||presetState.busy||!!presetState.pending||presetState.reconcile||config.rules.length>=16} options={presetState.items.map(record=>({value:record.id,label:record.name,disabled:record.body.order==='least-played'&&!presetState.applicationOrders?.includes('least-played')}))} onValueChange={id=>{const record=presetState.items.find(value=>value.id===id);if(record)void choosePreset(record);}}/>}</>}{error&&<Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t('builder.error.rejected')}</Text>}</View></BottomSheet>
  </View>;
}
