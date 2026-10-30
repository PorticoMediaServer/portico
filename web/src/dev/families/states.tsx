import {useEffect, useMemo, useRef, useState, type ComponentType, type ReactNode} from 'react';
import {defaultI18n, formatMessage, type MessageId} from '@i18n';
import {statesFixtureMessages} from '../../../../packages/i18n/src/catalog/en-US/next/dev/states-fixtures';
import {ArtworkScheduler} from '@core/artwork';
import {cardText} from '@portico/client-core/kit-model';
import {defineGalleryFamily, type GalleryCase} from '../gallery-registry';
import {Artwork, type AmbientArtSource, type ArtworkImage, type ArtworkSource} from '../../kit/artwork';
import {Icon} from '../../kit/Icon';
import {Button, Tabs} from '../../kit/controls';
import {PosterCard, WideCard} from '../../kit/cards';
import {OwnerTag, ServerMark} from '../../kit/shell';
import {CountdownRing, DetailsPanel, GridSkeleton, HomeSkeleton, KeyboardShortcuts, StateLayout, StateSentence, StateSteps, StateView, TitleSkeleton, type DetailsFact, type SkeletonArtwork, type StateAction, type StateContentProps} from '../../kit/states';
import styles from '../../kit/states/gallery.module.css';

const t = defaultI18n.t;
const fixture = (key: keyof typeof statesFixtureMessages) => formatMessage(statesFixtureMessages[key], {}, {locale:'en-US'});
const referenceFacts = {
  serverName: fixture('next.states.fixture.server'), libraryName: fixture('next.states.fixture.library'), showName: fixture('next.states.fixture.show'), phoneShow: fixture('next.states.fixture.phoneShow'),
  removedName: fixture('next.states.fixture.removed'), filmName: fixture('next.states.fixture.film'), profileName: fixture('next.states.fixture.profile'), searchTerm: fixture('next.states.fixture.query'), programmeName: fixture('next.states.fixture.programme'),
  movieName: fixture('next.states.fixture.movie'), episodeName: fixture('next.states.fixture.episode'), firstMovieName: fixture('next.states.fixture.firstMovie'), secondMovieName: fixture('next.states.fixture.secondMovie'),
  deviceNames: [fixture('next.states.fixture.deviceTV'), fixture('next.states.fixture.devicePhone')], streamNames: [fixture('next.states.fixture.streamTV'), fixture('next.states.fixture.streamPhone')],
  crashWhen: fixture('next.states.fixture.crashWhen'), crashApp: fixture('next.states.fixture.crashApp'), crashBrowser: fixture('next.states.fixture.crashBrowser'), crashWhere: fixture('next.states.fixture.crashWhere'),
} as const;
const server = () => referenceFacts.serverName;
/** The source draws a static partial-opacity arrival. This is not a decode or timing claim. */
function ReferenceArrival({children}: {children: ReactNode}) { return <div className={styles.referenceArrival} data-state-reference-arrival="partial">{children}</div>; }

/** Root supplies real authority-bound handles privately for ambient evidence. */
type StatesArtworkEvidence = Readonly<{
  home?: SkeletonArtwork;
  title?: Readonly<{foreground: ArtworkSource; ambient?: AmbientArtSource; palette?: SkeletonArtwork['palette']}> & Pick<SkeletonArtwork, 'visible' | 'onReady' | 'onError'>;
}>;
declare global { var __porticoWebStatesEvidence: StatesArtworkEvidence | undefined; }
function useStatesArtworkEvidence() {
  const [evidence, setEvidence] = useState(globalThis.__porticoWebStatesEvidence);
  useEffect(() => {
    const update = () => setEvidence(globalThis.__porticoWebStatesEvidence);
    window.addEventListener('portico-states-evidence', update);
    return () => window.removeEventListener('portico-states-evidence', update);
  }, []);
  return evidence;
}

