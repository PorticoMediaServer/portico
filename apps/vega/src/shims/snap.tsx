import * as Vega from '@amazon-devices/react-native-kepler';
import React, {createContext, forwardRef, useCallback, useContext, useMemo, useRef} from 'react';
import {animations} from './motion';

/**
 * The Apple TV build's focus-following scroll, which the shared rows and pages ask for with
 * `snapToAlignment="item"`, `snapToItemPadding`, and the `scrollSnapOffset` / `scrollSnapAlign`
 * marks on views (react-native-tvos). Vega's scroll view has none of these, and stops the app
 * when it is given them, so they are taken off here and the same rule is applied from focus:
 *
 * When focus moves to a control, each scroll view around it that snaps by item looks outward
 * from the control for marked views inside itself; the outermost one wins. A view marked with an
 * offset comes to rest with its leading edge that far into the viewport; one marked `start` comes
 * to rest the scroll view's item padding in. With no marked view the scroll view is left to the
 * system, which brings the focused control into view.
 */
type Host = {measureLayout(relativeTo: unknown, onSuccess: (x: number, y: number, width: number, height: number) => void, onFail?: () => void): void};
type Align = 'start' | 'center' | 'end';

type Scroller = {
  parent: Scroller | null;
  horizontal: boolean;
  padding: number;
  viewport: number;
  /** Length of the content along the scroll axis, or 0 while it is not known. */
  content: number;
  inner(): unknown;
  scrollTo(offset: number): void;
};

type Mark = {parent: Mark | null; scroller: Scroller | null; node: React.RefObject<Host | null>; offset?: number; align?: Align};

const ScrollerContext = createContext<Scroller | null>(null);
const MarkContext = createContext<Mark | null>(null);

const within = (inner: Scroller | null, outer: Scroller): boolean => {
  for (let scroller = inner; scroller; scroller = scroller.parent) if (scroller === outer) return true;
  return false;
};

function rest(scroller: Scroller, mark: Mark): void {
  const node = mark.node.current, inner = scroller.inner();
  if (!node || !inner) return;
  node.measureLayout(inner, (x, y, width, height) => {
    const origin = scroller.horizontal ? x : y, extent = scroller.horizontal ? width : height;
    let offset: number;
    if (mark.offset !== undefined) offset = origin - mark.offset;
    else if (mark.align === 'start') offset = origin - scroller.padding;
    else if (mark.align === 'center') offset = origin + extent / 2 - scroller.viewport / 2 + scroller.padding / 2;
    else if (mark.align === 'end') offset = origin + extent - scroller.viewport + scroller.padding;
    else return;
    if (scroller.content > 0) offset = Math.min(offset, Math.max(scroller.content - scroller.viewport, 0));
    scroller.scrollTo(Math.max(0, offset));
  }, () => {});
}

/** What a control needs to know to bring its surroundings to rest when it takes focus. */
export function useSnap(): (() => void) | undefined {
  const scroller = useContext(ScrollerContext), mark = useContext(MarkContext);
  return useMemo(() => {
    if (!scroller || !mark) return undefined;
    return () => {
      for (let around: Scroller | null = scroller; around; around = around.parent) {
        let chosen: Mark | null = null;
        for (let candidate: Mark | null = mark; candidate && within(candidate.scroller, around); candidate = candidate.parent) chosen = candidate;
        if (chosen) rest(around, chosen);
      }
    };
  }, [scroller, mark]);
}

type ScrollProps = React.ComponentProps<typeof Vega.ScrollView> & {snapToItemPadding?: number};
type ScrollNode = React.ComponentRef<typeof Vega.ScrollView>;

