import type {CSSProperties, FocusEventHandler, ReactNode, Ref} from 'react';
import type {CardTextModel, FactPart} from '@portico/client-core/kit-model';
import type {ArtworkSource, AmbientArtSource} from '../artwork';
import type {CardInteractionSource, CardInteractionAdmission} from './interaction-admission';

export type CardContent = Pick<CardTextModel, 'title' | 'caption'> & Partial<Pick<CardTextModel, 'detail' | 'progress'>>;
/** Authored visible text only; runs must concatenate exactly to the canonical text. */
export type CardTextRun = Readonly<{text: string; emphasis?: 'match' | 'secondary'}>;
/** Optional runs keyed by canonical FactPart.id; facts still own order and roles. */
export type CardFactPresentation = Readonly<Record<string, readonly CardTextRun[]>>;
export type CardTextPresentation = Readonly<{
  title?: readonly CardTextRun[];
  caption?: CardFactPresentation;
  /** Row indices follow extraCaption; omitted rows retain plain text. */
  extraCaption?: readonly (CardFactPresentation | undefined)[];
}>;
export type CardSelectionIntent = Readonly<{range: boolean; source: 'pointer' | 'keyboard' | 'long-press';
  /** The card element a long press started on, so its owner can anchor the drawn menu. */ anchor?: HTMLElement}>;
/** The shared selection family supplies the actual SelectionCircle; cards positions it. */
export type CardSelection = Readonly<{
  /** Original producer/row identity; unchanged by ordinary preview publication. */
  interaction?: CardInteractionSource;
  /** Bound only after the actual CardFrame/list DOM node commits. */
  admission?: CardInteractionAdmission;
  active: boolean;
  checked: boolean;
  preview?: boolean;
  /** Paint-only range hover; the owner retains registered membership and anchor. */
  onPreview?: (enabled: boolean) => void;
  /** Localized range hint supplied by the selection owner when an anchor exists. */
  hint?: Readonly<{text: string; shortcut?: string}>;
  /** Optional decorative SelectionCircle; CardFrame owns the single interactive target. */
  control?: ReactNode;
  onToggle: (intent: CardSelectionIntent) => void;
  onExit: () => void;
}>;
export type CardProps = Readonly<{
  model: CardContent;
  onNavigate: () => void;
  onPlay?: () => void;
  playLabel?: string;
  onMore?: (anchor: HTMLButtonElement) => void;
  moreExpanded?: boolean;
  onNotInterested?: () => void;
  disabled?: boolean;
  selection?: CardSelection;
  watched?: boolean;
  /** The localized completion state when a card says Finished instead of Watched. */
  watchedLabel?: string;
  isNew?: boolean;
  next?: boolean;
  /** Additional authored credit/reason rows, retaining semantic fact parts. */
  extraCaption?: readonly (readonly FactPart[])[];
  /** Inline presentation for CardCaption; canonical strings and accessible names stay authoritative. */
  textPresentation?: CardTextPresentation;
  className?: string;
  style?: CSSProperties;
  primaryRef?: Ref<HTMLButtonElement>;
  onFocus?: FocusEventHandler<HTMLButtonElement>;
  onBlur?: FocusEventHandler<HTMLButtonElement>;
  visible?: boolean;
  context?: 'shelf' | 'grid' | 'search' | 'detail';
  density?: 'shelf' | 'grid';
}>;
export type MediaCardProps = CardProps & Readonly<{
  artwork?: ArtworkSource;
  arrivalDurationMs?: number;
  fallback?: ReactNode;
  position?: string;
  ambient?: AmbientArtSource;
  palette?: Readonly<{first: string; second: string}>;
}>;
export type CardFacts = readonly FactPart[];