/** Reference assets and display facts are confined to this serve-only fixture module. */
function useReferenceArtwork() {
  const scheduler = useMemo(() => new ArtworkScheduler<ArtworkImage>({load: (key,signal) => new Promise((resolve,reject) => {
    const picture = new Image();
    const clear = () => { signal.removeEventListener('abort',abort); picture.onload=null; picture.onerror=null; };
    const abort = () => { clear(); picture.src=''; reject(new DOMException('Aborted','AbortError')); };
    signal.addEventListener('abort',abort,{once:true});
    picture.onload=() => { void picture.decode().then(() => { clear(); if(signal.aborted) reject(new DOMException('Aborted','AbortError')); else resolve({src:picture.src,width:picture.naturalWidth,height:picture.naturalHeight}); },error => { clear(); reject(error); }); };
    picture.onerror=() => { clear(); reject(new Error('Reference artwork failed to decode.')); };
    if(signal.aborted) abort(); else picture.src=new URL(key,'http://100.120.50.24:8787/').href;
  }), classify: () => ({retryable: false}), concurrency: 1, priorityExtra: 0, backoffMs: []}), []);
  useEffect(() => () => scheduler.dispose(), [scheduler]);
  return (key: string): ArtworkSource => ({scheduler, key});
}

function useActions() {
  const [events, setEvents] = useState<readonly string[]>([]);
  const action = (id: string, label: ReactNode, icon?: StateAction['icon']): StateAction => ({id, label, icon, onPress: () => setEvents(value => [...value, id])});
  const output = events.length > 0 ? <output className={styles.event} data-last-action={events.at(-1)} data-action-count={events.length}>{fixture('next.states.fixture.actionReceived')}</output> : null;
  return {action, output};
}

function connectionFacts(phone = false): readonly DetailsFact[] {
  return [
    {id: 'home', label: t('next.states.homeAddress'), value: phone ? undefined : '192.168.1.20', status: {label: t('next.states.noAnswer'), tone: 'warning'}},
    {id: 'secure', label: t('next.states.secureAddress'), value: phone ? undefined : 'demo.getportico.tv', status: {label: t('next.states.noAnswer'), tone: 'warning'}},
    {id: 'device', label: t(phone ? 'next.states.thisPhone' : 'next.states.thisComputer'), value: phone ? undefined : t('next.states.online'), status: {label: t(phone ? 'next.states.online' : 'next.states.connected'), tone: 'healthy'}},
  ];
}
function Connection({owner = true, phone = false, unknown = false}: {owner?: boolean; phone?: boolean; unknown?: boolean}) {
  const [open, setOpen] = useState(true);
  const {action, output} = useActions();
  const details = owner ? <DetailsPanel className={styles.connectionDetails} audience="owner" facts={connectionFacts(phone)} owner={{ownerMark:<ServerMark size={26}/>,ownerTag:<OwnerTag/>,paragraphs: [t('next.states.connectionOwnerBody', {time: fixture('next.states.fixture.connectionTime')})], codes: ['route_unavailable', 'request_failed'], actions: [action('copy', t('next.states.copyDetails'), 'i-copy')]}}/> : <DetailsPanel className={styles.connectionDetails} audience="viewer" facts={connectionFacts(phone)}/>;
  return <StateLayout className={phone ? styles.mobileConnection : undefined} title={t('next.states.cantReach', {server: server()})}
    backdrop={phone ? null : undefined}
    body={t(phone ? 'next.states.cantReachPhoneBody' : 'next.states.cantReachBody')} icon="i-srvoff" tone="warning"
    contentStyle={phone ? {top: 62, left: 20, right: 20} : {top: 500}}
    retry={{progress: unknown ? null : phone ? 1 - 12 / 56.55 : 1 - 22 / 56.55, label: unknown ? t('next.states.retrying') : t('next.states.retryIn', {seconds: phone ? 26 : 9}), detail: phone ? undefined : t('next.states.triesSince', {count: 4, time: fixture('next.states.fixture.connectionTime')})}}
    primary={action('retry', t('next.states.tryNow'), phone ? undefined : 'i-retry')} secondary={action('switch', t('next.states.switchServer'), phone ? undefined : 'i-server')}
    details={phone ? undefined : {open, onOpenChange: setOpen, content: details}} afterActions={phone ? details : undefined} after={output}/>;
}

