import React, {createContext, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState} from 'react';
import {Image, View, type StyleProp, type ViewStyle} from 'react-native';
import Animated, {cancelAnimation, runOnJS, useAnimatedStyle, useSharedValue, withTiming, type SharedValue} from 'react-native-reanimated';
import {freezeOverlayPaint, type OverlayPaintNode, type OverlayPaintTypes} from '../overlays/overlay-paint-model';
import {OverlayPaintProvider, OVERLAY_PAINT_INERT} from '../overlays/OverlayPaintContext';
import {useMotion} from '../useMotion';
import {useListeningMiniPaint} from './ListeningCoverBridge';
import {sameDockFence} from './model';
import type {DockRenderOccupant} from './types';
import type {DockVideoExitPaint} from './video-exit-paint';
import type {PlaybackFence} from '../../../../../packages/player-core/src';

type Pose = Readonly<{alpha: number; x: number; y: number; scale: number}>;
type Paint = Readonly<{alpha: SharedValue<number>; x: SharedValue<number>; y: SharedValue<number>; scale: SharedValue<number>; hideArt?: boolean}>;
const Phase = createContext<Paint | undefined>(undefined);
const noSubscribe = () => () => {};
type ExitCanvas = Readonly<{publish(key: string, node: React.ReactNode): () => void; entries: ReadonlyMap<string, React.ReactNode>}>;
const VideoExitCanvas = createContext<ExitCanvas | undefined>(undefined);
/** Only audited passive art enters this canvas. It sits above the persistent
 * video surface; it never owns a player, capture, route or business body. */
export function DockVideoExitProvider({children}: {children: React.ReactNode}) {
  const [entries, setEntries] = useState<ReadonlyMap<string, React.ReactNode>>(() => new Map());
  const publish = useCallback((key: string, node: React.ReactNode) => {
    setEntries(current => new Map(current).set(key, node));
    return () => setEntries(current => {
      if (current.get(key) !== node) return current;
      const next = new Map(current); next.delete(key); return next;
    });
  }, []);
  const value = useMemo(() => ({entries, publish}), [entries, publish]);
  return <VideoExitCanvas.Provider value={value}>{children}</VideoExitCanvas.Provider>;
}
export function DockVideoExitCanvas({style}: {style?: StyleProp<ViewStyle>}) {
  const canvas = useContext(VideoExitCanvas);
  return <View pointerEvents="none" accessible={false} accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={[{position: 'absolute', inset: 0}, style]}>
    {canvas && [...canvas.entries].map(([key, node]) => <React.Fragment key={key}>{node}</React.Fragment>)}
  </View>;
}
export type DockPaintRowProps = Readonly<{
  opener: React.ReactElement; art: React.ReactElement; words: React.ReactNode;
  controls?: React.ReactElement<React.ComponentProps<typeof View>>; tools?: React.ReactNode; line?: React.ReactNode;
  expanded: boolean; containerStyle: StyleProp<ViewStyle>; textStyle: StyleProp<ViewStyle>;
  listeningFence?: PlaybackFence; artWidth: number; openerGap: number; rowGap: number;
}>;

/** Materialised UI only. Its callbacks belong to the live input tree; the frozen
 * outgoing copy has no refs or business callbacks and renders under INERT. */
