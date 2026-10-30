import type {QueuePlaybackFence, ChannelPlaybackFence, PlaybackIdentity, QueueScope, QueuePostPlay, PolicyFrame, DockPlayback, WatchTogetherState, WatchTogetherFence} from '../src/index.ts';
export const scope:QueueScope={serverId:'server',authority:'local',accountId:'account',profileId:'profile',controllerId:'controller',controllerEpoch:'epoch',commandLaneId:'lane'};
export const identity:PlaybackIdentity={scope,run:1};
export const fence:QueuePlaybackFence={kind:'queue',playback:identity,sessionId:'session',itemId:'item',sourceId:'source',entryId:'first',generation:1};
export const channelFence:ChannelPlaybackFence={kind:'channel',playback:identity,sessionId:'channel-session',channelKind:'live-source',sourceId:'source',channelId:'channel',channelGeneration:'g1',generation:1};
export const intro={id:'intro',kind:'intro' as const,startSeconds:30,endSeconds:80,automaticSafe:true};
export const credits={id:'credits',kind:'credits' as const,startSeconds:1300,endSeconds:1400,automaticSafe:true};
export const post:QueuePostPlay={nextEntryId:'second',available:true,reason:'ready',countdownSeconds:10,autoplay:true,passoutCheckDue:false,automaticAdvances:0,startDuringCredits:false};
export const frame=(over:Partial<PolicyFrame>={}):PolicyFrame=>({fence,nowMs:0,positionSeconds:40,durationSeconds:1400,rate:1,playing:true,expanded:true,ended:false,kind:'episode',modes:{intro:'ask',credits:'ask'},postPlay:post,...over});
export const entry=(id:string)=>({id,hidden:false,removed:false,itemId:'item',editionId:null,partId:null,sourceContext:{kind:'item',id:'item',revision:null,entryId:null}});
export const queue=()=>({id:'queue',scope,revision:'1',highestSeenSequence:'0',replayWindow:{results:256,bodies:32},repeat:'off',shuffled:false,currentEntryId:'first',currentPlaybackId:'session',entries:[entry('first'),entry('second')]});
export const view=()=>({queue:queue(),items:['first','second'].map(entryId=>({entryId,itemId:'item',title:'Episode',kind:'episode',durationSeconds:1400,showId:'show',seasonId:'season'})),next:{entryId:'second',available:true,reason:'ready'},postPlay:{nextEntryId:'second',available:true,reason:'ready',countdownMs:10000,autoplay:true,passoutCheckDue:false,automaticAdvances:0,startDuringCredits:false}});
type LiveDock=Extract<DockPlayback,{kind:'live'}>;
type CatalogueDock=Exclude<DockPlayback,LiveDock>;
export function dock(kind:'live',over?:Partial<LiveDock>):LiveDock;
export function dock(kind?:CatalogueDock['kind'],over?:Partial<CatalogueDock>):CatalogueDock;
export function dock(kind:DockPlayback['kind']='video',over:Partial<DockPlayback>={}):DockPlayback {
 const common={positionSeconds:107,playing:true,expanded:false};
 return kind==='live'?{...common,fence:channelFence,kind,queue:null,...over as Partial<LiveDock>}:{...common,fence,kind,queue:null,...over as Partial<CatalogueDock>};
}
export const room:WatchTogetherFence={scope,roomId:'group',epoch:1};
export const T0=Date.parse('2026-09-16T20:00:00Z');
export const group=(over:Partial<WatchTogetherState>={}):WatchTogetherState=>({id:'group',revision:'7',playbackRevision:'3',eventOrdinal:'9',phase:'live',hostPresence:'connected',isHost:false,canControl:false,waiting:false,timeline:{itemId:'item',currentEntryId:'first',state:'playing',anchorPositionUs:'10000000',anchorAt:new Date(T0).toISOString(),rate:{numerator:'1',denominator:'1'},queuePosition:0,waitUntil:null},sync:{noCorrectionUnderMs:750,rateCorrectionMinimum:'0.90',rateCorrectionMaximum:'1.10',rateCorrectionMaxMs:4000,seekAtOrOverMs:3000},...over});
