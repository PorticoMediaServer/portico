import type {RoomSessionService, RoomSessionSnapshot, RoomEnterLifetime} from '../../../../../packages/client-core/src/room-session';
import type {Room, GroupControl} from '../../../../../packages/client-core/src/room-protocol';
import {NativeScreenLifetime, NativeScreenStore, screenScopeEnded} from './lifetime';

export type NativeTogetherService = Pick<RoomSessionService, 'getSnapshot' | 'subscribe' | 'refreshDirectory' | 'open' | 'create' | 'join' | 'dismiss' | 'serverNow' | 'refresh' | 'transport' | 'invite' | 'makeHost' | 'queueRemove' | 'leave' | 'end' | 'clearJoinFailure'>;

/** A borrowed service is already bound to the same authenticated player/follower lifetime. */
export class NativeTogetherController extends NativeScreenStore<RoomSessionSnapshot> {
  private off: () => void;
  private disposed = false;
  readonly lifetime: NativeScreenLifetime;
  private service: NativeTogetherService;
  constructor(lifetime: NativeScreenLifetime, service: NativeTogetherService) {
    super(service.getSnapshot());
    this.lifetime = lifetime; this.service = service;
    this.off = service.subscribe(() => {if (!this.disposed && lifetime.current()) this.publish(service.getSnapshot());});
  }
  private async action<T>(work: () => Promise<T>): Promise<T> {this.lifetime.assert(); if (this.disposed) throw screenScopeEnded(); const value = await work(); this.lifetime.assert(); if (this.disposed) throw screenScopeEnded(); return value;}
  refresh = () => this.action(() => this.service.refreshDirectory());
  open = (id: string, lifetime?: RoomEnterLifetime) => this.action(() => this.service.open(id, lifetime));
  create = (name: string, displayName: string, control: GroupControl, lifetime?: RoomEnterLifetime) => this.action(() => this.service.create({name, displayName, control}, lifetime));
  join = (code: string, displayName: string, lifetime?: RoomEnterLifetime) => this.action(() => this.service.join(code, displayName, lifetime));
  currentGroup = (original: Pick<Room, 'id'> & {you: Pick<Room['you'], 'memberId'>}): Room | undefined => {
    const value = this.getSnapshot();
    return !this.disposed && this.lifetime.current() && (value.phase === 'live' || value.phase === 'reconnecting')
      && value.group?.id === original.id && value.group.you.memberId === original.you.memberId ? value.group : undefined;
  };
  serverNow = () => this.service.serverNow();
  refreshRoom = () => this.action(() => this.service.refresh());
  transport = (...args: Parameters<RoomSessionService['transport']>) => this.action(() => this.service.transport(...args));
  invite = () => this.action(() => this.service.invite());
  makeHost = (memberId: string) => this.action(() => this.service.makeHost(memberId));
  queueRemove = (entryId: string) => this.action(() => this.service.queueRemove(entryId));
  leave = () => this.action(() => this.service.leave());
  end = () => this.action(() => this.service.end());
  /** A code edit or closed form retires the failed join's field error; never throws for a retired screen. */
  clearJoinFailure(): void {if (!this.disposed && this.lifetime.current() && this.service.getSnapshot().joinFailure) this.service.clearJoinFailure();}
  dismiss(): void {this.lifetime.assert(); if (this.disposed) throw screenScopeEnded(); this.service.dismiss();}
  dispose(): void {if (this.disposed) return; this.disposed = true; this.off(); this.clearListeners();}
}