function PageFailed() {
  const {action, output} = useActions();
  return <StateLayout title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')} icon="i-warn" tone="warning" contentStyle={{top: 470}}
    primary={action('retry', t('next.states.tryAgain'), 'i-retry')} secondary={action('home', t('next.states.goHome'))} after={output}/>;
}
function Updating() {
  const {action, output} = useActions();
  return <StateLayout title={t('next.states.updating', {server: server()})} body={t('next.states.updatingBody', {version: '1.4.0'})} icon="i-update" tone="accent" contentStyle={{top: 470}}
    secondary={action('switch', t('next.states.switchServer'), 'i-server')} links={[{...action('changes', t('next.states.whatsNew', {version: '1.4.0'}), 'i-right'),iconPosition:'end'}]} after={output}>
    <StateSteps steps={[{id: 'backup', label: t('next.states.backedUp'), status: 'done', detail: fixture('next.states.fixture.backupTime')}, {id: 'database', label: t('next.states.updatingDatabase'), status: 'current', detail: t('next.states.secondsLeft', {seconds: 30}), progress: 1 - 24 / 56.55}, {id: 'starting', label: t('next.states.starting'), status: 'pending'}]}/>
  </StateLayout>;
}
function PlayerFailure() {
  const reference = useReferenceArtwork();
  const {action, output} = useActions();
  return <StateLayout variant="player" backdrop={{kind: 'frozen', source: reference('assets/pool/movies/dune-part-two--bd.jpg')}}
    title={t('next.states.playbackStopped')} body={<StateSentence messageId="next.states.versionFailureBody" values={{failedVersion: '4K HDR10', position: '1:12:40', alternative: '1080p'}}/>} icon="i-warn" tone="warning" contentStyle={{top: 418}}
    primary={action('version', t('next.states.playVersion', {version: '1080p'}), 'i-play')} secondary={action('retry', t('next.states.tryAgain'), 'i-retry')}
    links={[action('report', t('next.states.reportProblem'), 'i-flag')]} afterActions={<DetailsPanel audience="owner" facts={[]} owner={{ownerMark:<ServerMark size={26}/>,ownerTag:<OwnerTag/>,paragraphs: [t('next.states.playbackOwnerBody',{browser:'Chrome on this Mac',version:'4K HDR10',format:'HEVC Main 10',resolution:'2160p',position:'1:12:40'})], codes: ['decoder_contradiction'], time: '4:05 PM', actions: [action('info', t('next.states.playbackInfo'), 'i-pulse'), action('copy', t('next.states.copyDetails'), 'i-copy')]}}/>} after={output}/>;
}
function CompactFailure({kind}: {kind: 'permanent' | 'busy' | 'limit'}) {
  const reference = useReferenceArtwork();
  const {action, output} = useActions();
  const streams = [0,1];
  return <div className={styles.compactStage}>
    <Artwork className={styles.frozen} source={reference(kind === 'permanent' ? 'assets/pool/movies/spirited-away--bd.jpg' : kind === 'busy' ? 'assets/pool/tv/the-last-of-us--bd.jpg' : 'assets/pool/tv/severance--bd.jpg')} alt=""/>
    <StateView className={styles.compactContent} variant="compact" icon={kind === 'permanent' ? 'i-info' : kind === 'busy' ? 'i-clock' : 'i-monitor'} tone={kind === 'permanent' ? 'neutral' : 'warning'}
      title={t(kind === 'permanent' ? 'next.states.cannotPlay' : kind === 'busy' ? 'next.states.convertingBusy' : 'next.states.streamLimit', {count: 2})}
      body={kind === 'permanent' ? <StateSentence messageId="next.states.convertingOffBody" values={{title:referenceFacts.filmName}}/> : kind === 'busy' ? t('next.states.convertingBusyBody') : t('next.states.streamLimitBody', {count: 2})}
      retry={kind === 'busy' ? {progress: 1 - 20 / 56.55, label: t('next.states.retryIn', {seconds: 20})} : undefined}
      primary={kind === 'permanent' ? action('back', t('next.states.back')) : kind === 'busy' ? action('retry', t('next.states.tryNow')) : undefined}
      secondary={kind === 'permanent' ? action('report', t('next.states.reportProblem'), 'i-flag') : kind === 'busy' ? action('back', t('next.states.back')) : undefined}>
      {kind === 'limit' && <div className={styles.sessions}>{streams.map(index => <div key={index}>
        <Icon name={index === 0 ? 'i-tv' : 'i-phone'} size={18}/><span><b>{referenceFacts.deviceNames[index]}</b><small>{referenceFacts.streamNames[index]}</small></span>
        <Button variant="glass" size="xs" onClick={action(`stop-${index}`, t('next.states.stop')).onPress}>{t('next.states.stop')}</Button>
      </div>)}</div>}
    </StateView>{output}
  </div>;
}
function LandscapeFailure({details = false}: {details?: boolean}) {
  const reference = useReferenceArtwork();
  const {action, output} = useActions();
  const [open, setOpen] = useState(details);
  return <StateLayout variant="landscape" backdrop={{kind: 'frozen', source: reference('assets/pool/tv/slow-horses--bd.jpg')}}
    title={t('next.states.playbackStopped')} body={<StateSentence messageId="next.states.droppedBody" values={{position:'12:04'}}/>} icon="i-wifioff" tone="warning"
    contentStyle={{width: open ? 330 : 380, transform: open ? 'translateY(-46%)' : 'translateY(-62%)'}}
    primary={action('retry', t('next.states.tryAgain'), 'i-retry')} secondary={action('close', t('next.states.close'))}
    details={{open, onOpenChange: setOpen, content: <DetailsPanel compact audience="owner" facts={[{id:'what',label:t('next.states.whatHappened'),value:t('next.states.noDataSeconds',{seconds:20})},{id:'sent',label:t('next.states.sentAs'),value:t('next.states.convertedTo',{quality:'1080p'})},{id:'network',label:t('next.states.network'),value:t('next.states.awayFromHome')}]} owner={{ownerMark:<ServerMark size={22}/>,ownerTag:<OwnerTag/>,paragraphs:[t('next.states.droppedOwnerBody',{rate:'4 Mb/s'})],codes:['lease_expired'],time:'4:05 PM',actions:[action('copy',t('next.states.copyDetails'),'i-copy')]}}/>}} after={output}/>;
}

