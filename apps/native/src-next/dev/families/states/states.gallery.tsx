import React, {useRef, useState} from 'react';
import {Platform, Text, View, type StyleProp, type ViewStyle} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src/index';
import {statesNativeFixtures} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/states-native-fixtures';
import {cardText} from '../../../../../../packages/client-core/src/presentation/kit-model/index';
import {Artwork, CoverFan, type AmbientArtSource, type ArtworkSource, type ArtworkStatus} from '../../../kit/artwork';
import {PosterCard, WideCard} from '../../../kit/cards';
import {Button, Focusable, Tabs} from '../../../kit/controls';
import {Banner, Menu, OverlaysProvider, SidePanel} from '../../../kit/overlays';
import {Icon} from '../../../kit/Icon';
import {fontFamilies, useTheme} from '../../../kit/theme';
import {CountdownRing, DetailsPanel, GridSkeleton, HomeSkeleton, StateLayout, StateSentence, StateView, TitleSkeleton, retryProgress, type StateFact} from '../../../kit/states';
import type {Gallery} from '../../gallery-registry';

const t = defaultI18n.t;
// Approved reference content is confined to the __DEV__ family graph. No production adapter imports it.
const fixture = {...statesNativeFixtures,
  available: [{id: 'home', path: 'pool/movies/12-angry-men.jpg', title: statesNativeFixtures.filmOne, year: 1957}, {id: 'alien', path: 'pool/movies/alien.jpg', title: statesNativeFixtures.filmTwo, year: 1979}],
};
declare global {var __PORTICO_NATIVE_FAMILY_ASSET_BASE__: string | undefined;}
const asset = (path: string): ArtworkSource => ({uri: `${globalThis.__PORTICO_NATIVE_FAMILY_ASSET_BASE__ ?? 'http://100.120.50.24:8787/assets/'}${path}`, cacheKey: `native-states-reference:${path}`});
const titleArt = asset('pool/tv/slow-horses--bd.jpg');
const movieArt = asset('pool/movies/dune-part-two--bd.jpg');
const wideArt = asset('bd-tlou.jpg');
const homeArt = asset('pool/tv/the-last-of-us--bd.jpg');
const secondContinueArt = asset('pool/movies/inside-out--bd.jpg');

/** Root supplies real platform actions for these DEV cases; no pretend clipboard/store success. */
export type NativeStatesEvidenceActions = Readonly<{
  copyDetails: (text: string) => void;
  openAppStore: (platform: 'ios' | 'android') => void;
  openServerUpdateHelp: () => void;
}>;
declare global {var __PORTICO_NATIVE_STATES_ACTIONS__: NativeStatesEvidenceActions | undefined;}
/** Actual shell components, supplied by the shell owner after its source milestone is integrated. */
export type NativeStatesOwnerEvidence = Readonly<{ownerMark: React.ReactNode; ownerTag: React.ReactNode; updateTag: React.ReactNode}>;
declare global {var __PORTICO_NATIVE_STATES_OWNER__: NativeStatesOwnerEvidence | undefined;}
function evidenceActions(): NativeStatesEvidenceActions {
  const actions = globalThis.__PORTICO_NATIVE_STATES_ACTIONS__;
  if (!actions) throw new Error('The native states platform evidence actions are required.');
  return actions;
}

function Canvas({children, landscape = false, style}: {children: React.ReactNode; landscape?: boolean; style?: StyleProp<ViewStyle>}) {
  const theme = useTheme();
  return <View style={[{width: theme.isTV ? 1920 : landscape ? 844 : 390, height: theme.isTV ? 1080 : landscape ? 390 : 844,
    marginLeft: -theme.gutter, backgroundColor: theme.colors.ink, overflow: 'hidden'}, style]}><OverlaysProvider>{children}</OverlaysProvider></View>;
}

