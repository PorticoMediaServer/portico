import type {InboxService, InboxSnapshot} from '@core/inbox.ts';

/** A mounted notification screen borrows the root's InboxService. It never
 * disposes it, and a retained render may not borrow that service's next page. */
export class NotificationScreenAdmission {
  private live = true;
  private started = false;
  private navigation?: {live: boolean};
  constructor(readonly service: InboxService, private readonly isCurrent: () => boolean) {}
  private originalCurrent = () => {try {return this.isCurrent();} catch {return false;}};
  current = () => this.live && this.originalCurrent();
  owns = (snapshot: InboxSnapshot) => this.current() && this.service.getSnapshot() === snapshot;
  run = (snapshot: InboxSnapshot, command: () => void) => {
    if (!this.owns(snapshot)) return false;
    this.started = true;
    command();return true;
  };
  /** A user command before the passive load already chose this view. Otherwise
   * load only the exact drawn audience/view, never the service's later defaults. */
  initialize = (snapshot: InboxSnapshot) => {
    if (this.started) return;
    this.run(snapshot, () => {void this.service.load(snapshot.audience, snapshot.view);});
  };
  /** Consume the drawn opening before a navigation command. Its optional
   * mark-read publication cannot admit a second callback or a newer target. */
  leave = (snapshot: InboxSnapshot, command: (current: () => boolean) => void) => {
    if (!this.owns(snapshot)) return false;
    this.started = true;this.live = false;
    const navigation = this.navigation = {live: true};
    command(() => this.navigation === navigation && navigation.live && this.originalCurrent());
    return true;
  };
  retire = () => {this.live = false;if (this.navigation) this.navigation.live = false;this.navigation = undefined;};
}
