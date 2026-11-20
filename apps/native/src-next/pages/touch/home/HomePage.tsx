import React from 'react';
import {RefreshControl, ScrollView, Text, View} from 'react-native';
import Animated from 'react-native-reanimated';
import {heroSpec} from '@portico/design';
import {AmbientArt, Artwork} from '../../../kit/artwork';
import {Button} from '../../../kit/controls';
import {BrowseFrame} from '../../../kit/frames';
import {Hero, Row} from '../../../kit/layout';
import {Banner} from '../../../kit/overlays';
import {HomeSkeleton, LoadingPaint, SkeletonBlock, StateView} from '../../../kit/states';
import {MusicMixesRow} from '../../../screens/destinations/music/MusicMixesRow';
import {HomeHeroFacts} from '../../../screens/home/HomeHeroFacts';
import {authorityKey} from '../../../shell/model';
import {HomeCard, HomeCollectionCard, HomeLiveRow, HomePendingRow, HomeWindow, homeRowSubtitle} from '../../shared/home/home-cards';
import {useHome, type HomeCandidate, type HomeProps} from '../../shared/home/useHome';

/**
 * Home on an upright phone (phone-app.html 1, 2), drawn by the library frame (`BrowseFrame`): the spotlight with its
 * reason, title, facts, Play and My List and its dots; then the server's rows (Continue Watching, the shelves, On now,
 * Continue Listening, the ranked row, a collection) and Music's mixes; Customize Home at the foot (kept, not drawn:
 * notes/phone.md). The frame places the hero, the rows, their gaps and the bottom clearance; Home keeps its own
 * scroller, which drives the row window, the scrolled header and pull to refresh.
 */