function Connection({detailsInitially = false, owner = false, testID}: {detailsInitially?: boolean; owner?: boolean; testID?: string}) {
  const prefix = testID ?? `dev.states.connection.${owner ? 'owner' : 'viewer'}.${detailsInitially ? 'details' : 'summary'}`;
  const theme = useTheme();
  const [trying, setTrying] = useState(false), [details, setDetails] = useState(detailsInitially), [servers, setServers] = useState(false), [downloads, setDownloads] = useState(false);
  const opener = useRef<View>(null);
  const facts: StateFact[] = [
    {id: 'home', label: t('next.states.homeAddress'), value: fixture.address, status: {label: t('next.states.noAnswer'), tone: 'warning'}},
    {id: 'secure', label: t('next.states.secureAddress'), value: fixture.secureAddress, status: {label: t('next.states.noAnswer'), tone: 'warning'}},
    {id: 'device', label: t('next.states.thisPhone'), status: {label: t('next.states.online'), tone: 'success'}},
  ];
  const ownerIdentity = globalThis.__PORTICO_NATIVE_STATES_OWNER__;
  const detailsAudience = owner && ownerIdentity ? {audience: 'owner' as const, owner: {
    heading: t('next.states.ownerHeading'), ownerMark: ownerIdentity.ownerMark, ownerTag: ownerIdentity.ownerTag,
    description: t('next.states.connectionOwnerBody', {time: fixture.since}),
  }} : {audience: 'viewer' as const};
  return <Canvas>
    {downloads ? <StateView testID={`${prefix}.view`} variant="centered" icon="i-dl" title={t('next.states.native.noDownloads')} body={t('downloads.emptyBody')}
      primaryAction={{id: 'back', label: t('action.back'), onPress: () => setDownloads(false)}} /> : <StateLayout testID={`${prefix}.state`} icon="i-srvoff" tone="warning"
      title={t('next.states.cantReach', {server: fixture.server})} body={t(theme.isTV ? 'next.states.cantReachBody' : 'next.states.cantReachPhoneBody')}
      contentTop={theme.isTV ? undefined : 208}
      retry={{progress: trying ? null : retryProgress(theme.isTV ? 30 : 8, theme.isTV ? 90 : 15), label: trying ? t('next.states.retrying') : t('next.states.retryIn', {seconds: theme.isTV ? 30 : 8}), detail: theme.isTV ? t('next.states.triesSince', {count: 6, time: fixture.since}) : undefined}}
      beforeActions={!theme.isTV && <Focusable testID={`${prefix}.downloads`} onPress={() => setDownloads(true)} style={{height: 74, width: '100%', marginBottom: 14, paddingHorizontal: 14, justifyContent: 'center', borderRadius: 14, backgroundColor: `${theme.colors.raised}D9`, borderWidth: 1, borderColor: `${theme.colors.text}14`}}
        accessibilityLabel={t('next.states.native.downloadsPlay')}>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 12}}>
          <View style={{width: 64, height: 50}}><CoverFan covers={[asset('pool/tv/slow-horses.jpg'), asset('pool/movies/spirited-away.jpg'), asset('pool/movies/inside-out.jpg')]} shape="poster" width={64} height={50} /></View>
          <View style={{flex: 1}}><Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t('next.states.native.downloadsPlay')}</Text><Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('next.states.native.downloadsMore', {title: fixture.phoneTitle, count: 2})}</Text></View>
          <Icon name="i-right" size={20} color={theme.colors.textTertiary} />
        </View>
      </Focusable>}
      primaryAction={{id: 'retry', label: t('next.states.tryNow'), icon: 'i-retry', onPress: () => setTrying(value => !value), hasTVPreferredFocus: theme.isTV}}
      secondaryAction={{id: 'servers', label: t('next.states.switchServer'), icon: theme.isTV ? 'i-server' : undefined, onPress: () => setServers(true)}}
      detailsAction={theme.isTV ? {id: 'details', label: t('next.states.details'), onPress: () => setDetails(true), ref: opener} : undefined}
      help={t(theme.isTV ? 'next.states.native.tvConnectionHelp' : 'next.states.native.phoneConnectionHelp')} />}
    <Menu testID={`${prefix}.menu`} open={servers} onOpenChange={setServers} title={t('next.states.switchServer')} items={[{id: 'reference-server', label: fixture.server, selected: true, onSelect: () => {setTrying(false); setServers(false);}}]} />
    <SidePanel testID={`${prefix}.details`} open={details} onOpenChange={setDetails} title={t('next.states.details')} topInset={0} width={theme.isTV ? 700 : 352} returnFocusRef={opener}>
      <DetailsPanel {...detailsAudience} compact facts={facts} testID={`${prefix}.facts`} />
    </SidePanel>
  </Canvas>;
}

function PageFailure() {
  const prefix = 'dev.states.pagefailure';
  const theme = useTheme();
  const [retrying, setRetrying] = useState(false);
  return <Canvas>{retrying ? <HomeSkeleton elapsedMs={1000} /> : <StateLayout testID={`${prefix}.state`} icon="i-warn" tone="warning" title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')} contentTop={theme.isTV ? undefined : 250}
    primaryAction={{id: 'retry', label: t('next.states.tryAgain'), onPress: () => setRetrying(true), hasTVPreferredFocus: theme.isTV}}
    secondaryAction={{id: 'home', label: t('next.states.goHome'), onPress: () => setRetrying(true)}} />}</Canvas>;
}