export function DockPaintRow({opener, art, words, controls, tools, line, expanded, containerStyle, textStyle, rowGap, listeningFence}: DockPaintRowProps) {
  const paint = useContext(Phase), motion = useMotion();
  const alpha = paint?.alpha, x = paint?.x, y = paint?.y, scale = paint?.scale;
  const mini = useListeningMiniPaint(listeningFence);
  const miniActive = mini.active, miniProgress = mini.progress, miniDragging = mini.dragging, miniPage = mini.page;
  const visibility = () => {
    'worklet';
    if (!miniActive) return 1;
    if (motion.reduce && !miniDragging?.value) return 1 - (miniPage?.value ?? 0);
    return Math.max(0, 1 - Math.max(0, Math.min(1, 1 - (miniProgress?.value ?? 1))) * 4);
  };
  const artStyle = useAnimatedStyle(() => ({opacity: paint?.hideArt ? 0 : (alpha?.value ?? 1) * visibility(), transform: [{scale: motion.transforms ? scale?.value ?? 1 : 1}]}));
  const wordsStyle = useAnimatedStyle(() => ({opacity: (alpha?.value ?? 1) * visibility(), transform: [{translateX: motion.transforms ? x?.value ?? 0 : 0}, {translateY: motion.transforms ? y?.value ?? 0 : 0}]}));
  const controlStyle = useAnimatedStyle(() => ({opacity: (alpha?.value ?? 1) * visibility(), transform: [{translateY: motion.transforms ? y?.value ?? 0 : 0}]}));
  const lineStyle = useAnimatedStyle(() => ({opacity: (alpha?.value ?? 1) * visibility()}));
  const toolsElement = React.isValidElement(tools) && tools.type === View ? tools as React.ReactElement<React.ComponentProps<typeof View>> : undefined;
  const toolsView = toolsElement
    ? <Animated.View {...toolsElement.props} style={[toolsElement.props.style, controlStyle]}/>
    : tools ? <Animated.View style={[{flexDirection: 'row', alignItems: 'center', gap: rowGap}, controlStyle]}>{tools}</Animated.View> : null;
  return <View style={[{position: 'absolute', inset: 0}, containerStyle]}>
    {React.cloneElement(opener as React.ReactElement<{children?: React.ReactNode}>, {}, <>
      <Animated.View style={artStyle}>{art}</Animated.View>
      <Animated.View style={[textStyle, wordsStyle]}>{words}</Animated.View>
    </>)}
    {expanded && <View style={{flex: 1}}/>}{toolsView}
    {controls && <Animated.View {...controls.props} style={[controls.props.style, controlStyle]}/>}
    {line && <Animated.View pointerEvents="none" style={[{position: 'absolute', inset: 0}, lineStyle]}>{line}</Animated.View>}
  </View>;
}

type Retiring = Readonly<{key: string; node: OverlayPaintNode; pose: Pose; targetX: number; skipArt?: boolean; video?: DockVideoExitPaint}>;
const sameTarget = (a: DockRenderOccupant, b: DockRenderOccupant) => sameDockFence(a.playback.fence, b.playback.fence)
  && a.mode === b.mode && a.remote?.id === b.remote?.id && a.remote?.phase === b.remote?.phase;
// Receiver sequence/position updates still retire input callbacks in MediaBar;
// they are not a new paint occupant or a reason to replay its content entrance.
const baseX = (row: React.ReactElement<DockPaintRowProps>) => row.props.artWidth + row.props.openerGap;

function RetiringRow({entry, done}: {entry: Retiring; done: (key: string) => void}) {
  const motion = useMotion(), canvas = useContext(VideoExitCanvas);
  const publish = canvas?.publish;
  const alpha = useSharedValue(entry.pose.alpha), x = useSharedValue(entry.pose.x), y = useSharedValue(entry.pose.y), scale = useSharedValue(entry.pose.scale);
  const valid = React.useSyncExternalStore(entry.video?.subscribe ?? noSubscribe, () => entry.video?.paintCurrent() ?? true);
  const artStyle = useAnimatedStyle(() => ({opacity: alpha.value, transform: [{scale: motion.transforms ? scale.value : 1}]}));
  const portal = !!publish && !!entry.video?.canvasBounds;
  useLayoutEffect(() => {
    const bounds = entry.video?.canvasBounds;
    if (!publish || !valid || !bounds) return;
    const art = (entry.node.props as unknown as DockPaintRowProps).art;
    return publish(entry.key, <OverlayPaintProvider mode={OVERLAY_PAINT_INERT}>
      <Animated.View pointerEvents="none" accessible={false} accessibilityElementsHidden importantForAccessibility="no-hide-descendants"
        style={[{position: 'absolute', left: bounds.x, top: bounds.y, width: bounds.width, height: bounds.height}, artStyle]}>{art}</Animated.View>
    </OverlayPaintProvider>);
  }, [entry, valid, publish, artStyle]);
  useEffect(() => {
    const config = {...motion.timing('standard'), duration: motion.duration('standard')};
    alpha.value = withTiming(0, config, finished => {if (finished) runOnJS(done)(entry.key);});
    x.value = motion.transforms ? withTiming(entry.targetX, config) : 0;
    y.value = motion.transforms ? withTiming(-6, config) : 0;
    scale.value = motion.transforms ? withTiming(.9, config) : 1;
    // Disposal fallback only. Each old layer keeps its own retirement clock;
    // later swaps never restart it or retain an ever-growing nested snapshot.
    const timer = setTimeout(() => done(entry.key), config.duration * 2);
    return () => {clearTimeout(timer); cancelAnimation(alpha); cancelAnimation(x); cancelAnimation(y); cancelAnimation(scale);};
  }, [entry, done, motion, alpha, x, y, scale]);
  useEffect(() => () => {void entry.video?.release().catch(() => {});}, [entry]);
  useEffect(() => {if (!valid) done(entry.key);}, [valid, done, entry.key]);
  if (!valid) return null;
  return <OverlayPaintProvider mode={OVERLAY_PAINT_INERT}><View pointerEvents="none" accessible={false} accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={{position: 'absolute', inset: 0}}>
    <Phase.Provider value={{alpha, x, y, scale, hideArt: portal || entry.skipArt}}>{entry.node}</Phase.Provider>
  </View></OverlayPaintProvider>;
}

