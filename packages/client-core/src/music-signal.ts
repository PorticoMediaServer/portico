import type {MessageId,MessageValues} from '../../i18n/src/index.ts';

/** Display models contain catalogue references and measured facts, never audio.reason. */
export type SignalKey=Extract<MessageId,`player.signal.${string}`>|'player.volume';
export type SignalText=Readonly<{key:SignalKey;values?:Readonly<Record<string,string|number|SignalText>>}>;
export type SignalTranslate=(key:MessageId,values?:MessageValues)=>string;
const text=(key:SignalKey,values?:SignalText['values']):SignalText=>Object.freeze({key,...(values?{values:Object.freeze(values)}:{})});
export function formatSignalText(value:SignalText,t:SignalTranslate):string{
 const values:Record<string,string|number>={};
 for(const [key,v] of Object.entries(value.values??{}))values[key]=typeof v==='object'?formatSignalText(v,t):v;
 return t(value.key,values);
}
export type MusicFormatFacts=Readonly<{
 codec?:string;lossless?:boolean;bitDepth?:number;sampleRate?:number;bitrateBps?:number;
 channels?:number;layout?:string;atmos?:boolean;dsdMultiple?:number;
}>;
export type MusicServerSignal=Readonly<{
 route:'original'|'repackaged'|'converted';delivered?:MusicFormatFacts;
 quality?:'original'|'cd-quality'|'320'|'96';dsdConverted?:boolean;atmosLost?:boolean;
 /** Baked gain is an actual server application fact; the renderer must not apply it again. */
 loudnessAppliedDb?:number;
 reason?:Readonly<{kind:'codec';codec:string;deviceName?:string}>|Readonly<{kind:'atmos'}>;
}>;
export type MusicOutputFacts=Readonly<{
 platform:'browser'|'android'|'apple'|'desktop'|'other';
 device:'browser'|'iphone'|'ipad'|'phone'|'tablet'|'appleTv'|'tv'|'computer'|'receiver'|'other';
 name?:string;usbDac?:boolean;sampleRate?:number;bitDepth?:number;decodedLayout?:string;
 resampledBy?:'browser'|'iphone'|'ipad'|'phone'|'tablet'|'appleTv'|'tv'|'airplay'|'system'|'other';
 system?:'Windows'|'macOS';atmosLost?:boolean;passthrough?:boolean;sampleRateMatched?:boolean;
 /** Engine-observed modes, not requested settings or capability advertisements. */
 bitPerfect?:boolean;bitPerfectMode?:'android14-usb'|'desktop-exclusive';exclusive?:boolean;volume?:number;
}>;
export type MusicSignalInput=Readonly<{
 source:MusicFormatFacts;server?:MusicServerSignal;output:MusicOutputFacts;
 eqOn:boolean;eqApplied:boolean;normalization:'off'|'track'|'album';levelingApplied:boolean;
 /** The engine fell back for this song, so the line says what was lost, not a notice. */
 fallback?:'eq';
 /** Actual gain used by this song's renderer, after Never boost/peak limits. */
 levelingGainDb?:number;
}>;
export type SignalStep=Readonly<{
 id:'file'|'server'|'leveling'|'eq'|'output'|'volume';label:SignalText|string;
 value?:SignalText|string;note?:SignalText;percent?:number;changed:boolean;
}>;
export type MusicSignalModel=Readonly<{
 /** Long form for desktop/accessibility; short form for phone/TV visual lines. */
 source?:SignalText|string;shortSource?:SignalText|string;state?:SignalText;stateLoss?:SignalText;foldSourceIntoState:boolean;
 rule:1|2|3|4|5|6|'desktop-volume';healthy:boolean;
 pill:SignalText;steps:readonly SignalStep[];
}>;
const positive=(n:unknown):n is number=>typeof n==='number'&&Number.isFinite(n)&&n>0;
const gainKnown=(n:unknown):n is number=>typeof n==='number'&&Number.isFinite(n)&&n>=-60&&n<=30;
const changedGain=(n:unknown):n is number=>gainKnown(n)&&Math.abs(n)>=.05;
const rate=(hz:number)=>text('player.signal.rate',{khz:hz/1000});
/** One decimal and a true minus; rounded zero is never an audible gain-change state. */
export function signalGain(db:number):string{
 if(!gainKnown(db))throw new Error('Invalid measured gain.');
 const n=Math.round(Math.abs(db)*10)/10;
 return `${db<0&&n!==0?'−':'+'}${n.toFixed(1)}`;
}
const dsdCodec=(codec:string|undefined):boolean=>codec!==undefined&&(codec.toLowerCase()==='dsd'||codec.toLowerCase().startsWith('dsd_'));
function codecName(codec:string):string{
 const name=codec.toLowerCase();
 // The probe reports a family member for these two: pcm_s24le, dsd_lsbf and so on. The
 // drawn names are the family, never the raw probe token.
 if(name==='pcm'||name.startsWith('pcm_'))return 'PCM';
 if(name==='dsd'||name.startsWith('dsd_'))return 'DSD';
 return ({flac:'FLAC',alac:'ALAC',aac:'AAC',mp3:'MP3',opus:'Opus',vorbis:'Vorbis',truehd:'Dolby TrueHD',eac3:'Dolby Digital Plus',ac3:'Dolby Digital',dts:'DTS',pcm:'PCM',wavpack:'WavPack'} as Record<string,string>)[name]??codec.toUpperCase();
}
/** An explicit measured layout, or the one-channel/two-channel names a count does prove; a
 * larger count alone cannot tell 5.1 from 6.0, 7.1 from 8.0, so it names no layout. */
