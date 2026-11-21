import React from 'react';
import {heroSpec} from '@portico/design';
import {Artwork} from '../../../kit/artwork';
import {FocusItem, FocusRegion} from '../../../kit/focus';
import {BrowseFrame} from '../../../kit/frames';
import {HomeCard, HomeCardSlot, HomeCollectionCard, HomeCollectionRow, HomeHeroArt, HomeHeroFacts,
  HomeLiveRow, HomePendingRow, HomeRemoteHint, HomeRowPlace, HomeWindow} from '../../../kit/home';
import {TVHomeCustomize, TVHomeRow, TVHomeSpotlight} from '../../../kit/home/HomeTVParts';
import {Parts} from '../../../kit/cards/primitives';
import {Hero, FocusPreviewHero, Row, type ArtworkRenderContext} from '../../../kit/layout';
import {Banner} from '../../../kit/overlays';
import {HomeSkeleton, LoadingPaint, StateView} from '../../../kit/states';
import {MusicMixesRow} from '../../../screens/destinations/music/MusicMixesRow';
import {homeCardText} from '../../../screens/home/model';
import {TvHomeRecovery} from '../../../screens/home/TvHomeRecovery';
import {authorityKey} from '../../../shell/model';
import {useTVHome, type TVHomeProps} from '../../shared/home/useTVHome';