export function HomePage(props: HomeProps & Readonly<{bottomInset?: number}>) {
  const {runtime, authority, bottomInset = 0} = props;
  const home = useHome(props), {theme, t} = home;
  if (!home.document) return home.error
    // E-mobile-03/04: a full page below the status bar and header that scrolls, so large text never clips its actions.
    ? <ScrollView testID="home-error-page" contentContainerStyle={{flexGrow: 1, justifyContent: 'center', paddingTop: home.errorTop, paddingBottom: bottomInset + 12}}>
      <StateView variant="centered" {...runtime.error(home.error, home.retry, 'home')}/></ScrollView>
    : <LoadingPaint ready={false} quietMs={0} skeleton={<HomeSkeleton elapsedMs={home.loadingElapsed} visible={home.visible}/>}/>;
  const notice = home.librariesUnavailable ? <View testID="home-libraries-unavailable" style={{paddingHorizontal: theme.gutter, paddingTop: 16, paddingBottom: 4}}>
    <Banner placement="inline" variant="notice" tone="warning" icon="i-alert" message={t('next.home.librariesUnavailable')}
      action={runtime.retryLibraries ? {label: t('next.states.tryAgain'), onPress: () => runtime.retryLibraries?.()} : undefined}/></View> : null;
  if (!home.document.spotlight.length && !home.rows.length) return <LoadingPaint ready immediate={home.cachedAtMount} skeleton={null}>
    {notice ? <View style={{paddingTop: 100}}>{notice}</View> : runtime.renderHomeEmpty()}</LoadingPaint>;

  const heroActions = (candidate: HomeCandidate) => {
    const offers = home.heroOffers(candidate);
    return <View style={{flexDirection: 'row', alignItems: 'center', gap: 12}}>
      {offers.playable && candidate.target?.playback && <Button icon="i-play" variant="primary" size="hero" onPress={() => home.play(candidate)}>{candidate.primaryLabel ?? t('next.home.play')}</Button>}
      {offers.watchlisted !== undefined && <Button icon={offers.watchlisted ? 'i-check' : 'i-plus'} variant="glass" size="hero" onPress={() => home.myList(candidate, !offers.watchlisted)}>{t('next.home.myList')}</Button>}
    </View>;
  };
  const spotlight = home.noHero ? null : <View style={{height: home.heroBlock}}><Hero candidates={home.candidates} activeIndex={home.activeIndex} onIndexChange={home.setActiveIndex} heroFocused={false}
    active={home.heroActive} visible={home.heroVisible} variant="phone-app" height={home.heroHeight}
    renderArtwork={(candidate, context) => <AmbientArt source={runtime.ambient(candidate.entry.backdropUrl ?? candidate.entry.posterUrl)}
      foreground={home.showBackdrops ? runtime.artwork(candidate.entry.backdropUrl ?? candidate.entry.posterUrl) : undefined} treatment="phone" visible={home.visible && context.visible} priority
      onReady={context.onReady} onFallbackReady={context.onReady} onError={context.onError} foregroundStyle={context.foregroundStyle} width={home.width} height={home.heroHeight}/>}
    renderMetadata={candidate => <HomeHeroFacts facts={candidate.facts ?? []}/>} renderActions={heroActions}
    renderThumbnail={candidate => <Artwork source={runtime.artwork(candidate.entry.backdropUrl ?? candidate.entry.posterUrl)} width={heroSpec.thumbnail.width} height={heroSpec.thumbnail.height}/>}
    onArtworkError={(error, candidate) => runtime.onArtworkError(error, candidate.entry)}/></View>;

  const rows = home.rows.map((_row, index) => {
    const model = home.row(index), {row, source} = model;
    if (!source) return null;
    if (!model.mounted) return <View key={row.id} style={{height: model.slotHeight}}/>;
    const title = home.rowTitle(row), subtitle = home.rowSubtitle(row);
    const body = row.live ? <HomeLiveRow row={row} runtime={runtime} visible={model.shown} itemWidth={theme.spec.card.live.width.phone} onRange={model.onRange} onVisibleKeys={model.onVisibleKeys}
        onItemFocus={() => {}} current={home.current} viewportWidth={home.width} tabletMedium={false} scrollMemory={model.scrollMemory}/>
      : row.collection ? <View style={{paddingHorizontal: theme.gutter}}><HomeCollectionCard row={row} runtime={runtime} width={home.width - theme.gutter * 2} visible={model.shown} current={home.current} onFocus={() => {}}/></View>
      : <Row title={title} subtitle={subtitle ? <Text numberOfLines={1} style={[homeRowSubtitle(theme), {color: theme.colors.textTertiary, includeFontPadding: false}]}>{subtitle}</Text> : undefined}
        items={model.presentation.cards} getKey={item => item.entry.id} getFocusId={home.cardFocusId} initialViewportWidth={home.width} scrollMemory={model.scrollMemory}
        itemWidth={model.itemWidth} itemHeight={model.itemHeight} captionHeight={model.captionHeight} gutter={theme.gutter} rightGutter={theme.gutter}
        selection={runtime.selection(row.id)} onVisibleRange={model.onRange} onVisibleKeys={model.onVisibleKeys} onSeeAll={home.seeAll(row, title)}
        renderItem={(item, context) => <View style={theme.fontScale > 1 || theme.textScale > 1 ? {width: model.itemWidth, minHeight: model.itemHeight} : {width: model.itemWidth, height: model.itemHeight}}>
          <LoadingPaint ready={!context.waiting} immediate={!context.waiting} skeleton={<View style={{backgroundColor: theme.colors.ink}}><SkeletonBlock width={model.itemWidth} height={model.itemHeight}/></View>}>
            <HomeCard runtime={runtime} row={row} entry={item.entry} rank={item.rank} context={context} width={model.itemWidth} rowIndex={index} visible={model.shown} preferred={false} current={home.current}/>
          </LoadingPaint></View>}/>;
    return <HomeWindow key={row.id} runtime={runtime} row={source} sectionOrder={index} visible={model.shown}><View onLayout={model.onLayout}>{body}</View></HomeWindow>;
  });

  return <LoadingPaint ready immediate={home.cachedAtMount} skeleton={null}>
    <BrowseFrame testID="home-frame" hero={spotlight} onRowsLayout={event => home.setRowsTop(event.nativeEvent.layout.y)}
      scroller={(content, style) => <Animated.ScrollView ref={home.scroller.ref} testID="native-home" style={home.scroller.style} scrollEventThrottle={16} onScroll={home.scroller.onScroll}
        refreshControl={<RefreshControl refreshing={home.scroller.refreshing} tintColor={theme.colors.text} onRefresh={home.scroller.onRefresh}/>}
        onScrollBeginDrag={home.scroller.onScrollBeginDrag} showsVerticalScrollIndicator={false}
        contentContainerStyle={{paddingBottom: Number(style.paddingBottom ?? 0) + bottomInset}}>{content}</Animated.ScrollView>}>
      {notice}
      {rows}
      {home.pendingRows.map(row => <HomePendingRow key={`pending:${row.id}`} row={row} labels={home.labels} viewportWidth={home.width}/>)}
      {runtime.mixes ? <MusicMixesRow focusScope={`home:${authorityKey(authority)}.mixes`} port={runtime.mixes} artwork={runtime.artwork} reportError={runtime.reportError} title={t('music.mixesForYou')} onOpen={runtime.musicDiscover} active={props.active ?? true}/> : null}
      <View style={{marginHorizontal: 24, alignItems: 'center', gap: 12}}>
        {/* Centred over its description: the frame keeps Customize Home (notes/phone.md, "not drawn but kept"). */}
        <Button variant="glass" icon="i-sliders" style={{alignSelf: 'center', height: 48, paddingHorizontal: 22}} onPress={home.customize}>{t('home.customize.action')}</Button>
        <Text style={[theme.type.caption, {color: theme.colors.textTertiary, textAlign: 'center'}]}>{t('next.home.customizeDescription')}</Text>
      </View>
    </BrowseFrame>
  </LoadingPaint>;
}
