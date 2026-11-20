import React from 'react';
import {BrowseFrame} from '../../../kit/frames';
import {BrowseGrid} from '../../../kit/frames/BrowseGrid';
import {BrowseToolbar, FilteredEmpty} from '../../../kit/browse';
import {HomeRowCard} from '../../../kit/browse/HomeRowCard';
import {Button, Chip} from '../../../kit/controls';
import {FocusItem} from '../../../kit/focus';
import {AmbientArt} from '../../../kit/artwork';
import {StateView} from '../../../kit/states';
import {useSeeAll, type SeeAllProps} from '../../shared/library/useSeeAll';

export function SeeAllPage(props: SeeAllProps) {
  const {controller, runtime, active, onBack, onPlay, onOpen, onPlayEntry, onMenu, onFilters, onReshuffle, kindLabel, selection} = props;
  const view = useSeeAll(props), {t, focus} = view;
  return <BrowseFrame testID="home-see-all-frame" focus={focus?.header} startFocus={view.ready ? focus?.header.entry : undefined}
    header={{title: view.title, back: {label: t('next.home.shell.home'), onPress: onBack, testID: 'home-see-all-back'},
      intro: view.intro, ambient: view.ambient && <AmbientArt source={view.ambient} treatment="library" visible={active}/>,
      actions: view.playable ? <><Button focusId="play" icon="i-play" size="hero" onPress={() => onPlay(false)}>{t('entry.play')}</Button>
        <Button focusId="shuffle" variant="glass" icon="i-shuffle" size="hero" onPress={() => onPlay(true)}>{t('title.shuffle')}</Button></> : undefined}}
    state={view.failure ?? (!view.ready ? {kind: 'loading'} : undefined)}
    toolbar={view.model ? <BrowseToolbar testID="home-see-all-toolbar" model={view.model} labels={view.labels}
      resultCount={view.total === undefined ? undefined : t('count.titles', {count: view.total})}
      onFilters={onFilters} onSortChange={controller.setSort} onReshuffle={onReshuffle} onQuickChange={view.onQuickChange}
      region={focus?.toolbar} appliedRegion={focus?.applied} applied={view.applied} onRemoveFilter={view.remove} onClearFilters={view.clear}>
      {view.kinds.map(kind => <FocusItem key={kind.id} focusId={`kind:${kind.id}`}>
        <Chip selected={kind.selected} count={kind.count} onPress={kind.onPress}>{kind.id === 'all' ? t('search.chip.all') : kindLabel(kind.id)}</Chip>
      </FocusItem>)}
    </BrowseToolbar> : undefined}
    grid={view.gridFailure ? <StateView {...view.gridFailure} variant="centered"/> : view.empty ? <FilteredEmpty focus={focus?.grid} onClearFilters={view.clear}/>
      : <BrowseGrid cardSizePercent={runtime.appearanceCardSizePercent} shape="poster" testID="home-see-all-grid" collection={controller.collection}
        getKey={item => item.membershipId} selection={selection} region={focus?.grid} renderItem={(item, cell, width) => {
          const current = () => view.isCurrent(item, cell.index);
          return <FocusItem focusId={item.membershipId} cell={{index: cell.index}}>
            <HomeRowCard entry={item.entry} width={width} runtime={runtime}
              onOpen={() => {if (current()) onOpen(item.entry, current);}} onPlay={() => {if (current()) onPlayEntry(item.entry, current);}}
              onMenu={onMenu && (anchor => {if (current()) onMenu(item.entry, anchor);})}
              onFocus={cell.onFocus} onBlur={cell.onBlur}/>
          </FocusItem>;
        }}/>}/>;
}