function Crash() {
  const prefix = 'dev.states.crash';
  const theme = useTheme();
  const [loading, setLoading] = useState(false);
  return <Canvas>{loading ? <HomeSkeleton elapsedMs={0} /> : <StateLayout testID={`${prefix}.state`} icon="i-warn" tone="error" title={t('next.states.crashed')} body={t('next.states.native.phoneCrashBody')}
    contentTop={theme.isTV ? undefined : 250} backdropHeight={460}
    primaryAction={{id: 'retry', label: t('next.states.tryAgain'), icon: 'i-retry', onPress: () => setLoading(true), hasTVPreferredFocus: theme.isTV}}
    secondaryAction={{id: 'details', label: t('next.states.copyDetails'), icon: 'i-copy', onPress: () => evidenceActions().copyDetails([
      [t('next.states.when'), fixture.since], [t('next.states.app'), fixture.appVersion], [t('next.states.where'), t('next.states.native.homeLoading')],
    ].map(([label, value]) => `${label}: ${value}`).join('\n'))}} />}</Canvas>;
}

function Empty({kind = 'saved', testID}: {testID?: string; kind?: 'saved' | 'member' | 'search' | 'recordings' | 'downloads'}) {
  const prefix = testID ?? `dev.states.empty.${kind}`;
  const [home, setHome] = useState(false);
  const theme = useTheme();
  const title = kind === 'saved' ? t('next.states.myListEmpty') : kind === 'member' ? t('next.states.gettingReady', {server: fixture.server}) : kind === 'search' ? t('next.states.noMatches', {query: fixture.query}) : kind === 'recordings' ? t('next.states.noRecordings') : t('next.states.native.noDownloads');
  const body = kind === 'saved' ? t('next.states.myListEmptyBody') : kind === 'member' ? t('next.states.gettingReadyBody') : kind === 'search' ? t('next.states.noMatchesBody') : kind === 'recordings' ? t('next.states.noRecordingsBody', {title: fixture.programme, time: fixture.programmeTime}) : t('downloads.emptyBody');
  if (kind === 'search' && !theme.isTV) return <Canvas><View style={{position: 'absolute', left: 20, right: 20, top: 130}}><StateView testID={`${prefix}.state`} variant="search" title={title} body={body} /></View></Canvas>;
  return <Canvas>{home ? <HomeSkeleton elapsedMs={1000} /> : theme.isTV ? <View style={{position: 'absolute', left: 510, top: 220, width: 900}}>
    <StateView testID={`${prefix}.view`} variant="centered" icon={kind === 'saved' ? 'i-bookmark' : kind === 'recordings' ? 'i-rec' : 'i-clock'} title={title} body={body}
      primaryAction={kind === 'member' || kind === 'downloads' ? undefined : {id: 'browse', label: t('next.states.goHome'), onPress: () => setHome(true), hasTVPreferredFocus: theme.isTV}} style={{paddingHorizontal: 0}} />
  </View> : <StateLayout testID={`${prefix}.state`} title={title} body={body} icon={kind === 'saved' ? 'i-bookmark' : kind === 'recordings' ? 'i-rec' : kind === 'downloads' ? 'i-dl' : 'i-clock'} contentTop={kind === 'saved' ? 178 : kind === 'member' ? 286 : kind === 'downloads' ? 154 : 250} backdropShape={kind === 'downloads' ? 'none' : undefined} detailsPresentation={kind === 'member' ? 'link' : undefined}
    primaryAction={kind === 'recordings' ? {id: 'guide', label: t('next.states.openGuide'), icon: 'i-guide', onPress: () => setHome(true)} : undefined}
    detailsAction={kind === 'member' ? {id: 'switch', label: t('next.states.switchServer'), icon: 'i-server', onPress: () => setHome(true)} : undefined} />}</Canvas>;
}

function RemovedTitle() {
  const prefix = 'dev.states.removedtitle';
  const [removed, setRemoved] = useState(false), [searching, setSearching] = useState(false);
  return removed ? <LoadingHome testID={`${prefix}.recovery`} /> : <Canvas>{searching ? <StateView testID={`${prefix}.view`} icon="i-search" title={t('next.states.noMatches', {query: fixture.removedTitle})} body={t('next.states.noMatchesBody')}
    primaryAction={{id: 'back', label: t('action.back'), onPress: () => setSearching(false)}} /> : <StateLayout testID={`${prefix}.state`} contentTop={250} backdropHeight={420} icon="i-not"
    title={t('next.states.removed', {title: fixture.removedTitle, server: fixture.server})} body={t('next.states.removedBody')}
    primaryAction={{id: 'search', label: t('next.states.searchTitle', {title: fixture.removedTitle}), icon: 'i-search', onPress: () => setSearching(true)}}
    secondaryAction={{id: 'remove', label: t('next.states.removeContinue'), onPress: () => setRemoved(true)}} />}</Canvas>;
}

