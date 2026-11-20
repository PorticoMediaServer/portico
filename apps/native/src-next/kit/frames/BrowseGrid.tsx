import React, {type Ref} from 'react';
import {frameSpec} from '@portico/design';
import {WindowedGrid, type WindowedGridHandle} from '../layout';
import type {WindowedGridProps} from '../layout/WindowedGrid';
import {cardCaptionHeight} from '../cards/caption-metrics';
import {SkeletonBlock} from '../states';
import {useTheme} from '../theme';
import {browseGridCard} from './BrowseFrame';
import {useFrameMetrics} from './metrics';

type Props<T> = Omit<WindowedGridProps<T>, 'itemWidth' | 'itemHeight' | 'captionHeight' | 'gap' | 'rowGap' | 'gutter' | 'rightGutter' | 'paddingBottom' | 'renderPlaceholder' | 'renderItem' | 'renderNativeSlot'> & {
  gridRef?: Ref<WindowedGridHandle>;
  shape: 'list' | 'poster' | 'square' | 'genre';
  alphabet?: boolean;
  renderItem: (item: T, context: Parameters<WindowedGridProps<T>['renderItem']>[1], width: number) => React.ReactNode;
  renderNativeSlot?: (item: T | undefined, context: Parameters<NonNullable<WindowedGridProps<T>['renderNativeSlot']>>[1], width: number, height: number) => React.ReactNode;
};

export function BrowseGrid<T>({gridRef, shape, alphabet, renderItem, renderNativeSlot, ...props}: Props<T>) {
  const frame = useFrameMetrics(), theme = useTheme(), spec = frameSpec.browse;
  const list = shape === 'list';
  const rightGutter = alphabet ? frame.rightGutter + theme.space.xs : frame.rightGutter;
  const width = list ? frame.width - frame.gutter - rightGutter
    : browseGridCard({...frame, rightGutter}, false, shape === 'genre' ? spec.genreWidth[frame.form] : undefined).width;
  const art = list ? spec.listRow[frame.form] : shape === 'genre' ? width / theme.spec.card.aspect.wide : width / theme.spec.card.aspect[shape];
  const caption = list ? art : shape === 'genre' ? 0 : cardCaptionHeight({...theme, fontScale: 1}, 2);
  return <WindowedGrid<T> {...props} ref={gridRef} itemWidth={width} itemHeight={art + (list ? 0 : caption)} captionHeight={caption}
    gap={list ? 0 : spec.grid.columnGap[frame.form]} rowGap={list ? 0 : spec.grid.rowGap[frame.form]}
    gutter={frame.gutter} rightGutter={rightGutter} paddingBottom={frame.bottomClear} style={{flex: 1}}
    renderPlaceholder={() => <SkeletonBlock width={width} height={art} radius={theme.radii.artwork}/>}
    renderItem={(item, context) => renderItem(item, context, width)}
    renderNativeSlot={renderNativeSlot && ((item, context) => renderNativeSlot(item, context, width, art))}/>;
}