type EmptyKind = 'not-found' | 'removed' | 'restricted' | 'owner' | 'first-library' | 'member' | 'my-list' | 'recordings' | 'live';
function Empty({kind}: {kind: EmptyKind}) {
  const {action, output} = useActions();
  const content: Record<EmptyKind, StateContentProps> = {
    'not-found': {icon:'i-unlink', title:t('next.states.notFound'), body:t('next.states.notFoundBody'), primary:action('home',t('next.states.goHome'),'i-home'), secondary:action('search',t('next.states.shortcut.search'),'i-search')},
    removed:{icon:'i-not',title:t('next.states.removed',{title:referenceFacts.removedName,server:server()}),body:t('next.states.removedBody'),primary:action('search',t('next.states.searchTitle',{title:referenceFacts.removedName}),'i-search'),secondary:action('remove',t('next.states.removeContinue'))},
    restricted:{icon:'i-lock',title:t('next.states.restricted',{profile:referenceFacts.profileName}),body:t('next.states.restrictedBody'),primary:action('home',t('next.states.goHome')),secondary:action('profile',t('next.states.switchProfile'),'i-switch')},
    owner:{eyebrow:<OwnerTag variant="state" identity={t('next.shell.serverSettings')}/>,icon:'i-lock',title:t('next.states.ownerRequired'),body:t('next.states.ownerRequiredBody',{server:server()}),primary:action('settings',t('next.states.mySettings')),secondary:action('home',t('next.states.goHome'))},
    'first-library':{eyebrow:<><span className={styles.readyDot}/>{t('next.states.ready',{server:server()})}</>,title:t('next.states.firstLibrary'),body:t('next.states.firstLibraryBody'),primary:action('add',t('next.states.addLibrary'),'i-plus'),secondary:action('live',t('next.states.setupLive'),'i-live')},
    member:{icon:'i-clock',title:t('next.states.gettingReady',{server:server()}),body:t('next.states.gettingReadyBody'),links:[action('switch',t('next.states.switchServer'),'i-server')]},
    'my-list':{icon:'i-bookmark',title:t('next.states.myListEmpty'),body:t('next.states.myListEmptyBody')},
    recordings:{icon:'i-rec',title:t('next.states.noRecordings'),body:t('next.states.noRecordingsBody',{title:referenceFacts.programmeName,time:fixture('next.states.fixture.recordingTime')}),primary:action('guide',t('next.states.openGuide'),'i-guide')},
    live:{eyebrow:<OwnerTag variant="state" identity={server()}/>,title:t('next.states.setupLive'),body:t('next.states.setupLiveBody'),primary:action('source',t('next.states.addTVSource'),'i-plus'),secondary:action('tuners',t('next.states.findTuners'))},
  };
  return <StateLayout {...content[kind]} className={kind === 'owner' ? styles.ownerDenied : kind === 'restricted' ? styles.restricted : kind === 'member' ? styles.memberEmpty : kind === 'live' ? styles.liveEmpty : undefined}
    backdrop={kind === 'my-list' || kind === 'recordings' || kind === 'owner' ? null : {kind:'colour',shape:kind === 'not-found' || kind === 'first-library' ? 'side':'top'}}
    chrome={undefined}
    contentStyle={{top:kind === 'first-library' || kind === 'not-found' ? 330 : kind === 'member' ? 286 : kind === 'my-list' ? 178 : kind === 'owner' ? 44 : 250,...(kind === 'owner' ? {left:20,right:20} : {})}} after={output}/>;
}
function FirstScan() {
  const {action, output} = useActions();
  return <div className={styles.scanStage}><StateView variant="scan" title={t('next.states.firstScan')} body={t('next.states.firstScanBody',{library:referenceFacts.libraryName})} secondary={action('activity',t('next.states.viewActivity'),'i-act')}/>{output}</div>;
}
function SearchEmpty() {
  return <StateView variant="search" title={t('next.states.noMatches',{query:referenceFacts.searchTerm})} body={t('next.states.noMatchesBody')}/>;
}
function Crash() {
  const {action, output} = useActions();
  return <StateLayout title={t('next.states.crashed')} body={t('next.states.crashedBody')} tone="error" icon="i-warn" contentStyle={{top:420}}
    primary={action('reload',t('next.states.reload'),'i-reload')} secondary={action('copy',t('next.states.copyDetails'),'i-copy')} links={[{...action('send',t('next.states.sendOwner'),'i-right'),iconPosition:'end'}]}
    afterActions={<><p className={styles.detailsHeading}>{t('next.states.copyIncludes')}</p><DetailsPanel audience="viewer" facts={[
      {id:'when',label:t('next.states.when'),value:referenceFacts.crashWhen},{id:'app',label:t('next.states.app'),value:referenceFacts.crashApp},{id:'browser',label:t('next.states.browser'),value:referenceFacts.crashBrowser},{id:'where',label:t('next.states.where'),value:referenceFacts.crashWhere},{id:'reference',label:t('next.states.reference'),value:'7F3K Q2MD'},
    ]}/></>} after={output}/>;
}
function LoadingHome({phone = false, early = false}: {phone?: boolean; early?: boolean}) {
  const reference = useReferenceArtwork();
  const evidence = useStatesArtworkEvidence();
  const {action, output} = useActions();
  const episode = cardText({id:'tlou-1',kind:'episode',title:referenceFacts.episodeName,show:{title:referenceFacts.showName},episode:{mode:'seasonal',season:1,episode:1}},'continuation',{t:defaultI18n.t});
  const movie = cardText({id:'inside-out',kind:'movie',title:referenceFacts.movieName,year:2015},'ordinary',{t:defaultI18n.t});
  const arrived = early ? undefined : new Map([
    [0,<WideCard key="episode" model={{...episode,progress:.22,...(phone ? {caption:[...episode.caption.filter(part => part.id !== 'episode-title'),{id:'remaining',text:fixture('next.states.fixture.remaining'),role:'supplement' as const}],detail:[]} : {})}} artwork={reference('assets/bd-tlou.jpg')} arrivalDurationMs={240} style={{width:'100%'}} onNavigate={action('title',referenceFacts.showName).onPress}/>],
    ...(!phone ? [[1,<ReferenceArrival key="movie"><WideCard model={{...movie,progress:.09}} artwork={reference('assets/bd-insideout.jpg')} arrivalDurationMs={240} style={{width:'100%'}} onNavigate={action('title',referenceFacts.movieName).onPress}/></ReferenceArrival>] as const] : []),
  ]);
  return <><HomeSkeleton elapsedMs={early ? 299 : phone ? 500 : 8000} window={{width:phone ? 390 : 1440,height:phone ? 844 : 920,scrollTop:0}} art={evidence?.home} rows={phone ? [{id:'continue',shape:'wide',arrived}] : [{id:'continue',shape:'wide',arrived},{id:'posters',shape:'poster'}]}
    slowNotice={<div className={styles.slowNotice}><span className={styles.waitingDot}/><span><b>{t('next.states.stillLoading')}</b>{' '}{t('next.states.slowConnection',{server:server()})}</span></div>}/>
    {output}</>;
}
function LoadingTitle({phone = false}: {phone?: boolean}) {
  const reference = useReferenceArtwork();
  const [tab, setTab] = useState('episodes');
  const [status, setStatus] = useState<{key: string; status: 'ready' | 'error'}>();
  const supplied = useStatesArtworkEvidence()?.title;
  const artwork = supplied?.foreground ?? reference(phone ? 'assets/pool/tv/slow-horses--bd.jpg':'assets/bd-tlou.jpg');
  return <div className={styles.artworkStage} data-state-artwork-status={status?.key === artwork.key ? status.status : 'loading'} data-state-ambient-source={supplied?.ambient ? 'provided' : 'absent'}><TitleSkeleton elapsedMs={500} window={{width:phone ? 390 : 1440,height:phone ? 844 : 960,scrollTop:0}}
    pressed={{title:phone ? referenceFacts.phoneShow : referenceFacts.showName,artwork,ambient:supplied?.ambient,palette:supplied?.palette,visible:supplied?.visible,onReady:source => {setStatus({key:source.key,status:'ready'});supplied?.onReady?.(source);},onError:source => {setStatus({key:source.key,status:'error'});supplied?.onError?.(source);}}}
    tabs={<Tabs listClassName={phone ? undefined : styles.titleTabsList} value={tab} onValueChange={setTab} phone={phone} items={[{value:'episodes',label:t('next.states.episodes')},{value:'similar',label:t('next.states.moreLikeThis')},{value:'details',label:t('next.states.details')}]}/>}/></div>;
}
function LoadingGrid({phone = false}: {phone?: boolean}) {
  const reference = useReferenceArtwork();
  const {action, output} = useActions();
  const [top, setTop] = useState(0);
  const arrived = new Map([
    [0,<PosterCard key="first" context="grid" density="grid" model={cardText({id:'12-angry-men',kind:'movie',title:referenceFacts.firstMovieName,year:1957},'ordinary',{t:defaultI18n.t})} artwork={reference('assets/pool/movies/12-angry-men.jpg')} arrivalDurationMs={240} style={{width:'100%'}} onNavigate={action('title',referenceFacts.firstMovieName).onPress}/>],
    [1,<ReferenceArrival key="second"><PosterCard context="grid" density="grid" model={cardText({id:'alien',kind:'movie',title:referenceFacts.secondMovieName,year:1979},'ordinary',{t:defaultI18n.t})} artwork={reference('assets/pool/movies/alien.jpg')} arrivalDurationMs={240} style={{width:'100%'}} onNavigate={action('title',referenceFacts.secondMovieName).onPress}/></ReferenceArrival>],
  ]);
  return <div className={styles.gridViewport} onScroll={event => setTop(event.currentTarget.scrollTop)}>
    <GridSkeleton elapsedMs={500} window={{width:phone ? 390 : 1440,height:phone ? 600 : 620,scrollTop:top}} itemCount={50000} arrived={arrived} captions={false} leadingCaptionLines={phone ? [2,1] : [2]}/>{output}
  </div>;
}
function ShortcutCase() {
  const reference = useReferenceArtwork();
  const [open, setOpen] = useState(true);
  const button = useRef<HTMLButtonElement>(null);
  return <div className={styles.shortcutStage}><Artwork source={reference('assets/bd-tlou.jpg')} alt="" className={styles.shortcutPicture}/>
    <Button ref={button} variant="glass" onClick={() => setOpen(true)}>{t('next.states.shortcuts')}</Button>
    <KeyboardShortcuts open={open} onOpenChange={setOpen} skipBackSeconds={10} skipForwardSeconds={30} returnFocusRef={button}/>
  </div>;
}
function ProgressUnknown() { return <div className={styles.progressStage}><CountdownRing progress={null} accessibilityLabel={t('next.states.unknownProgress')}/></div>; }

