import {useEffect,useLayoutEffect,useRef,useState} from 'react';
import type {I18n} from '@i18n';
import {Segmented,Switch} from '../../kit/controls';
import {TrackChoices} from '../../kit/media';
import {Icon, type IconName} from '../../kit/Icon';
import {BottomSheet,Dialog,Menu} from '../../kit/overlays';
import type {WatchPanelContext} from './types';
import {phoneOptionRows,type WatchPanelModel,type WatchRadioChoice} from './panel-model';
import styles from './panels.module.css';

type Section = 'subtitles' | 'audio' | 'quality' | 'speed';
const SUBTITLE_SIZES = ['small','medium','large','extra-large'] as const;
const SUBTITLE_BACKGROUNDS = ['none','translucent','opaque'] as const;
const SIZE_LABEL = {small:'settings.subtitleSize.small',medium:'settings.subtitleSize.medium',large:'settings.subtitleSize.large','extra-large':'settings.subtitleSize.extra-large'} as const;
const BACKGROUND_LABEL = {none:'settings.subtitleBackground.none',translucent:'settings.subtitleBackground.translucent',opaque:'settings.subtitleBackground.opaque'} as const;
const sectionOf = (panel: string | null): Section | undefined => panel === 'subtitles-audio' ? undefined : panel === 'subtitles' || panel === 'audio' || panel === 'quality' || panel === 'speed' ? panel : undefined;

function RadioRows({rows,select,reportError,label}:{rows:readonly WatchRadioChoice[];select(id:string):void|Promise<void>;reportError(error:unknown):void;label:string}){
 const buttons=useRef(new Map<string,HTMLButtonElement>()),selected=rows.find(row=>row.selected),first=rows.find(row=>row.enabled);
 const act=(row:WatchRadioChoice)=>{if(!row.enabled)return;try{void Promise.resolve(select(row.id)).catch(reportError);}catch(error){reportError(error);}};
 return <div role="radiogroup" aria-label={label}>{rows.map(row=><button key={row.id} type="button" role="radio"
  ref={node=>{if(node)buttons.current.set(row.id,node);else buttons.current.delete(row.id);}} aria-checked={row.selected} aria-disabled={!row.enabled||undefined}
  tabIndex={row.selected||!selected&&row.id===first?.id?0:-1} className={styles.row} data-selected={row.selected||undefined}
  onClick={()=>act(row)} onKeyDown={event=>{
   const direction=['ArrowDown','ArrowRight'].includes(event.key)?1:['ArrowUp','ArrowLeft'].includes(event.key)?-1:0;
   if(!direction)return;event.preventDefault();event.stopPropagation();const enabled=rows.filter(r=>r.enabled),at=enabled.findIndex(r=>r.id===row.id);
   const next=enabled[(at+direction+enabled.length)%enabled.length];if(next){buttons.current.get(next.id)?.focus();act(next);}
  }}><span>{row.label}{row.detail&&<small>{row.detail}</small>}</span><Icon name="i-check" size={20}/></button>)}</div>;
}
function RadioColumn({title,rows,select,reportError}:{title:string;rows:readonly WatchRadioChoice[];select(id:string):void|Promise<void>;reportError(error:unknown):void}){
 return <section className={styles.column} aria-label={title}><h3>{title}</h3>
  <RadioRows rows={rows} select={select} reportError={reportError} label={title}/></section>;
}
/** desktop-player f3: the selected track's own correction, a tenth of a second at a time. */
function TimingRow({timing,reportError,t}:{timing:NonNullable<WatchPanelModel['subtitleTiming']>;reportError(error:unknown):void;t:I18n['t']}){
 const shift=(delta:number)=>{try{void timing.shift(delta).catch(reportError);}catch(error){reportError(error);}};
 return <div className={styles.timing}><span>{t('player.opt.timing')}</span>
  <div className={styles.timingStep} role="group" aria-label={t('player.opt.timingHelp')}>
   <button type="button" aria-label={t('player.opt.earlier')} onClick={()=>shift(-100)}>−</button>
   <b>{t('player.opt.offsetSeconds',{seconds:timing.offsetSeconds.toFixed(1)})}</b>
   <button type="button" aria-label={t('player.opt.later')} onClick={()=>shift(100)}>+</button>
  </div></div>;
}