function RestrictedTitle() {
  const prefix = 'dev.states.restrictedtitle';
  const [home, setHome] = useState(false), [profiles, setProfiles] = useState(false);
  return home ? <LoadingHome testID={`${prefix}.recovery`} /> : <Canvas><StateLayout testID={`${prefix}.state`} contentTop={250} backdropHeight={420} icon="i-lock"
    title={t('next.states.restricted', {profile: fixture.profile})} body={t('next.states.restrictedBody')}
    primaryAction={{id: 'home', label: t('next.states.goHome'), onPress: () => setHome(true)}}
    secondaryAction={{id: 'profiles', label: t('next.states.switchProfile'), icon: 'i-switch', onPress: () => setProfiles(true)}} />
    <Menu testID={`${prefix}.menu`} open={profiles} onOpenChange={setProfiles} title={t('next.states.switchProfile')} items={[{id: 'current', label: fixture.profile, selected: true, onSelect: () => {setProfiles(false); setHome(true);}}]} />
  </Canvas>;
}

function LoadingHome({early = false, slow = false, testID}: {early?: boolean; slow?: boolean; testID?: string}) {
  const prefix = testID ?? `dev.states.home.${early ? 'early' : slow ? 'slow' : 'loading'}`;
  const theme = useTheme();
  const [selected, setSelected] = useState<number>(), [active, setActive] = useState(0);
  const model = cardText({id: 'tlou-episode', kind: 'episode', title: fixture.episode, show: {id: 'tlou', title: fixture.title}, episode: {mode: 'seasonal', season: 1, episode: 1}, durationSeconds: 4923, positionSeconds: 1083}, 'continuation', {t});
  const second = cardText({id: 'inside-out', kind: 'movie', title: fixture.secondContinueTitle, year: 2015, durationSeconds: 5700, positionSeconds: 513}, 'continuation', {t});
  const focused = active === 0 ? model : second;
  const pressed = {title: focused.title, subtitle: <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 12}}>{focused.caption.map(part => <Text key={part.id} style={[theme.type.meta, {fontSize: 24 * theme.textScale, color: theme.colors.accentStrong}]}>{part.text}</Text>)}</View>, art: {foreground: active === 0 ? homeArt : secondContinueArt}};
  return <Canvas>{selected !== undefined ? <TitleSkeleton testID={`${prefix}.title`} elapsedMs={1000} pressed={{title: selected === 0 ? model.title : second.title, art: {foreground: selected === 0 ? homeArt : secondContinueArt}}} playAction={{id: 'play', label: t('action.back'), onPress: () => setSelected(undefined)}} /> : <HomeSkeleton elapsedMs={early ? 0 : slow ? 9000 : 1000}
    pressed={theme.isTV ? pressed : undefined}
    rowTitle={theme.isTV ? t('home.row.continueWatching') : undefined}
    renderArrived={early ? undefined : index => index === 0 ? <WideCard testID={`${prefix}.continue.last-of-us`} title={model.title} caption={theme.isTV ? model.caption : [...model.caption.filter(part => part.id === 'episode-label'), ...model.detail]} artwork={wideArt} artworkProps={{arrivalDurationMs: 240, visible: true, position: theme.isTV ? {x: .2, y: .3} : undefined}} width={theme.isTV ? 400 : 262} progress={.22} onNavigate={() => setSelected(0)} onFocus={() => setActive(0)} hasTVPreferredFocus={theme.isTV} />
      : index === 1 && theme.isTV ? <WideCard testID={`${prefix}.continue.inside-out`} title={second.title} caption={second.caption} artwork={secondContinueArt} artworkProps={{arrivalDurationMs: 240, visible: true, style: {opacity: .4}}} width={400} progress={.09} onNavigate={() => setSelected(1)} onFocus={() => setActive(1)} /> : undefined}
    slowNotice={<Banner variant="quiet" tone="warning" topInset={theme.isTV ? 90 : 104} message={<Text><Text style={{fontFamily: fontFamilies[700], color: theme.colors.text}}>{t('next.states.stillLoading')}</Text>{' '}{t('next.states.slowConnection', {server: fixture.server})}</Text>} />} />}</Canvas>;
}