const specimen = (id:string,labelKey:MessageId,width:number,height:number,render:ComponentType):GalleryCase => ({id,labelKey,width,height,render});
export default defineGalleryFamily({id:'states',labelKey:'next.states.family',cases:[
  specimen('connection-owner','next.states.caseConnection',1440,900,Connection),
  specimen('connection-viewer','next.states.caseConnection',1440,900,()=> <Connection owner={false}/>),
  specimen('connection-mobile','next.states.caseConnection',390,660,()=> <Connection owner={false} phone/>),
  specimen('connection-unknown','next.states.unknownProgress',1440,900,()=> <Connection unknown/>),
  specimen('page-failed','next.states.pageFailed',1440,860,PageFailed),
  specimen('updating','next.states.caseUpdating',1440,860,Updating),
  specimen('playback','next.states.playbackStopped',1440,900,PlayerFailure),
  specimen('cannot-play','next.states.cannotPlay',435,330,()=> <CompactFailure kind="permanent"/>),
  specimen('conversion-busy','next.states.convertingBusy',435,330,()=> <CompactFailure kind="busy"/>),
  specimen('stream-limit','next.states.caseStreamLimit',435,330,()=> <CompactFailure kind="limit"/>),
  specimen('landscape','next.states.playbackStopped',844,390,LandscapeFailure),
  specimen('landscape-details','next.states.details',844,390,()=> <LandscapeFailure details/>),
  specimen('not-found','next.states.notFound',1440,860,()=> <Empty kind="not-found"/>),
  specimen('removed','next.states.caseRemoved',390,844,()=> <Empty kind="removed"/>),
  specimen('restricted','next.states.caseRestricted',390,844,()=> <Empty kind="restricted"/>),
  specimen('owner-only','next.states.ownerRequired',390,660,()=> <Empty kind="owner"/>),
  specimen('first-library','next.states.firstLibrary',1440,860,()=> <Empty kind="first-library"/>),
  specimen('first-scan','next.states.firstScan',1440,620,FirstScan),
  specimen('member-empty','next.states.caseMember',390,844,()=> <Empty kind="member"/>),
  specimen('my-list','next.states.myListEmpty',390,844,()=> <Empty kind="my-list"/>),
  specimen('search-empty','next.states.caseSearch',350,100,SearchEmpty),
  specimen('recordings','next.states.noRecordings',390,844,()=> <Empty kind="recordings"/>),
  specimen('live-empty','next.states.setupLive',390,844,()=> <Empty kind="live"/>),
  specimen('home-skeleton','next.states.homeSkeleton',1440,920,LoadingHome),
  specimen('home-early','next.states.elapsedEarly',1440,920,()=> <LoadingHome early/>),
  specimen('home-skeleton-phone','next.states.homeSkeleton',390,844,()=> <LoadingHome phone/>),
  specimen('title-skeleton','next.states.titleSkeleton',1440,960,LoadingTitle),
  specimen('title-skeleton-phone','next.states.titleSkeleton',390,844,()=> <LoadingTitle phone/>),
  specimen('grid-skeleton','next.states.gridSkeleton',1440,620,LoadingGrid),
  specimen('grid-skeleton-phone','next.states.gridSkeleton',390,600,()=> <LoadingGrid phone/>),
  specimen('crash','next.states.crashed',1440,860,Crash),
  specimen('shortcuts','next.states.shortcuts',1440,900,ShortcutCase),
  specimen('progress-unknown','next.states.unknownProgress',72,72,ProgressUnknown),
]});
