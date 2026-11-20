import React from 'react';
import {BrowseFrame} from '../../../kit/frames';
import {BrowseGrid} from '../../../kit/frames/BrowseGrid';
import {BrowseToolbar, FilteredEmpty} from '../../../kit/browse';
import {HomeRowCard} from '../../../kit/browse/HomeRowCard';
import {LibraryListRow} from '../../../kit/browse/LibraryListRow';
import {Button, Chip} from '../../../kit/controls';
import {AmbientArt} from '../../../kit/artwork';
import {StateView} from '../../../kit/states';
import {useSeeAll, type SeeAllProps} from '../../shared/library/useSeeAll';

export function SeeAllPage(props: SeeAllProps) {
  const {controller, runtime, onBack, onPlay, onOpen, onPlayEntry, onMenu, onFilters, onReshuffle, kindLabel, active, selection} = props;
  const view = useSeeAll(props), {t} = view;
  const list = view.view === 'list';
  return <BrowseFrame testID="home-see-all-frame"
    header={{title: view.title, back: {label: t('next.home.shell.home'), onPress: onBack, testID: 'home-see-all-back'},
      intro: view.intro, ambient: view.ambient && <AmbientArt source={view.ambient} treatment="library" visible={active}/>,
      actions: view.playable ? <><Button icon="i-play" onPress={() => onPlay(false)}>{t('entry.play')}</Button>
        <Button variant="glass" icon="i-shuffle" onPress={() => onPlay(true)}>{t('title.shuffle')}</Button></> : undefined}}
    state={view.failure ?? (!view.ready ? {kind: 'loading'} : undefined)}
    toolbar={view.model ? <BrowseToolbar testID="home-see-all-toolbar" model={view.model} labels={view.labels} resultCount={undefined} resultTotal={view.total}
      onFilters={onFilters} onSortChange={controller.setSort} onReshuffle={onReshuffle} onQuickChange={view.onQuickChange}
      view={{value: view.view, onValueChange: view.setView}}>
      {view.kinds.map(kind => <Chip key={kind.id} selected={kind.selected} count={kind.count} onPress={kind.onPress}>{kind.id === 'all' ? t('search.chip.all') : kindLabel(kind.id)}</Chip>)}
    </BrowseToolbar> : undefined}
    grid={view.gridFailure ? <StateView {...view.gridFailure} variant="centered"/> : view.empty ? <FilteredEmpty onClearFilters={() => {controller.setFilters([]); controller.selectKind(undefined);}}/>
      : <BrowseGrid shape={list ? 'list' : 'poster'} testID={list ? 'home-see-all-list' : 'home-see-all-grid'} collection={controller.collection}
        getKey={item => item.membershipId} selection={selection} renderItem={(item, cell, width) => {
          const current = () => view.isCurrent(item, cell.index);
          const open = () => {if (current()) onOpen(item.entry, current);};
          const play = () => {if (current()) onPlayEntry(item.entry, current);};
          if (list) return <LibraryListRow entry={item.entry} width={width} runtime={runtime} actions={{open, play,
            menu: (_entry, anchor) => {if (anchor && current()) onMenu?.(item.entry, anchor);}, saveView: () => {}}}
            focusId={item.membershipId} onFocus={cell.onFocus} onBlur={cell.onBlur} disabled={!active}/>;
          return <HomeRowCard entry={item.entry} width={width} runtime={runtime} onOpen={open} onPlay={play}
            onMenu={onMenu && (anchor => {if (current()) onMenu(item.entry, anchor);})}
            onFocus={cell.onFocus} onBlur={cell.onBlur}/>;
        }}/>}/>;
}