function LoadingTitle({early = false, testID}: {early?: boolean; testID?: string}) {
  const prefix = testID ?? `dev.states.title.${early ? 'early' : 'loading'}`;
  const theme = useTheme();
  const [tab, setTab] = useState(theme.isTV ? 'more' : 'episodes'), [playing, setPlaying] = useState(false);
  return <Canvas>{playing ? <StateView testID={`${prefix}.view`} title={t('next.states.playbackStopped')} body={t('next.states.droppedBody', {position: fixture.position})}
    primaryAction={{id: 'back', label: t('action.back'), onPress: () => setPlaying(false), hasTVPreferredFocus: theme.isTV}} /> : <TitleSkeleton testID={`${prefix}.title`} elapsedMs={early ? 0 : 1000}
    pressed={{title: theme.isTV ? fixture.movie : fixture.phoneTitle, art: {foreground: theme.isTV ? movieArt : titleArt, position: {x: .44, y: .2}}}}
    playAction={{id: 'play', label: t('action.play'), icon: 'i-play', onPress: () => setPlaying(true), hasTVPreferredFocus: theme.isTV}}
    tabs={<Tabs testID={`${prefix}.tabs`} value={tab} onValueChange={setTab} items={[...(!theme.isTV ? [{value: 'episodes', label: t('title.episodes')}] : []), {value: 'more', label: t('next.states.moreLikeThis')}, {value: 'details', label: t('title.details')}]}
      style={{marginHorizontal: -theme.gutter}} />}
  />}</Canvas>;
}

function LoadingGrid({early = false, distant = false, testID}: {early?: boolean; distant?: boolean; testID?: string}) {
  const prefix = testID ?? `dev.states.grid.${early ? 'early' : distant ? 'distant' : 'loading'}`;
  const theme = useTheme();
  const [offset, setOffset] = useState(distant ? 414 * 1000 : 0), [selected, setSelected] = useState<string>();
  const render = (index: number) => {
    const item = fixture.available[index];
    if (!item || early) return undefined;
    const model = cardText({id: item.id, kind: 'movie', title: item.title, year: item.year}, 'ordinary', {t});
    return <PosterCard testID={`${prefix}.movie.${item.id}`} title={model.title} caption={model.caption} artwork={asset(item.path)} artworkProps={{arrivalDurationMs: 240, visible: true, style: index === 1 ? {opacity: .4} : undefined}} width={theme.isTV ? 200 : (390 - 32 - 20) / 3}
      hasTVPreferredFocus={theme.isTV && index === 0} onNavigate={() => setSelected(item.title)} />;
  };
  return <Canvas>{selected ? <TitleSkeleton testID={`${prefix}.title`} elapsedMs={1000} pressed={{title: selected}} playAction={{id: 'play', label: t('action.back'), onPress: () => setSelected(undefined)}} /> : <>
    <View style={{position: 'absolute', left: theme.isTV ? 200 : 16, top: theme.isTV ? 262 : 250, height: theme.isTV ? 818 : 510, width: theme.isTV ? 1624 : 358, overflow: 'hidden'}}>
      <View style={{transform: [{translateY: -offset}]}}><GridSkeleton elapsedMs={early ? 0 : 1000} scrollOffset={offset} viewportHeight={theme.isTV ? 818 : 510} totalCount={50000} width={theme.isTV ? 1592 : 358} columns={theme.isTV ? 7 : 3} cardWidth={theme.isTV ? 200 : undefined} renderArrived={render} testID={`${prefix}.grid`} /></View>
    </View>
    <View style={{position: 'absolute', top: theme.isTV ? 144 : 146, left: theme.isTV ? 200 : 20}}><Button testID={`${prefix}.action`} variant="glass" size="compact" onPress={() => setOffset(value => value ? 0 : 414 * 1000)}>{t('next.states.native.gridLoading')}</Button></View>
  </>}</Canvas>;
}

function ProgressCases() {
  const prefix = 'dev.states.progresscases';
  const [progress, setProgress] = useState<number | null>(null);
  const theme = useTheme();
  return <View style={{gap: 24, paddingVertical: 24}}>
    <View style={{flexDirection: 'row', gap: 24}}>{[null, 0, .25, .5, 1].map((value, i) => <CountdownRing key={i} progress={value} size={theme.isTV ? 48 : 34} accessibilityLabel={value === null ? t('next.states.unknownProgress') : t('next.states.loading')} />)}</View>
    <CountdownRing progress={progress} size={theme.isTV ? 88 : 54} />
    <Button testID={`${prefix}.action`} onPress={() => setProgress(value => value === null ? .5 : value === .5 ? 1 : null)}>{progress === null ? t('next.states.unknownProgress') : progress === .5 ? t('next.states.retryIn', {seconds: 8}) : t('next.states.retrying')}</Button>
  </View>;
}

