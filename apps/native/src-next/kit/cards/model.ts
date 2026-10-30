export type CardSelection = Readonly<{
  active: boolean;
  selected: boolean;
  onToggle: () => void;
  onEnter: () => void;
}>;

/** Callbacks retain the caller's navigation and server-selected playback identities. */
export type CardActions = Readonly<{
  onMore?: () => void;
  selection?: CardSelection;
}> & (
  | Readonly<{primaryAction?: 'navigate'; onNavigate: () => void; onPlay?: () => void}>
  | Readonly<{primaryAction: 'play'; onPlay: () => void; onNavigate?: () => void}>
);

export function activateCard(actions: CardActions): void {
  if (actions.selection?.active) actions.selection.onToggle();
  else if (actions.primaryAction === 'play') actions.onPlay();
  else actions.onNavigate();
}

/** Q6: an active touch selection toggles; every other hold opens the caller's menu. */
export function holdCard(actions: CardActions, isTV: boolean): void {
  if (!isTV && actions.selection?.active) actions.selection.onToggle();
  else actions.onMore?.();
}

export function cardProgress(value: number | undefined): number | undefined {
  return value === undefined || !Number.isFinite(value) ? undefined : Math.max(0, Math.min(1, value));
}

/** The same localized presentation facts as the face, without repeated facts or a synopsis. */
export function cardAccessibilityLabel(title: string, caption: readonly {text: string}[] = [], detail: readonly {text: string}[] = [], states: readonly (string | undefined)[] = []): string {
  const parts = [title, ...caption.map(part => part.text), ...detail.map(part => part.text), ...states]
    .map(part => part?.trim()).filter((part): part is string => !!part);
  return [...new Set(parts)].join(', ');
}