/** The drawn single options panel. Every visible radio row has actual source membership and
 * every action calls that same runtime with its captured full fence. No optimistic selection.
 * On a phone the drawn sheet lists one row per control; each row opens its own panel. */
export function WatchOptions({model,context,i18n,reportError,openSettings,info}:{openSettings():void|Promise<void>;model:WatchPanelModel;context:WatchPanelContext;i18n:I18n;reportError(error:unknown):void;/** Playback info f2: the phone reaches the same panel from its own options sheet. */info?:boolean}){
 const root=useRef<HTMLDivElement>(null),rowRefs=useRef(new Map<string,HTMLButtonElement>()),lastSection=useRef<Section|null>(null),t=i18n.t;
 const sleepRoot=useRef<HTMLDivElement>(null);
 const [sleepOpen,setSleepOpen]=useState(false),sleepAnchor=useRef<HTMLButtonElement|null>(null);
 // desktop-player f3b: a switch that failed keeps the playing track and offers Try again here.
 const [audioFailed,setAudioFailed]=useState<string|undefined>(undefined);
 useLayoutEffect(()=>{
  if(context.layout!=='phone')return;
  if(sleepOpen)sleepRoot.current?.querySelector<HTMLButtonElement>('[aria-checked="true"], button')?.focus({preventScroll:true});
 },[sleepOpen,context.layout]);
 const closeSleep=()=>{setSleepOpen(false);sleepAnchor.current?.focus({preventScroll:true});};
 const icons:Readonly<Record<Section,IconName>>=Object.freeze({subtitles:'i-cc',audio:'i-audio',quality:'i-quality',speed:'i-gauge'});
 const sections=phoneOptionRows(model,t).map(row=>({...row,icon:icons[row.id]}));
 const asked=sectionOf(context.panel),section=asked&&sections.some(row=>row.id===asked)?asked:null;
 useLayoutEffect(()=>{
  if(context.layout!=='phone')return;
  if(section){
   lastSection.current=section;
   const scope=root.current?.querySelector(`[data-section="${section}"]`)??root.current;
   scope?.querySelector<HTMLButtonElement>('[aria-checked="true"], [role="radio"][tabindex="0"], button')?.focus({preventScroll:true});
   return;
  }
  const previous=lastSection.current;lastSection.current=null;
  if(previous)rowRefs.current.get(previous)?.focus({preventScroll:true});
 },[section,context.layout]);
 useLayoutEffect(()=>{
  if(!context.returnFocusRef.current?.matches(':focus-visible'))return;
  const asked=sectionOf(context.panel);
  if(context.layout==='phone'&&!asked)return;
  const target=asked==='quality'?'quality':asked==='speed'?'speed':asked==='audio'?'audio':asked==='subtitles'?'subtitles':undefined;
  const scope=(target?root.current?.querySelector(`[data-section="${target}"]`):root.current)??root.current;
  scope?.querySelector<HTMLButtonElement>('[aria-checked="true"], [role="radio"][tabindex="0"]')?.focus({preventScroll:true});
 },[context.panel,context.returnFocusRef,context.layout]);
 useEffect(()=>()=>{const button=context.returnFocusRef.current;if(button?.isConnected)button.focus({preventScroll:true});},[context.returnFocusRef]);
 if(!model.isCurrent())return null;
 const sectionRows=(id:Section):readonly WatchRadioChoice[] => id==='subtitles'?model.subtitles.map(row=>({id:row.id,label:row.primary,detail:row.secondary,selected:row.selected,enabled:!row.unavailable}))
  :id==='audio'?model.audio.map(row=>({id:row.id,label:row.primary,detail:row.secondary,selected:row.selected,enabled:!row.unavailable}))
  :id==='quality'?model.quality:[];
 const chooseAudio=(id:string)=>{setAudioFailed(undefined);return Promise.resolve(model.selectAudio(id)).catch(()=>{if(model.isCurrent())setAudioFailed(id);});};
 const audioFailure=audioFailed?<div className={styles.audioRetry} role="status"><span>{t('player.opt.audioChangeFailed')}</span><button type="button" onClick={()=>chooseAudio(audioFailed)}>{t('action.tryAgain')}</button></div>:null;
 const sectionSelect=(id:Section,value:string)=>id==='subtitles'?model.selectSubtitle(value):id==='audio'?chooseAudio(value):id==='quality'?model.selectQuality(value):model.selectSpeed(value);
 const sectionTitle=(id:Section)=>id==='subtitles'?t('player.opt.subtitles'):id==='audio'?t('player.opt.audio'):id==='quality'?t('player.opt.quality'):t('player.opt.speed');
 const subPanel=section?<div ref={root} data-section={section} className={styles.subBody} onKeyDown={event=>{
  if(event.key==='Escape'){event.preventDefault();event.stopPropagation();context.show('options');}
 }}>{section==='speed'?<div className={styles.column}>
   <Segmented className={styles.speeds} aria-label={sectionTitle('speed')} value={model.speed.find(row=>row.selected)?.id??''}
    items={model.speed.map(row=>({value:row.id,label:row.label,disabled:!row.enabled}))}
    onValueChange={id=>{try{model.selectSpeed(id);}catch(error){reportError(error);}}}/>
   </div>:<><RadioRows rows={sectionRows(section)} select={id=>sectionSelect(section,id)} reportError={reportError} label={sectionTitle(section)}/>{section==='quality'&&model.qualityNotes.length>0&&<div className={styles.optionFoot}>{model.qualityNotes.map(note=><span key={note}>{note}</span>)}</div>}{section==='audio'&&<>{audioFailure}{model.audioNotes.length>0&&<div className={styles.optionFoot}>{model.audioNotes.map(note=><span key={note}>{note}</span>)}</div>}</>}</>}</div>:null;
 // desktop-player f7: the sheet's own "Up next" row carries the queue's real remaining count.
 const remaining=context.snapshot.queueView?.remainingCount,upNext=typeof remaining==='number'&&Number.isInteger(remaining)&&remaining>0?i18n.number(remaining):undefined;
 const titleActions:readonly Readonly<{id:string;label:string;icon:IconName;action:()=>void|Promise<void>;value?:string}>[]=[
  // A show keeps its authored Episodes drawer; any other queue opens the real queue list.
  ...(context.available?.includes('episodes')?[{id:'episodes',label:t('next.dock.queue'),icon:'i-queue' as const,...(upNext?{value:upNext}:{}),action:()=>context.show('episodes')}]
    :context.available?.includes('queue')?[{id:'queue',label:t('next.dock.queue'),icon:'i-queue' as const,...(upNext?{value:upNext}:{}),action:()=>context.show('queue')}]:[]),
  ...(info?[{id:'info',label:t('next.info.title'),icon:'i-info' as const,action:()=>context.show('info')}]:[]),
  ...(context.reportProblem?[{id:'report',label:context.reportProblem.label,icon:'i-flag' as const,action:context.reportProblem.action}]:[]),
 ];
 const list=<div ref={root} className={styles.phoneRows} onKeyDown={event=>{if(event.key==='Escape'){event.preventDefault();event.stopPropagation();context.close();}}}>
  <p className={styles.phoneNote}><Icon name="i-info" size={18}/><span>{t('player.opt.temporaryChanges')}{' '}{t('player.opt.defaultsUnchanged')}</span></p>
  {sections.map(row=><button key={row.id} type="button" className={styles.phoneRow}
   ref={node=>{if(node)rowRefs.current.set(row.id,node);else rowRefs.current.delete(row.id);}}
   onClick={()=>context.show(row.id)}>
   <span className={styles.phoneIcon}><Icon name={row.icon} size={18}/></span>
   <span className={styles.phoneLabel}>{row.label}</span>
   <span className={styles.phoneValue}>{row.value}{row.detail&&<small>{row.detail}</small>}</span>
   <Icon name="i-right" size={18}/>
  </button>)}
  {model.sleepTimer&&<button ref={sleepAnchor} type="button" className={styles.phoneRow} onClick={()=>{if(model.isCurrent())setSleepOpen(true);}}>
   <span className={styles.phoneIcon}><Icon name="i-clock" size={18}/></span>
   <span className={styles.phoneLabel}>{t('player.opt.sleepTimer')}</span>
   <span className={styles.phoneValue}>{model.sleepTimer.minutes?t('player.opt.minutes',{count:model.sleepTimer.minutes}):t('player.opt.off')}</span><Icon name="i-right" size={18}/>
  </button>}

  {titleActions.length>0&&<section aria-label={t('player.cannotPlay.thisTitle')}>
   <h3 className={styles.phoneGroup}>{t('player.cannotPlay.thisTitle')}</h3>
   {titleActions.map(action=><button key={action.id} type="button" className={styles.phoneRow} onClick={()=>{
    if(!model.isCurrent())return;
    try{if(action.id==='report')context.close();void Promise.resolve(action.action()).catch(reportError);}catch(error){reportError(error);}
   }}><span className={styles.phoneIcon}><Icon name={action.icon} size={18}/></span><span className={styles.phoneLabel}>{action.label}</span>{action.value&&<span className={styles.phoneValue}>{action.value}</span>}<Icon name="i-right" size={18}/></button>)}
  </section>}

 </div>;
 const columns=<><div ref={root} className={styles.columns} onKeyDown={event=>{
  if(event.key==='Escape'){event.preventDefault();event.stopPropagation();context.close();}
  else if(event.defaultPrevented&&['ArrowLeft','ArrowRight','ArrowUp','ArrowDown'].includes(event.key))event.stopPropagation();
 }}>
  {model.subtitles.length>0&&<div data-section="subtitles"><TrackChoices title={t('player.opt.subtitles')} choices={model.subtitles} layout={context.layout} onSelect={model.selectSubtitle} reportError={reportError} t={t}/>{model.subtitleTiming&&<TimingRow timing={model.subtitleTiming} reportError={reportError} t={t}/>}{model.subtitleAdditions.length>0&&<div className={styles.additions}><TrackChoices title={t('player.opt.notAdded')} choices={model.subtitleAdditions} layout={context.layout} onSelect={model.selectSubtitle} onAdd={id=>model.selectSubtitle(id)} reportError={reportError} t={t}/></div>}{model.subtitleForced&&<label className={styles.forcedRow}><span><b>{t('player.subtitles.forcedOnly')}</b><small>{t('player.subtitles.forcedOnlyHelp')}</small></span><Switch checked={model.subtitleForced.only} onCheckedChange={value=>{try{model.subtitleForced?.set(value);}catch(error){reportError(error);}}}/></label>}{model.subtitleSearch&&<div className={styles.searchRow} aria-disabled="true"><span className={styles.searchLabel}><Icon name="i-dl" size={18}/>{t('player.opt.findSubtitles')}</span><p className={styles.searchWhy}>{model.subtitleSearch.reason}</p></div>}</div>}
  {(model.audio.length>0||model.quality.length>0)&&<div>
   {model.audio.length>0&&<div data-section="audio"><TrackChoices title={t('player.opt.audio')} choices={model.audio} layout={context.layout} onSelect={chooseAudio} reportError={reportError} t={t}/>{audioFailure}{model.audioNotes.length>0&&<div className={styles.optionFoot}>{model.audioNotes.map(note=><span key={note}>{note}</span>)}</div>}</div>}
   {model.quality.length>0&&<div data-section="quality"><RadioColumn title={t('player.opt.quality')} rows={model.quality} select={model.selectQuality} reportError={reportError}/>{model.qualityNotes.length>0&&<div className={styles.optionFoot}>{model.qualityNotes.map(note=><span key={note}>{note}</span>)}</div>}</div>}
  </div>}
  {(model.speed.length>0||model.subtitleStyle||model.sleepTimer)&&<div>
   {model.speed.length>0&&<div data-section="speed" className={styles.column}>
   <h3>{t('player.opt.speed')}<span>{model.speed.find(row=>row.selected)?.label}</span></h3>
   <Segmented className={styles.speeds} aria-label={t('player.opt.speed')} value={model.speed.find(row=>row.selected)?.id??''}
    items={model.speed.map(row=>({value:row.id,label:row.label,disabled:!row.enabled}))}
    onValueChange={id=>{try{model.selectSpeed(id);}catch(error){reportError(error);}}}/>
  </div>}

  {model.subtitleStyle&&<div data-section="style" className={styles.column}>
   <h3>{t('player.opt.subtitleStyle')}</h3>
   <p className={styles.styleLabel}>{t('settings.row.subtitleSize')}</p>
   <Segmented className={styles.styleSegments} aria-label={t('settings.row.subtitleSize')} value={model.subtitleStyle.size}
    items={SUBTITLE_SIZES.map(value=>({value,label:t(SIZE_LABEL[value])}))}
    onValueChange={value=>{const next=SUBTITLE_SIZES.find(size=>size===value);if(next)model.subtitleStyle?.set({size:next});}}/>
   <p className={styles.styleLabel}>{t('settings.row.subtitleBackground')}</p>
   <Segmented className={styles.styleSegments} aria-label={t('settings.row.subtitleBackground')} value={model.subtitleStyle.background}
    items={SUBTITLE_BACKGROUNDS.map(value=>({value,label:t(BACKGROUND_LABEL[value])}))}
    onValueChange={value=>{const next=SUBTITLE_BACKGROUNDS.find(background=>background===value);if(next)model.subtitleStyle?.set({background:next});}}/>
  </div>}
  {model.sleepTimer&&<div className={styles.sleepRow}>
   <button ref={node=>{sleepAnchor.current=node;}} type="button" className={styles.sleepButton} aria-haspopup="menu" aria-expanded={sleepOpen}
    onClick={()=>setSleepOpen(value=>!value)}><span>{t('player.opt.sleepTimer')}</span>
    <span className={styles.sleepValue}>{model.sleepTimer.minutes?t('player.opt.minutes',{count:model.sleepTimer.minutes}):t('player.opt.off')}<Icon name="i-down" size={16}/></span></button>
   <Menu open={sleepOpen} onOpenChange={setSleepOpen} title={t('player.opt.sleepTimer')} anchorRef={sleepAnchor} returnFocusRef={sleepAnchor} presentation="anchored" width={220}
    items={[0,15,30,45,60,90].map(minutes=>({id:String(minutes),label:minutes?t('player.opt.minutes',{count:minutes}):t('player.opt.off'),selected:model.sleepTimer?.minutes===minutes,
     onSelect:()=>{try{model.sleepTimer?.set(minutes);}catch(error){reportError(error);}}}))}/>
  </div>}
 </div>}
 </div><div className={styles.note}><Icon name="i-info" size={20}/><span><b>{t('player.opt.temporaryChanges')}</b>{' '}{t('player.opt.defaultsUnchanged')}</span><a href="/settings" onClick={event=>{if(event.button!==0||event.ctrlKey||event.metaKey||event.shiftKey||event.altKey)return;event.preventDefault();try{context.close();void Promise.resolve(openSettings()).catch(reportError);}catch(error){reportError(error);}}}>{t('next.reader.openSettings')}<Icon name="i-right" size={16}/></a></div></>;
 const phoneSleep=sleepOpen&&model.sleepTimer?<div ref={sleepRoot} className={styles.subBody} onKeyDown={event=>{if(event.key==='Escape'){event.preventDefault();event.stopPropagation();closeSleep();}}}>
  <RadioRows label={t('player.opt.sleepTimer')} rows={[0,15,30,45,60,90].map(minutes=>({id:String(minutes),label:minutes?t('player.opt.minutes',{count:minutes}):t('player.opt.off'),selected:model.sleepTimer?.minutes===minutes,enabled:true}))}
   select={id=>{if(!model.isCurrent())return;model.sleepTimer?.set(Number(id));}} reportError={reportError}/>
 </div>:null;
 const phoneTitle=section||phoneSleep?<span className={styles.subHeader}><button type="button" className={styles.back} aria-label={t('next.dock.backToOptions')} onClick={()=>{if(phoneSleep)closeSleep();else context.show('options');}}><Icon name="i-left" size={20}/></button><span>{phoneSleep?t('player.opt.sleepTimer'):section?sectionTitle(section):''}</span></span>:t('next.dock.options');
 if(context.layout==='phone'&&context.landscape)return <Dialog open responsive={false} placement="right" width={356} className={styles.landscapeOptions} headerClassName={styles.landscapeHeader} title={phoneTitle}
  onOpenChange={open=>{if(!open)context.close();}} returnFocusRef={context.returnFocusRef}>{phoneSleep??(section?subPanel:list)}</Dialog>;
 return context.layout==='phone'?<BottomSheet open title={phoneTitle}
  headerAction={<button type="button" className={styles.done} onClick={context.close}>{t('action.done')}</button>} showCloseButton={false}
  onOpenChange={open=>{if(!open)context.close();}} returnFocusRef={context.returnFocusRef}
  detents={[Math.max(1,(typeof window==='undefined'?context.phoneTop:window.visualViewport?.height??window.innerHeight)-context.phoneTop)]}>{phoneSleep??(section?subPanel:list)}</BottomSheet>
  :<section className={styles.options} role="dialog" aria-label={t('next.dock.options')}>{columns}</section>;
}
