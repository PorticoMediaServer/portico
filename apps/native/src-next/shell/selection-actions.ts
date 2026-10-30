import type {SelectionAction as CoreAction, SelectionActionReason, SelectionActionRules} from '../../../../packages/client-core/src/selection-model/index';
import type {SelectionAction as NativeAction, SelectionActionOffer} from '../kit/selection';

const actions = {
  play: 'play', playNext: 'play-next', playLater: 'play-later', playlist: 'playlist',
  myList: 'my-list', watched: 'mark-watched', unwatched: 'mark-unwatched',
  collection: 'collection', favorite: 'favorite', removeMyList: 'remove-my-list',
  download: 'download', edit: 'edit', refresh: 'refresh', delete: 'delete',
} as const satisfies Partial<Record<NativeAction, CoreAction>>;

/** Renderer ids only. Authorization and execution remain in the shared controller. */
export function nativeSelectionCommand(action: NativeAction): CoreAction | undefined {
  return action in actions ? actions[action as keyof typeof actions] : undefined;
}

export function nativeSelectionOffers(rules: SelectionActionRules, reason: (reason: SelectionActionReason) => string): readonly SelectionActionOffer[] {
  const offers: SelectionActionOffer[] = [];
  for (const [id, action] of Object.entries(actions) as [keyof typeof actions, CoreAction][]) {
    const rule = rules[action];
    if (rule.state === 'hidden') continue;
    offers.push({id, ...(rule.state === 'disabled' ? {disabled: true} : {}), ...('reason' in rule ? {reason: reason(rule.reason)} : {})});
  }
  const queued = offers.filter(offer => offer.id === 'playNext' || offer.id === 'playLater');
  const sortUnavailable = ['play-next', 'play-later'].every(action => {
    const rule = rules[action as CoreAction];
    return rule.state === 'disabled' && rule.reason === 'unsupported-sort';
  });
  if (queued.length) offers.push({id: 'queue', ...(queued.every(offer => offer.disabled) && !sortUnavailable ? {disabled: true} : {}),
    ...(sortUnavailable ? {reason: reason('unsupported-sort')} : {})});
  if (offers.some(offer => ['watched', 'unwatched', 'collection', 'favorite', 'download', 'removeMyList', 'edit', 'refresh', 'delete'].includes(offer.id))) offers.push({id: 'more'});
  return offers;
}
