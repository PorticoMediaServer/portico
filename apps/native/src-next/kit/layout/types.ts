import type {StyleProp, ViewStyle} from 'react-native';
import type {AnimatedStyle} from 'react-native-reanimated';
import type {FactPart} from '../../../../../packages/client-core/src/presentation/kit-model';

export type LayoutVariant = 'desktop' | 'phone-app' | 'phone-web' | 'tv';
export type HeroCandidate = Readonly<{id: string; title: string; long?: boolean; reason?: string; overview?: string; facts?: readonly FactPart[]; thumbnail?: unknown}>;
export type CardSelection = Readonly<{active: boolean; selected: boolean; onToggle: () => void; onEnter: () => void}>;
export type LayoutSelection = Readonly<{active: boolean; isSelected: (id: string) => boolean; onToggle: (id: string) => void; onEnter: (id: string) => void}>;
export type LayoutItemContext = Readonly<{index: number; id: string; focusId?: string; testID?: string; waiting?: boolean; selection?: CardSelection; onFocus: () => void; onBlur: () => void}>;
export type ArtworkRenderContext = Readonly<{onReady: () => void; onError: (error?: unknown) => void; visible: boolean; active: boolean; foregroundStyle: StyleProp<AnimatedStyle<ViewStyle>>}>;
export type ThumbnailRenderContext = Readonly<{index: number; active: boolean; visible: boolean}>;

export function itemSelection(selection: LayoutSelection | undefined, id: string): CardSelection | undefined {
  return selection && {active: selection.active, selected: selection.isSelected(id), onToggle: () => selection.onToggle(id), onEnter: () => selection.onEnter(id)};
}