/** Glass stays outside this presenter. Every interrupted live layer starts its
 * exit at its current paint; earlier exits finish on their original clocks. */
export function DockHandover({occupant, row, allowedTypes, takeVideoExit}: {occupant: DockRenderOccupant; row: React.ReactElement<DockPaintRowProps>;
  allowedTypes: OverlayPaintTypes; takeVideoExit?: (fence: PlaybackFence) => DockVideoExitPaint | undefined}) {
  const motion = useMotion();
  const alpha = useSharedValue(1), x = useSharedValue(0), y = useSharedValue(0), scale = useSharedValue(1);
  const [retiring, setRetiring] = useState<readonly Retiring[]>([]);
  const committed = useRef<{occupant: DockRenderOccupant; row: typeof row; policy: typeof motion} | undefined>(undefined);
  const sequence = useRef(0);
  const done = useCallback((key: string) => setRetiring(current => current.filter(entry => entry.key !== key)), []);
  useLayoutEffect(() => {
    const old = committed.current, changed = old && !sameTarget(old.occupant, occupant);
    let animate = changed;
    if (old && changed) {
      // Sample before cancelling or changing the live shared values. The source
      // tree is the last committed materialised UI, never a component invocation.
      const pose = {alpha: alpha.value, x: x.value, y: y.value, scale: scale.value};
      const delta = baseX(old.row) + pose.x - baseX(row);
      let node = freezeOverlayPaint(old.row as OverlayPaintNode, allowedTypes);
      let video: DockVideoExitPaint | undefined;
      if (old.occupant.mode === 'video') {
        video = takeVideoExit?.(old.occupant.playback.fence);
        if (!node) {void video?.release().catch(() => {}); video = undefined;}
        if (node && video) {
          const art = (node.props as unknown as DockPaintRowProps).art as React.ReactElement<{children?: React.ReactNode}>;
          const captured = React.cloneElement(art as React.ReactElement<{children?: React.ReactNode}>, {}, <>
            <Image accessible={false} source={{uri: video.image.uri}} fadeDuration={0} resizeMode="contain" style={{position: 'absolute', inset: 0}}/>
            {art.props.children}
          </>);
          node = React.cloneElement(node, {art: captured});
        }
      }
      if (node) setRetiring(current => [...current, {key: `dock-swap-${++sequence.current}`, node: node!, pose, targetX: pose.x - delta, skipArt: old.occupant.mode === 'video' && !video, video}]);
      cancelAnimation(alpha); cancelAnimation(x); cancelAnimation(y); cancelAnimation(scale);
      if (node) {
        alpha.value = 0; x.value = motion.transforms ? delta : 0; y.value = motion.transforms ? 6 : 0; scale.value = motion.transforms ? .85 : 1;
      } else {
        // Unsupported paint is refused, never replaced by a fabricated old frame.
        animate = false; alpha.value = 1; x.value = 0; y.value = 0; scale.value = 1;
      }
    }
    if (animate || old && old.policy !== motion) {
      const config = {...motion.timing('standard'), duration: motion.duration('standard')};
      alpha.value = withTiming(1, config);
      x.value = motion.transforms ? withTiming(0, config) : 0;
      y.value = motion.transforms ? withTiming(0, config) : 0;
      scale.value = motion.transforms ? withTiming(1, config) : 1;
    }
    committed.current = {occupant, row, policy: motion};
  }, [occupant, row, allowedTypes, takeVideoExit, motion, alpha, x, y, scale]);
  useEffect(() => () => {cancelAnimation(alpha); cancelAnimation(x); cancelAnimation(y); cancelAnimation(scale);}, [alpha, x, y, scale]);
  return <View pointerEvents="box-none" style={{position: 'absolute', inset: 0}}>
    {retiring.map(entry => <RetiringRow key={entry.key} entry={entry} done={done}/>)}
    <Phase.Provider value={{alpha, x, y, scale}}>{row}</Phase.Provider>
  </View>;
}
