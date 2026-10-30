import type {PersonalSavedService, SavedDefinition} from '@core/personal-saved.ts';

/** One dialog owns the service. An uncertain create is retried byte-for-byte,
 * rather than discarded by reselecting the directory and creating a duplicate. */
export async function saveViewDraft(service: PersonalSavedService, definition: SavedDefinition, name: string, isCurrent: () => boolean): Promise<string> {
  const current = () => {if (!isCurrent()) throw new DOMException('Saved view retired', 'AbortError');};
  current();
  if (service.getSnapshot().retryPending) {
    if (!await service.retry()) throw service.getSnapshot().mutationError;
  } else {
    if (service.getSnapshot().route?.view !== 'views') await service.select({view: 'views'});
    else if (service.getSnapshot().error || !service.getSnapshot().viewerFence) await service.refresh();
    current();
    if (service.getSnapshot().error) throw service.getSnapshot().error;
    if (!await service.mutate({action: 'create', kind: 'view', name: name.trim(), definition})) throw service.getSnapshot().mutationError;
  }
  current();
  const id = service.getSnapshot().result?.resourceId;
  if (!id) throw Object.assign(new Error('invalid_saved'), {code: 'invalid_saved'});
  return id;
}