function layout(f:MusicFormatFacts):string|undefined{
 // These measured variants name a speaker family the drawn badge/wording already know.
 // Every other layout label stays exactly as measured.
 const measured=f.layout?.trim(),known=measured?.toLowerCase();
 if(measured)return known==='5.1(side)'?'5.1':known==='7.1(wide)'||known==='7.1(wide-side)'?'7.1':measured;
 return ({1:'mono',2:'stereo'} as Record<number,string>)[f.channels??0];
}
export function musicSignalSource(f:MusicFormatFacts,short=false):SignalText|string|undefined{
 if(f.atmos===true)return text(short?'player.signal.badge.atmos':'player.signal.source.atmos');
 if(positive(f.dsdMultiple))return text('player.signal.source.dsd',{multiple:f.dsdMultiple});
 if(!f.codec)return undefined;
 const codec=codecName(f.codec);
 if(f.lossless===true&&positive(f.bitDepth)&&positive(f.sampleRate))return short?text('player.signal.badge.lossless',{codec,bits:f.bitDepth,khz:f.sampleRate/1000}):text('player.signal.source.lossless',{codec,bits:f.bitDepth,rate:rate(f.sampleRate)});
 if(f.lossless===false&&positive(f.bitrateBps))return text(short?'player.signal.badge.lossy':'player.signal.source.lossy',{codec,kbps:Math.round(f.bitrateBps/1000)});
 return codec;
}
/** Only notable formats. Callers omit these entirely on an album's own track list/cards. */
export function musicFormatBadges(f:MusicFormatFacts):readonly (SignalText|string)[]{
 const out:(SignalText|string)[]=[];
 if(f.atmos===true)out.push(text('player.signal.badge.atmos'));
 else if(positive(f.dsdMultiple)||dsdCodec(f.codec))out.push(text('player.signal.badge.dsd'));
 else if(f.lossless===true&&positive(f.bitDepth)&&f.bitDepth>16&&positive(f.sampleRate)&&f.codec)out.push(text('player.signal.badge.lossless',{codec:codecName(f.codec),bits:f.bitDepth,khz:f.sampleRate/1000}));
 const channels=layout(f);if(channels==='5.1'||channels==='7.1')out.push(channels);
 return Object.freeze(out.slice(0,2));
}
/** Album aggregation is deterministic and bounded; the server should publish this once. */
export function albumMusicFormatBadges(formats:readonly MusicFormatFacts[]):readonly (SignalText|string)[]{
 const result:(SignalText|string)[]=[],seen=new Set<string>();
 for(const f of formats)for(const b of musicFormatBadges(f)){const id=JSON.stringify(b);if(!seen.has(id)){seen.add(id);result.push(b);if(result.length===2)return Object.freeze(result);}}
 return Object.freeze(result);
}
export function musicSignal(input:MusicSignalInput):MusicSignalModel{
 const {source,server,output}=input,delivered=server?.delivered;
 const gain=server?.loudnessAppliedDb??(input.levelingApplied?input.levelingGainDb:undefined);
 const gainChanged=changedGain(gain),converted=server?.route==='converted';
 const normalizationAltered=input.levelingApplied||server?.loudnessAppliedDb!==undefined&&server.loudnessAppliedDb!==0;
 const atmosLost=server?.atmosLost===true||output.atmosLost===true;
 const volumeChanged=output.platform==='desktop'&&output.exclusive===true&&typeof output.volume==='number'&&output.volume>=0&&output.volume<1;
 const untouched=server!==undefined&&!converted&&!positive(source.dsdMultiple)&&!input.eqApplied&&!normalizationAltered&&!volumeChanged&&output.resampledBy===undefined&&!atmosLost;
 const bitPerfect=untouched&&source.lossless===true&&positive(source.sampleRate)&&source.sampleRate===output.sampleRate&&positive(source.bitDepth)&&source.bitDepth===output.bitDepth&&output.bitPerfect===true&&(
  output.platform==='android'&&output.bitPerfectMode==='android14-usb'&&output.usbDac===true||
  output.platform==='desktop'&&output.bitPerfectMode==='desktop-exclusive'&&output.exclusive===true);
 const matched=untouched&&output.platform==='apple'&&output.sampleRateMatched===true&&positive(output.sampleRate)&&positive(source.sampleRate)&&output.sampleRate===source.sampleRate;
 const passthrough=output.passthrough===true&&server!==undefined&&!converted&&!input.eqApplied&&!normalizationAltered&&!atmosLost;
 const pill=text(atmosLost?'player.signal.pill.atmosLost':bitPerfect?'player.signal.pill.bitPerfect':matched?'player.signal.pill.rateMatched':'player.signal.pill.notBitPerfect');
 let state:SignalText|undefined,stateLoss:SignalText|undefined,rule:MusicSignalModel['rule']=6,foldSourceIntoState=false;
 if(converted){
  rule=1;
  if(server.dsdConverted&&positive(source.dsdMultiple)&&positive(delivered?.sampleRate)){
   state=text('player.signal.line.dsdToPcm',{source:text('player.signal.source.dsd',{multiple:source.dsdMultiple}),rate:rate(delivered.sampleRate)});foldSourceIntoState=true;
  }else if(server.atmosLost&&layout(delivered??{}))state=text('player.signal.line.atmosLost',{layout:layout(delivered??{})!});
  else if(server.quality==='cd-quality')state=text('player.signal.line.convertedCd');
  else if((server.quality==='320'||server.quality==='96')&&positive(delivered?.bitrateBps))state=text('player.signal.line.convertedBitrate',{kbps:Math.round(delivered.bitrateBps/1000)});
  else if(delivered?.codec)state=text('player.signal.server.converted',{format:codecName(delivered.codec)});
 }else if(input.eqOn||input.eqApplied){rule=2;state=text(input.eqApplied?'player.signal.line.eqOn':'player.signal.line.eqUnavailable');}
 else if(input.fallback==='eq'){rule=2;state=text('player.signal.line.eqUnavailable');}
 else if(gainChanged){rule=3;state=text('player.signal.line.loudness',{gain:signalGain(gain)});}
 else if(volumeChanged){rule='desktop-volume';state=text('player.signal.line.volume',{level:output.volume!});}
 else if(output.resampledBy&&positive(output.sampleRate)){
  rule=4;state=text('player.signal.line.resampled',{by:output.resampledBy,rate:rate(output.sampleRate),...(output.system?{system:output.system}:{})});
 }else if(output.decodedLayout&&output.atmosLost){rule=4;state=text('player.signal.output.decoded',{layout:output.decodedLayout});stateLoss=text('player.signal.pill.atmosLost');}
 else if(bitPerfect){rule=5;state=text('player.signal.line.bitPerfect',{output:output.name??text('player.signal.output.usbDac')});}
 else if(matched){rule=5;state=text('player.signal.rateMatched',{rate:rate(output.sampleRate!)});}
 else if(passthrough){rule=5;state=text('player.signal.line.passthrough');}
 const steps:SignalStep[]=[];
 const channels=layout(source);
 const file=source.atmos&&source.codec&&channels?text('player.signal.file.withAtmos',{codec:codecName(source.codec),layout:channels}):source.lossless===true&&source.codec&&positive(source.bitDepth)&&positive(source.sampleRate)&&channels?text('player.signal.file.lossless',{codec:codecName(source.codec),bits:source.bitDepth,rate:rate(source.sampleRate),channels:text('player.signal.channels',{layout:channels})}):musicSignalSource(source);
 steps.push({id:'file',label:text('player.signal.step.file'),value:file,changed:false,...(source.lossless===true?{note:text('player.signal.file.losslessNote')}:{})});
 let serverNote:SignalText|undefined;
 if(server?.reason?.kind==='atmos')serverNote=text('player.signal.server.atmosUnavailable',{device:output.platform==='browser'?'browser':'other'});
 else if(server?.reason?.kind==='codec'&&(output.platform==='browser'||server.reason.deviceName))serverNote=text('player.signal.server.cantPlay',{device:output.platform==='browser'?'browser':'other',codec:codecName(server.reason.codec),name:server.reason.deviceName??''});
 let serverValue:SignalText|undefined;
 if(converted&&delivered?.codec)serverValue=text('player.signal.server.converted',{format:[codecName(delivered.codec),layout(delivered)].filter(Boolean).join(' ')});
 else if(server)serverValue=text('player.signal.server.asIs');
 steps.push({id:'server',label:text('player.signal.step.server'),value:serverValue,note:serverNote,changed:converted});
 const leveling=server?.loudnessAppliedDb!==undefined||input.levelingApplied;
 // An accepted plan can carry measured baked gain while the current preference has moved to
 // off; the pinned gain stays audible and the unknown mode must not be guessed.
 const baked=server?.loudnessAppliedDb;
 steps.push({id:'leveling',label:text('player.signal.step.leveling'),value:passthrough?text('player.signal.notApplied'):baked!==undefined&&baked!==0&&gainKnown(baked)&&input.normalization==='off'?text('player.signal.line.loudness',{gain:signalGain(baked)}):input.normalization==='off'?text('player.signal.off'):leveling&&gainKnown(gain)?text('player.signal.leveling.applied',{mode:input.normalization,gain:signalGain(gain)}):text('player.signal.notApplied'),changed:!passthrough&&gainChanged});
 steps.push({id:'eq',label:text('player.signal.step.eq'),value:passthrough?text('player.signal.notApplied'):input.eqApplied?text('player.signal.eq.on'):!input.eqOn?text('player.signal.off'):text('player.signal.notApplied'),changed:!passthrough&&input.eqApplied});
 if(output.platform==='desktop'&&output.exclusive===true)steps.push({id:'volume',label:text('player.volume'),...(typeof output.volume==='number'&&output.volume>=0&&output.volume<=1?{percent:output.volume}:{}),changed:volumeChanged});
 let outputValue:SignalText|undefined;
 if(passthrough)outputValue=text('player.signal.line.passthrough');
 else if(output.resampledBy&&positive(output.sampleRate))outputValue=text('player.signal.output.resampled',{rate:rate(output.sampleRate)});
 else if(output.decodedLayout)outputValue=text('player.signal.output.decoded',{layout:output.decodedLayout});
 else if(bitPerfect&&positive(output.bitDepth)&&positive(output.sampleRate))outputValue=text('player.signal.output.bitPerfect',{bits:output.bitDepth,rate:rate(output.sampleRate)});
 else if(matched)outputValue=text('player.signal.rateMatched',{rate:rate(output.sampleRate!)});
 steps.push({id:'output',label:output.name??(output.usbDac?text('player.signal.output.usbDac'):text('player.signal.step.output',{output:output.device})),value:outputValue,changed:output.resampledBy!==undefined||output.atmosLost===true,...(matched?{note:text('player.signal.output.switched')}:output.platform==='browser'?{note:text('player.signal.output.browserNote')}:{})});
 return Object.freeze({source:foldSourceIntoState?undefined:musicSignalSource(source),shortSource:foldSourceIntoState?undefined:musicSignalSource(source,true),state,stateLoss,foldSourceIntoState,rule,healthy:rule===5&&(bitPerfect||passthrough),pill,steps:Object.freeze(steps.map(s=>Object.freeze(s)))});
}
export function musicSignalAccessibleName(model:MusicSignalModel,t:SignalTranslate):string|undefined{
 const source=typeof model.source==='string'?model.source:model.source?formatSignalText(model.source,t):undefined;
 const state=model.state?[formatSignalText(model.state,t),...(model.stateLoss?[formatSignalText(model.stateLoss,t)]:[])].join(', '):undefined;
 // A folded DSD state already includes the source once.
 if(!source)return state?t('player.signal.a11ySourceOnly',{source:state}):undefined;
 return state?t('player.signal.a11y',{source,state}):t('player.signal.a11ySourceOnly',{source});
}