function PlayerFailure({detailsInitially = false, owner = false, testID}: {detailsInitially?: boolean; owner?: boolean; testID?: string}) {
  const prefix = testID ?? `dev.states.playerfailure.${owner ? 'owner' : 'viewer'}.${detailsInitially ? 'details' : 'summary'}`;
  const theme = useTheme();
  const [details, setDetails] = useState(detailsInitially), [trying, setTrying] = useState(false), [closed, setClosed] = useState(false);
  const opener = useRef<View>(null);
  const facts: StateFact[] = [
    {id: 'reason', label: t('next.states.whatHappened'), value: t('next.states.noDataSeconds', {seconds: 20})},
    {id: 'delivery', label: t('next.states.sentAs'), value: t('next.states.convertedTo', {quality: t('next.kit.height', {height: 1080})})},
    {id: 'network', label: t('next.states.network'), value: t('next.states.awayFromHome')},
  ];
  const ownerIdentity = globalThis.__PORTICO_NATIVE_STATES_OWNER__;
  const detailsAudience = owner && ownerIdentity ? {audience: 'owner' as const, owner: {
    heading: t('next.states.ownerHeading'), ownerMark: ownerIdentity.ownerMark, ownerTag: ownerIdentity.ownerTag,
    description: t('next.states.droppedOwnerBody', {rate: fixture.uploadRate}), codesLabel: t('next.states.code', {count: 1}),
    codes: [fixture.diagnosticCode], time: fixture.diagnosticTime,
    actions: [{id: 'copy', label: t('next.states.copyDetails'), icon: 'i-copy' as const, onPress: () => evidenceActions().copyDetails([
      `${t('next.states.whatHappened')}: ${t('next.states.noDataSeconds', {seconds: 20})}`,
      `${t('next.states.sentAs')}: ${t('next.states.convertedTo', {quality: t('next.kit.height', {height: 1080})})}`,
      `${t('next.states.network')}: ${t('next.states.awayFromHome')}`,
      t('next.states.droppedOwnerBody', {rate: fixture.uploadRate}),
      `${t('next.states.code', {count: 1})} ${fixture.diagnosticCode}`, fixture.diagnosticTime,
    ].join('\n'))}],
  }} : {audience: 'viewer' as const};
  return closed ? <LoadingTitle testID={`${prefix}.recovery`} /> : <Canvas landscape>
    <StateLayout testID={`${prefix}.state`} variant="player" frozenPicture={<Artwork source={theme.isTV ? asset('pool/tv/severance--bd.jpg') : titleArt} brightness={.36} saturation={.9} zoom={1.06} />}
      title={t('next.states.playbackStopped')} body={<StateSentence messageId={theme.isTV ? 'next.states.native.tvStoppedBody' : 'next.states.droppedBody'} values={{position: theme.isTV ? fixture.tvPosition : fixture.position}} />}
      icon={theme.isTV ? 'i-warn' : 'i-wifioff'} tone="warning" contentStyle={{transform: [{translateY: theme.isTV ? -54 : -23}]}}
      primaryAction={{id: 'retry', label: t('next.states.tryAgain'), icon: 'i-retry', onPress: () => setTrying(value => !value), hasTVPreferredFocus: theme.isTV}}
      secondaryAction={theme.isTV ? undefined : {id: 'close', label: t('action.close'), onPress: () => setClosed(true)}}
      detailsAction={owner ? {id: 'details', label: t('next.states.details'), ref: opener, onPress: () => setDetails(true), accessibilityState: {expanded: details}} : undefined}
      retry={trying ? {progress: null, label: t('next.states.retrying')} : undefined} />
    <SidePanel testID={`${prefix}.details`} open={details} onOpenChange={setDetails} title={t('next.states.details')} width={theme.isTV ? 700 : 352} topInset={theme.isTV ? 0 : 10} returnFocusRef={opener}>
      <DetailsPanel {...detailsAudience} compact facts={facts} testID={`${prefix}.facts`} />
    </SidePanel>
  </Canvas>;
}