const SnapScroll = forwardRef<ScrollNode, ScrollProps>(function SnapScroll(
  {snapToAlignment: _alignment, snapToItemPadding, horizontal, onLayout, onContentSizeChange, ...props},
  forwarded,
) {
  const parent = useContext(ScrollerContext);
  const node = useRef<ScrollNode | null>(null);
  const along = horizontal === true;
  const scroller = useMemo<Scroller>(() => ({
    parent,
    horizontal: along,
    padding: 0,
    viewport: 0,
    content: 0,
    inner: () => (node.current as unknown as {getInnerViewRef?: () => unknown} | null)?.getInnerViewRef?.(),
    scrollTo: offset => node.current?.scrollTo(along ? {x: offset, animated: animations} : {y: offset, animated: animations}),
  }), [parent, along]);
  scroller.padding = snapToItemPadding ?? 0;
  const attach = useCallback((value: ScrollNode | null) => {
    node.current = value;
    if (typeof forwarded === 'function') forwarded(value);
    else if (forwarded) forwarded.current = value;
  }, [forwarded]);
  const laidOut = useCallback<NonNullable<ScrollProps['onLayout']>>(event => {
    const {width, height} = event.nativeEvent.layout;
    scroller.viewport = along ? width : height;
    onLayout?.(event);
  }, [scroller, along, onLayout]);
  const sized = useCallback((width: number, height: number) => {
    scroller.content = along ? width : height;
    onContentSizeChange?.(width, height);
  }, [scroller, along, onContentSizeChange]);
  return (
    <ScrollerContext.Provider value={scroller}>
      <Vega.ScrollView ref={attach} horizontal={horizontal} onLayout={laidOut} onContentSizeChange={sized} {...props} />
    </ScrollerContext.Provider>
  );
});

const snaps = (props: {snapToAlignment?: unknown}): boolean => props.snapToAlignment === 'item';

const ScrollViewShim = forwardRef<ScrollNode, ScrollProps>(function ScrollView(props, ref) {
  return snaps(props) ? <SnapScroll ref={ref} {...props} /> : <Vega.ScrollView ref={ref} {...props} />;
});
export const ScrollView = ScrollViewShim as unknown as typeof Vega.ScrollView;
export type ScrollView = Vega.ScrollView;

const snapScroll = (props: ScrollProps): React.ReactElement => <SnapScroll {...props} />;

type ListProps = React.ComponentProps<typeof Vega.FlatList>;
const FlatListShim = forwardRef<React.ComponentRef<typeof Vega.FlatList>, ListProps>(function FlatList(props, ref) {
  // The list hands its props to its scroll view, which is where the snapping ones are taken off.
  return snaps(props) ? <Vega.FlatList ref={ref} renderScrollComponent={snapScroll as never} {...props} /> : <Vega.FlatList ref={ref} {...props} />;
});
export const FlatList = FlatListShim as unknown as typeof Vega.FlatList;
export type FlatList<Item = any> = Vega.FlatList<Item>;

type ViewProps = React.ComponentProps<typeof Vega.View> & {scrollSnapOffset?: number; scrollSnapAlign?: Align};
type ViewNode = React.ComponentRef<typeof Vega.View>;

const Marked = forwardRef<ViewNode, ViewProps>(function Marked({scrollSnapOffset, scrollSnapAlign, ...props}, forwarded) {
  const parent = useContext(MarkContext), scroller = useContext(ScrollerContext);
  const node = useRef<Host | null>(null);
  const mark = useMemo<Mark>(() => ({parent, scroller, node, offset: scrollSnapOffset, align: scrollSnapAlign}), [parent, scroller, scrollSnapOffset, scrollSnapAlign]);
  const attach = useCallback((value: ViewNode | null) => {
    node.current = value as unknown as Host | null;
    if (typeof forwarded === 'function') forwarded(value);
    else if (forwarded) forwarded.current = value;
  }, [forwarded]);
  // Not collapsable: the view has to exist to be measured, whatever its style.
  return <MarkContext.Provider value={mark}><Vega.View ref={attach} collapsable={false} {...props} /></MarkContext.Provider>;
});

const ViewShim = forwardRef<ViewNode, ViewProps>(function View(props, ref) {
  return props.scrollSnapOffset === undefined && props.scrollSnapAlign === undefined ? <Vega.View ref={ref} {...props} /> : <Marked ref={ref} {...props} />;
});
export const View = ViewShim as unknown as typeof Vega.View;
export type View = Vega.View;
