import type {ShowWorkspaceRestore} from '../../../../../packages/client-core/src/show-workspace';

export type NativeTitleTab = 'episodes' | 'related' | 'details';
export type NativeTitleAnchor = Readonly<{itemId: string | null; offset: number}>;
export type NativeTitlePlace = Readonly<{
  tab: NativeTitleTab;
  seasonId: string | null;
  group: string | null;
  hideWatched: boolean;
  anchor: NativeTitleAnchor;
}>;
export type NativeTitleRestore = Readonly<{
  revision: number;
  place: NativeTitlePlace;
  workspace: ShowWorkspaceRestore | null;
}>;
const emptyAnchor: NativeTitleAnchor = Object.freeze({itemId: null, offset: 0});

/** Page-local state. Presentation changes never select a route or recreate its services. */
export class NativeTitleMemory {
  private anchors = new Map<string, NativeTitleAnchor>();
  private revision = 0;
  private place: NativeTitlePlace;
  private restore: NativeTitleRestore | undefined;
  private boundsSettled = false;

  constructor(show: boolean) {
    this.place = Object.freeze({tab: show ? 'episodes' : 'related', seasonId: null, group: null, hideWatched: false, anchor: emptyAnchor});
  }
  getSnapshot = (): NativeTitlePlace => this.place;
  private key(place: NativeTitlePlace): string {return JSON.stringify([place.tab, place.seasonId, place.group, place.hideWatched]);}
  select(change: Partial<Pick<NativeTitlePlace, 'tab' | 'seasonId' | 'group' | 'hideWatched'>>): NativeTitlePlace {
    this.anchors.set(this.key(this.place), this.place.anchor);
    const next = {...this.place, ...change};
    if (change.seasonId) next.group = null;
    if (change.group) next.seasonId = null;
    this.place = Object.freeze({...next, anchor: this.anchors.get(this.key(next)) ?? emptyAnchor});
    this.restore = undefined;
    return this.place;
  }
  remember(anchor: NativeTitleAnchor): NativeTitlePlace {
    if (anchor.itemId !== null && (!anchor.itemId || /[\x00-\x1f]/.test(anchor.itemId)) || !Number.isFinite(anchor.offset) || anchor.offset < 0) throw new Error('invalid_title_anchor');
    this.place = Object.freeze({...this.place, anchor: Object.freeze({...anchor})});
    this.anchors.set(this.key(this.place), this.place.anchor);
    return this.place;
  }
  /** Frame 9: capture, settle bounds, restore anchor, then reopen panel/focus. */
  capture(workspace: ShowWorkspaceRestore | null): NativeTitleRestore {
    this.boundsSettled = false;
    return this.restore = Object.freeze({revision: ++this.revision, place: this.place, workspace});
  }
  settled(token: NativeTitleRestore): NativeTitleAnchor | undefined {
    if (this.restore !== token || this.place !== token.place) return;
    this.boundsSettled = true;
    return token.place.anchor;
  }
  restored(token: NativeTitleRestore): boolean {
    if (this.restore !== token || this.place !== token.place || !this.boundsSettled) return false;
    this.restore = undefined;
    return true;
  }
}