function UpdateServer() {
  const prefix = 'dev.states.updateserver';
  const [downloads, setDownloads] = useState(false), [servers, setServers] = useState(false);
  return downloads ? <Empty testID={`${prefix}.recovery`} kind="downloads" /> : <Canvas><StateLayout testID={`${prefix}.state`} icon="i-update" tone="warning" contentTop={150} backdropHeight={300}
    kicker={globalThis.__PORTICO_NATIVE_STATES_OWNER__?.updateTag}
    title={t('next.states.native.updateServer')} body={t('next.states.native.updateServerBody', {server: fixture.server, minimum: fixture.minimumServer, version: fixture.oldServer})}
    primaryAction={{id: 'instructions', label: t('next.states.native.updateServerHelp'), trailingIcon: 'i-ext', iconSize: 18, onPress: () => evidenceActions().openServerUpdateHelp()}}
    secondaryAction={{id: 'downloads', label: t('action.watchDownloads'), icon: 'i-dl', onPress: () => setDownloads(true)}}
    additionalActions={[{id: 'servers', label: t('next.states.switchServer'), onPress: () => setServers(true)}]} />
    <Menu testID={`${prefix}.menu`} open={servers} onOpenChange={setServers} title={t('next.states.switchServer')} items={[{id: 'reference-server', label: fixture.server, selected: true, onSelect: () => setServers(false)}]} />
  </Canvas>;
}

function RecordingPanel() {
  const prefix = 'dev.states.recordingpanel';
  const [guide, setGuide] = useState(false);
  return guide ? <LoadingGrid testID={`${prefix}.recovery`} /> : <Canvas><View style={{position: 'absolute', left: 200, top: 210, width: 800}}><StateView testID={`${prefix}.view`} variant="panel" icon="i-rec" tone="accent"
    title={t('recordings.noneScheduled')} body={t('recordings.noneScheduledBody')}
    primaryAction={{id: 'guide', label: t('next.states.openGuide'), onPress: () => setGuide(true), hasTVPreferredFocus: true}} /></View></Canvas>;
}

/** Root binds authenticated, version-selected handles in memory before opening this DEV case. */
export type NativeStatesArtworkEvidence = Readonly<{title: string; foreground: ArtworkSource; ambient: AmbientArtSource}>;
declare global {var __PORTICO_NATIVE_STATES_ARTWORK__: NativeStatesArtworkEvidence | undefined;}
function RealAmbientLoadingTitle() {
  const prefix = 'dev.states.realambientloadingtitle';
  const theme = useTheme();
  const handle = globalThis.__PORTICO_NATIVE_STATES_ARTWORK__;
  const [status, setStatus] = useState<ArtworkStatus>('queued');
  const [playing, setPlaying] = useState(false);
  if (!handle || handle.ambient.variant !== 'ambient-v1' || handle.ambient.maxEdge !== 64 || !handle.ambient.version) throw new Error('The native states artwork evidence handle is required.');
  return <Canvas><View testID={`states-real-artwork-${status}`} style={{flex: 1}}>
    {playing ? <StateView testID={`${prefix}.view`} title={handle.title} primaryAction={{id: 'back', label: t('action.back'), onPress: () => setPlaying(false)}} /> : <TitleSkeleton testID={`${prefix}.title`} elapsedMs={1000}
      pressed={{title: handle.title, art: {source: handle.ambient, foreground: handle.foreground, onStatusChange: setStatus}}}
      playAction={{id: 'play', label: t('action.play'), onPress: () => setPlaying(true), hasTVPreferredFocus: theme.isTV}} />}
  </View></Canvas>;
}

function Offline() {
  const prefix = 'dev.states.offline';
  const [retry, setRetry] = useState(false);
  return retry ? <Connection testID={`${prefix}.recovery`} /> : <Canvas><StateLayout testID={`${prefix}.state`} icon="i-wifioff" tone="warning" title={t('next.states.offlinePhone')}
    primaryAction={{id: 'retry', label: t('next.states.tryNow'), onPress: () => setRetry(true)}} /></Canvas>;
}

function UpdateApp() {
  const prefix = 'dev.states.updateapp';
  const [downloads, setDownloads] = useState(false);
  return downloads ? <Empty testID={`${prefix}.recovery`} kind="downloads" /> : <Canvas><StateLayout testID={`${prefix}.state`} icon="i-update" tone="accent" title={t('next.states.native.updateApp')} contentTop={286}
    body={t(Platform.OS === 'ios' ? 'next.states.native.updateIOSBody' : 'next.states.native.updateAndroidBody', {server: fixture.server})}
    primaryAction={{id: 'store', label: t(Platform.OS === 'ios' ? 'next.states.native.openAppStore' : 'next.states.native.openGooglePlay'), onPress: () => evidenceActions().openAppStore(Platform.OS === 'ios' ? 'ios' : 'android')}}
    secondaryAction={{id: 'downloads', label: t('action.watchDownloads'), icon: 'i-dl', onPress: () => setDownloads(true)}}
    help={t('next.states.native.appVersionHelp', {version: fixture.appVersion, server: fixture.server, minimum: fixture.minimumApp})} /></Canvas>;
}

