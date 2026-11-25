import React, {useRef, type ReactNode} from 'react';
import {heroSpec} from '@portico/design';
import {Artwork} from '../../../kit/artwork';
import {BrowseFrame} from '../../../kit/frames';
import {HomeCard, HomeCardSlot, HomeCollectionCard, HomeCollectionRow, HomeHeroActions, HomeHeroArt, HomeHeroFacts, HomeLiveRow, HomePendingRow, HomeRowPlace, HomeSpotlight, HomeWindow} from '../../../kit/home';
import {Row, type LayoutItemContext} from '../../../kit/layout';
import {Banner} from '../../../kit/overlays';
import {HomeSkeleton, LoadingPaint} from '../../../kit/states';
import {useHome, type HomeProps} from '../../shared/home/useHome';

type HomeCardModel = ReturnType<ReturnType<typeof useHome>['row']>['presentation']['cards'][number];
const homeCardKey = (item: HomeCardModel) => item.entry.id;
const noopFocus = () => {};

/**
 * Home on a phone or tablet (phone-app.html 1, 2 and tablet.html 1, 2), drawn by the library frame (`BrowseFrame`): the spotlight with its
 * reason, title, facts, Play and My List and its dots; then the server's rows (Continue Watching, the shelves, On now,
 * Continue Listening, the ranked row, a collection) and Music's mixes; Customize Home at the foot (kept, not drawn:
 * notes/phone.md). The frame places everything and scrolls it; the page's scroll handler drives the row window, the
 * scrolled header and pull to refresh. `mixes` is Music's mixes row, handed in by the screen that owns it.
 */
export function HomePage({mixes, toolbar, ...props}: HomeProps & Readonly<{mixes?: ReactNode; toolbar?: ReactNode}>) {
  const {runtime} = props;
  const home = useHome(props), {t} = home;
  const renderers = useRef(new Map<string, {deps: readonly unknown[]; renderItem: (item: HomeCardModel, context: LayoutItemContext) => ReactNode}>());
  if (!home.document) {
    const failure = home.failure();
    if (!failure) return <LoadingPaint ready={false} quietMs={0} skeleton={<HomeSkeleton elapsedMs={home.loadingElapsed} visible={home.visible}/>}/>;
    return <BrowseFrame testID="home-error-page" hero={null} state={failure}/>;
  }
  const notice = home.librariesUnavailable ? <Banner placement="inline" variant="notice" tone="warning" icon="i-alert" message={t('next.home.librariesUnavailable')}
    action={runtime.retryLibraries ? {label: t('next.states.tryAgain'), onPress: () => runtime.retryLibraries?.()} : undefined}/> : undefined;
  if (!home.document.spotlight.length && !home.rows.length) return <LoadingPaint ready immediate={home.cachedAtMount} skeleton={null}>
    {notice ? <BrowseFrame testID="home-frame" hero={null} notice={notice}/> : runtime.renderHomeEmpty()}</LoadingPaint>;

  const spotlight = home.noHero && !toolbar ? null : <HomeSpotlight toolbar={toolbar} candidates={home.candidates} activeIndex={home.activeIndex} onIndexChange={home.setActiveIndex} heroFocused={false}
    active={home.heroActive} visible={home.heroVisible} height={home.heroHeight}
    renderArtwork={(candidate, context) => <HomeHeroArt ambient={runtime.ambient(candidate.entry.backdropUrl ?? candidate.entry.posterUrl)}
      picture={home.showBackdrops ? runtime.artwork(candidate.entry.backdropUrl ?? candidate.entry.posterUrl) : undefined} context={context} width={home.width} height={home.heroHeight} visible={home.visible}/>}
    renderMetadata={candidate => <HomeHeroFacts facts={candidate.facts ?? []}/>} renderActions={candidate => <HomeHeroActions actions={home.heroActions(candidate)}/>}
    renderThumbnail={candidate => <Artwork source={runtime.artwork(candidate.entry.backdropUrl ?? candidate.entry.posterUrl)} width={heroSpec.thumbnail.width} height={heroSpec.thumbnail.height}/>}
    onArtworkError={(error, candidate) => runtime.onArtworkError(error, candidate.entry)}/>;

  const rows = home.rows.map((_row, index) => {
    const model = home.row(index), {row, source} = model;
    if (!source) return null;
    if (!model.mounted) return <HomeRowPlace key={row.id} height={model.slotHeight}/>;
    const renderDeps = [runtime, row, model.itemWidth, model.itemHeight, index, model.shown, home.current] as const;
    let cached = renderers.current.get(row.id);
    if (!cached || renderDeps.some((value, at) => !Object.is(value, cached!.deps[at]))) {
      const grow = row.id === 'continue_books';
      cached = {
        deps: renderDeps,
        renderItem: (item, context) => <HomeCardSlot width={model.itemWidth} height={model.itemHeight} grow={grow} waiting={!!context.waiting}>
          <HomeCard runtime={runtime} row={row} entry={item.entry} rank={item.rank} context={context} width={model.itemWidth} rowIndex={index} visible={model.shown} preferred={false} current={home.current}/>
        </HomeCardSlot>,
      };
      renderers.current.set(row.id, cached);
    }
    const body = row.live ? <HomeLiveRow row={row} runtime={runtime} visible={model.shown} itemWidth={home.liveCardWidth} onRange={model.onRange} onVisibleKeys={model.onVisibleKeys}
        onItemFocus={noopFocus} current={home.current} viewportWidth={home.width} tabletMedium={false} scrollMemory={model.scrollMemory}/>
      : row.collection ? <HomeCollectionRow width={home.width}>{bannerWidth => <HomeCollectionCard row={row} runtime={runtime} width={bannerWidth} visible={model.shown} current={home.current} onFocus={noopFocus}/>}</HomeCollectionRow>
      : <Row title={model.title} subtitle={model.subtitle} items={model.presentation.cards} getKey={homeCardKey} getFocusId={home.cardFocusId} initialViewportWidth={home.width}
        scrollMemory={model.scrollMemory} itemWidth={model.itemWidth} itemHeight={model.itemHeight} growItems={row.id === 'continue_books'} captionHeight={model.captionHeight}
        selection={runtime.selection(row.id)} onVisibleRange={model.onRange} onVisibleKeys={model.onVisibleKeys} onSeeAll={model.seeAll}
        renderItem={cached.renderItem}/>;
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
