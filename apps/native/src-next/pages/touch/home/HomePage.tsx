import React, {type ReactNode} from 'react';
import {heroSpec} from '@portico/design';
import {Artwork} from '../../../kit/artwork';
import {BrowseFrame} from '../../../kit/frames';
import {HomeCard, HomeCardSlot, HomeCollectionCard, HomeCollectionRow, HomeHeroActions, HomeHeroArt, HomeHeroFacts, HomeLiveRow, HomePendingRow, HomeRowPlace, HomeWindow} from '../../../kit/home';
import {Hero, Row} from '../../../kit/layout';
import {Banner} from '../../../kit/overlays';
import {HomeSkeleton, LoadingPaint} from '../../../kit/states';
import {useHome, type HomeProps} from '../../shared/home/useHome';

/**
 * Home on an upright phone (phone-app.html 1, 2), drawn by the library frame (`BrowseFrame`): the spotlight with its
 * reason, title, facts, Play and My List and its dots; then the server's rows (Continue Watching, the shelves, On now,
 * Continue Listening, the ranked row, a collection) and Music's mixes; Customize Home at the foot (kept, not drawn:
 * notes/phone.md). The frame places everything and scrolls it; the page's scroll handler drives the row window, the
 * scrolled header and pull to refresh. `mixes` is Music's mixes row, handed in by the screen that owns it.
 */
export function HomePage({mixes, ...props}: HomeProps & Readonly<{mixes?: ReactNode}>) {
  const {runtime} = props;
  const home = useHome(props), {t} = home;
  if (!home.document) {
    const failure = home.failure();
    if (!failure) return <LoadingPaint ready={false} quietMs={0} skeleton={<HomeSkeleton elapsedMs={home.loadingElapsed} visible={home.visible}/>}/>;
    return <BrowseFrame testID="home-error-page" hero={null} state={failure}/>;
  }
  const notice = home.librariesUnavailable ? <Banner placement="inline" variant="notice" tone="warning" icon="i-alert" message={t('next.home.librariesUnavailable')}
    action={runtime.retryLibraries ? {label: t('next.states.tryAgain'), onPress: () => runtime.retryLibraries?.()} : undefined}/> : undefined;
  if (!home.document.spotlight.length && !home.rows.length) return <LoadingPaint ready immediate={home.cachedAtMount} skeleton={null}>
    {notice ? <BrowseFrame testID="home-frame" hero={null} notice={notice}/> : runtime.renderHomeEmpty()}</LoadingPaint>;

  const spotlight = home.noHero ? null : <Hero candidates={home.candidates} activeIndex={home.activeIndex} onIndexChange={home.setActiveIndex} heroFocused={false}
    active={home.heroActive} visible={home.heroVisible} variant="phone-app" height={home.heroHeight}
    renderArtwork={(candidate, context) => <HomeHeroArt ambient={runtime.ambient(candidate.entry.backdropUrl ?? candidate.entry.posterUrl)}
      picture={home.showBackdrops ? runtime.artwork(candidate.entry.backdropUrl ?? candidate.entry.posterUrl) : undefined} context={context} width={home.width} height={home.heroHeight} visible={home.visible}/>}
    renderMetadata={candidate => <HomeHeroFacts facts={candidate.facts ?? []}/>} renderActions={candidate => <HomeHeroActions actions={home.heroActions(candidate)}/>}
    renderThumbnail={candidate => <Artwork source={runtime.artwork(candidate.entry.backdropUrl ?? candidate.entry.posterUrl)} width={heroSpec.thumbnail.width} height={heroSpec.thumbnail.height}/>}
    onArtworkError={(error, candidate) => runtime.onArtworkError(error, candidate.entry)}/>;

  const rows = home.rows.map((_row, index) => {
    const model = home.row(index), {row, source} = model;
    if (!source) return null;
    if (!model.mounted) return <HomeRowPlace key={row.id} height={model.slotHeight}/>;
    const title = home.rowTitle(row);
    const body = row.live ? <HomeLiveRow row={row} runtime={runtime} visible={model.shown} itemWidth={home.liveCardWidth} onRange={model.onRange} onVisibleKeys={model.onVisibleKeys}
        onItemFocus={() => {}} current={home.current} viewportWidth={home.width} tabletMedium={false} scrollMemory={model.scrollMemory}/>
      : row.collection ? <HomeCollectionRow width={home.width}>{bannerWidth => <HomeCollectionCard row={row} runtime={runtime} width={bannerWidth} visible={model.shown} current={home.current} onFocus={() => {}}/>}</HomeCollectionRow>
      : <Row title={title} subtitle={home.rowSubtitle(row) || undefined} items={model.presentation.cards} getKey={item => item.entry.id} getFocusId={home.cardFocusId} initialViewportWidth={home.width}
        scrollMemory={model.scrollMemory} itemWidth={model.itemWidth} itemHeight={model.itemHeight} captionHeight={model.captionHeight}
        selection={runtime.selection(row.id)} onVisibleRange={model.onRange} onVisibleKeys={model.onVisibleKeys} onSeeAll={home.seeAll(row, title)}
        renderItem={(item, context) => <HomeCardSlot width={model.itemWidth} height={model.itemHeight} waiting={!!context.waiting}>
          <HomeCard runtime={runtime} row={row} entry={item.entry} rank={item.rank} context={context} width={model.itemWidth} rowIndex={index} visible={model.shown} preferred={false} current={home.current}/>
        </HomeCardSlot>}/>;
    return <HomeWindow key={row.id} runtime={runtime} row={source} sectionOrder={index} visible={model.shown}><HomeRowPlace onLayout={model.onLayout}>{body}</HomeRowPlace></HomeWindow>;
  });

  return <LoadingPaint ready immediate={home.cachedAtMount} skeleton={null}>
    <BrowseFrame testID="home-frame" hero={spotlight} notice={notice} onRowsLayout={event => home.setRowsTop(event.nativeEvent.layout.y)} scroll={home.scroll}
      foot={{action: {label: t('home.customize.action'), icon: 'i-sliders', onPress: home.customize}, description: t('next.home.customizeDescription')}}>
      {rows}
      {home.pendingRows.map(row => <HomePendingRow key={`pending:${row.id}`} row={row} labels={home.labels} viewportWidth={home.width}/>)}
      {mixes}
    </BrowseFrame>
  </LoadingPaint>;
}