const all = ['phone', 'tablet', 'tv'] as const;
const galleries: readonly Gallery[] = [
  {id: 'states-connection', title: Platform.isTV ? 'next.states.native.connectionTV' : 'next.states.native.connectionPhone', render: Connection, platforms: all},
  {id: 'states-connection-details', title: 'next.states.native.details', render: () => <Connection detailsInitially />, platforms: all},
  {id: 'states-page-failure', title: 'next.states.native.pageFailure', render: PageFailure, platforms: all},
  {id: 'states-saved-empty', title: 'next.states.myListEmpty', render: () => <Empty />, platforms: all},
  {id: 'states-member-empty', title: 'next.states.native.inlineEmpty', render: () => <Empty kind="member" />, platforms: all},
  {id: 'states-search-empty', title: 'next.states.family', render: () => <Empty kind="search" />, platforms: all},
  {id: 'states-recordings-empty', title: 'next.states.noRecordings', render: () => <Empty kind="recordings" />, platforms: all},
  {id: 'states-downloads-empty', title: 'next.states.native.noDownloads', render: () => <Empty kind="downloads" />, platforms: ['phone', 'tablet']},
  {id: 'states-title-removed', title: 'next.states.native.removed', render: RemovedTitle, platforms: ['phone', 'tablet']},
  {id: 'states-title-restricted', title: 'next.states.native.restricted', render: RestrictedTitle, platforms: ['phone', 'tablet']},
  {id: 'states-home-loading', title: 'next.states.native.homeLoading', render: LoadingHome, platforms: all},
  {id: 'states-home-early', title: 'next.states.native.homeLoading', render: () => <LoadingHome early />, platforms: all},
  {id: 'states-home-slow', title: 'next.states.native.homeLoading', render: () => <LoadingHome slow />, platforms: all},
  {id: 'states-title-loading', title: 'next.states.native.titleLoading', render: LoadingTitle, platforms: all},
  {id: 'states-title-early', title: 'next.states.native.titleLoading', render: () => <LoadingTitle early />, platforms: all},
  {id: 'states-grid-loading', title: 'next.states.native.gridLoading', render: LoadingGrid, platforms: all},
  {id: 'states-grid-early', title: 'next.states.native.gridLoading', render: () => <LoadingGrid early />, platforms: all},
  {id: 'states-grid-distant', title: 'next.states.native.gridLoading', render: () => <LoadingGrid distant />, platforms: all},
  {id: 'states-progress', title: 'next.states.family', render: ProgressCases, platforms: all},
  {id: 'states-offline', title: 'next.states.family', render: Offline, platforms: ['phone', 'tablet']},
  {id: 'states-player-failure', title: 'next.states.native.playerFailure', render: PlayerFailure, platforms: all},
  {id: 'states-player-details', title: 'next.states.native.details', render: () => <PlayerFailure detailsInitially />, platforms: all},
  {id: 'states-recording-panel', title: 'next.states.noRecordings', render: RecordingPanel, platforms: ['tv']},
  ...(globalThis.__PORTICO_NATIVE_STATES_ACTIONS__ ? [
    {id: 'states-crash', title: 'next.states.native.crash' as const, render: Crash, platforms: all},
    {id: 'states-update-app', title: 'next.states.native.updateApp' as const, render: UpdateApp, platforms: ['phone', 'tablet'] as const},
  ] : []),
  ...(globalThis.__PORTICO_NATIVE_STATES_OWNER__ ? [{id: 'states-connection-owner-details', title: 'next.states.native.details' as const, render: () => <Connection detailsInitially owner />, platforms: all}] : []),
  ...(globalThis.__PORTICO_NATIVE_STATES_OWNER__ && globalThis.__PORTICO_NATIVE_STATES_ACTIONS__ ? [
    {id: 'states-player-owner-details', title: 'next.states.native.details' as const, render: () => <PlayerFailure detailsInitially owner />, platforms: all},
    {id: 'states-update-server', title: 'next.states.native.updateServer' as const, render: UpdateServer, platforms: ['phone', 'tablet'] as const},
  ] : []),
  ...(globalThis.__PORTICO_NATIVE_STATES_ARTWORK__ ? [{id: 'states-title-real-artwork', title: 'next.states.native.titleLoading' as const, render: RealAmbientLoadingTitle, platforms: all}] : []),
];
export default galleries;