export function TVHomePage(props: TVHomeProps) {
  const {runtime, authority, active = true} = props, home = useTVHome(props), {t} = home;
  const mixes = runtime.mixes && <MusicMixesRow focusScope={`home:${authorityKey(authority)}.mixes`} port={runtime.mixes}
    artwork={runtime.artwork} reportError={runtime.reportError} title={t('music.mixesForYou')} onOpen={runtime.musicDiscover} active={active}
    focus={home.mixesRegion ? {id: home.mixesRegion.id, up: home.mixesRegion.edges.up!, down: home.mixesRegion.edges.down, left: home.ids.rail} : undefined}/>;
  if (!home.document) return home.recovery.visible && home.recovery.session
    ? <TvHomeRecovery session={home.recovery.session} onRetryNow={home.recovery.retryNow} onSwitchServer={home.recovery.switchServer} connectionFacts={runtime.connectionFacts}/>
    : home.snapshot.error ? <StateView {...runtime.error(home.snapshot.error, home.retry, 'home')}/>
    : <LoadingPaint ready={false} quietMs={0} skeleton={<HomeSkeleton elapsedMs={home.loadingElapsed} visible={home.visible}/>}/>;
  const notice = home.librariesUnavailable ? <Banner testID="home-libraries-unavailable" placement="inline" variant="notice" tone="warning" icon="i-alert"
    message={t('next.home.librariesUnavailable')} action={runtime.retryLibraries ? {label: t('next.states.tryAgain'), onPress: () => runtime.retryLibraries?.()} : undefined}/> : undefined;
  if (home.startModel.actionless) return <LoadingPaint ready immediate={home.cachedAtMount} skeleton={null}>
    {notice ? <BrowseFrame testID="home-frame" hero={null} notice={notice}/> : runtime.renderHomeEmpty()}{mixes}
  </LoadingPaint>;
  const renderArtwork = (candidate: typeof home.candidates[number], context: ArtworkRenderContext) =>
    <HomeHeroArt ambient={runtime.ambient(candidate.entry.backdropUrl ?? candidate.entry.posterUrl)}
      picture={home.showBackdrops ? runtime.artwork(candidate.entry.backdropUrl ?? candidate.entry.posterUrl) : undefined}
      context={context} width={home.width} height={heroSpec.tvHome.artwork} visible={home.visible}/>;
  const spotlight = <Hero candidates={home.candidates} activeIndex={home.activeIndex} onIndexChange={home.setActiveIndex}
    heroFocused={home.heroFocused} testID={home.ids.surface('hero')} active={home.heroActive && !home.preview}
    visible={home.visible && home.heroVisible} variant="tv" height={home.heroHeight}
    copyTop={home.preview ? heroSpec.tvHome.previewCopyTop : undefined}
    renderArtwork={renderArtwork} renderMetadata={candidate => <HomeHeroFacts facts={candidate.facts ?? []}/>}
    renderActions={() => null}
    renderThumbnail={candidate => <Artwork source={runtime.artwork(candidate.entry.backdropUrl ?? candidate.entry.posterUrl)}
      width={heroSpec.thumbnail.width} height={heroSpec.thumbnail.height}/>}
    onArtworkError={(error, candidate) => runtime.onArtworkError(error, candidate.entry)}/>;
  const preview = home.preview && <FocusPreviewHero focusedEntry={home.preview} repeating={home.repeating} active={home.heroActive} visible={home.visible && home.heroVisible}
    renderArtwork={renderArtwork} renderReason={candidate => <Parts parts={homeCardText(candidate.entry, true, runtime.text).caption} fit="wrap"/>}
    renderMetadata={candidate => <HomeHeroFacts facts={candidate.facts ?? []} continuation={homeCardText(candidate.entry, true, runtime.text)}/>}
    renderHint={candidate => <HomeRemoteHint resume={!!candidate.target?.progressSeconds} text={runtime.text}/>}
    onPreviewError={(error, candidate) => runtime.onArtworkError(error, candidate.entry)}/>;
  return <LoadingPaint ready immediate={home.cachedAtMount} skeleton={null}>
    <BrowseFrame testID="home-frame" hero={<TVHomeSpotlight hero={spotlight} preview={preview}/>} notice={notice}
      scroll={home.scroll} onRowsLayout={event => home.setRowsTop(event.nativeEvent.layout.y)}>
      {home.rows.map((row, index) => {
        const model = home.rowModel(index), {source} = model;
        if (!source) return null;
        if (!model.mounted) return <HomeRowPlace key={row.id} height={model.slotHeight}/>;
        const body = row.live ? <HomeLiveRow row={row} runtime={runtime} visible={model.shown} itemWidth={heroSpec.tvHome.channelWidth}
          onRange={model.onRange} onVisibleKeys={model.onVisibleKeys} onItemFocus={item => model.onItemFocus(JSON.stringify([item.channel.sourceId, item.channel.id]))}
          current={home.current} focus={model.focus} viewportWidth={home.width} tabletMedium={false}/>
          : row.collection ? <FocusRegion {...model.focus} id={model.focus?.id ?? home.ids.row(row.id)} orientation="horizontal" order={[home.ids.collection(row.collection)]}>
            <FocusItem focusId={home.ids.collection(row.collection)}><HomeCollectionRow width={home.width}>{width =>
              <HomeCollectionCard row={row} runtime={runtime} width={width} visible={model.shown} current={home.current}
                onFocus={() => model.onItemFocus(row.collection!.collection.id)}/>}</HomeCollectionRow></FocusItem>
          </FocusRegion> : <Row title={model.title} subtitle={model.subtitle} items={model.presentation.cards} getKey={item => item.entry.id}
            getFocusId={home.cardFocusId} focus={model.focus} initialViewportWidth={home.width} testID={home.ids.row(row.id)}
            itemWidth={model.itemWidth} itemHeight={model.itemHeight} growItems={row.id === 'continue_books'} captionHeight={model.captionHeight} selection={runtime.selection(row.id)}
            paging={model.paging} onVisibleRange={model.onRange} onVisibleKeys={model.onVisibleKeys} onSeeAll={model.seeAll}
            onRepeatingChange={home.setRepeating} onItemFocus={item => model.onItemFocus(item.entry.id)}
            renderItem={(item, context) => <HomeCardSlot width={model.itemWidth} height={model.itemHeight} grow={row.id === 'continue_books'} waiting={!!context.waiting}>
              <HomeCard runtime={runtime} row={row} entry={item.entry} rank={item.rank} context={context} width={model.itemWidth}
                rowIndex={index} visible={model.shown} preferred={false} current={home.current}/>
            </HomeCardSlot>}/>;
        return <HomeWindow key={row.id} runtime={runtime} row={source} sectionOrder={index} visible={model.shown}
          physicalIds={model.physicalIds} retainedIds={model.retainedIds} range={model.range}><TVHomeRow onLayout={model.onLayout}>{body}</TVHomeRow></HomeWindow>;
      })}
      {home.pendingRows.map(row => <HomePendingRow key={`pending:${row.id}`} row={row} labels={home.labels} viewportWidth={home.width}/>)}
      {mixes}
      <TVHomeCustomize focus={home.customizeRegion} label={t('home.customize.action')} description={t('next.home.customizeDescriptionTV')}
        onPress={home.customize} disabled={home.holdCustomize}/>
    </BrowseFrame>
  </LoadingPaint>;
}
