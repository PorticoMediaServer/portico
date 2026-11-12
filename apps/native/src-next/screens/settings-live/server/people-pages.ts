import {SettingsPeopleClient, type SettingsPersonRow} from '../../../../../../packages/client-core/src/settings-people';
import {PeopleInvitationsClient, type ServerInvitation} from '../../../../../../packages/client-core/src/direct-account';
import type {NativeScreenLifetime} from '../lifetime';
import {createTVServerReader} from './tv-server-reader';

type Section = 'members' | 'invitations';
type Row = SettingsPersonRow | ServerInvitation;
export type PeopleSection<T> = {items: readonly T[]; cursor: string; pending: boolean; error?: unknown; loaded: boolean};
/** The TV People page's groups. The rebuilt server answers each in one read (no cursor), and keeps no server-wide list
 * of devices (only each person's own), so the TV shows accounts and waiting invitations. */
export type PeoplePages = {members: PeopleSection<SettingsPersonRow>; invitations: PeopleSection<ServerInvitation>};
const initial = (): PeoplePages => ({members: {items: [], cursor: '', pending: false, loaded: false}, invitations: {items: [], cursor: '', pending: false, loaded: false}});
const everyone = {search: '', role: 'all', status: 'all', sort: 'person', direction: 'asc'} as const;

/** One instance owns one panel opening and its exact authority lifetime. Every read goes through the TV server reader:
 * GET only, for the owner or an administrator on their primary profile, checked before each request. */
export class PeoplePagesController {
  private state = initial();
  private abort = new AbortController();
  private generation = 0;
  private disposed = false;
  private readonly people: SettingsPeopleClient;
  private readonly invitations: PeopleInvitationsClient;
  constructor(context: Parameters<typeof createTVServerReader>[0], private lifetime: NativeScreenLifetime, private changed: (state: PeoplePages) => void) {
    const reader = createTVServerReader(context, lifetime), api = {request: reader.request};
    this.people = new SettingsPeopleClient(api, () => !this.disposed && this.lifetime.current());
    this.invitations = new PeopleInvitationsClient(api);
  }
  start(): void {this.refresh();}
  /** Internal refresh seam. The current panel has no visible refresh emitter. */
  refresh(): void {
    if (this.disposed || !this.lifetime.current()) return;
    this.abort.abort(); this.abort = new AbortController(); this.generation++;
    this.state = initial(); this.changed(this.state);
    for (const section of ['members', 'invitations'] as const) void this.load(section);
  }
  dispose(): void {if (this.disposed) return; this.disposed = true; this.generation++; this.abort.abort(); this.people.dispose();}
  currentGeneration(): number {return this.generation;}
  /** Reads a group once; a read already in flight is shared, and a loaded group is not read again until refresh. A failed
   * read keeps the rows it had and can be tried again. */
  async load(section: Section, _expectedCursor?: string, expectedGeneration?: number): Promise<void> {
    const current = this.state[section], abort = this.abort, generation = this.generation;
    if (this.disposed || abort.signal.aborted || !this.lifetime.current() || current.pending || current.loaded || expectedGeneration !== undefined && expectedGeneration !== generation) return;
    this.update(section, {...current, pending: true, error: undefined});
    try {
      const items: readonly Row[] = section === 'members' ? (await this.people.read(everyone, '', abort.signal)).items
        : (await this.invitations.list({state: 'pending', sort: 'sent', direction: 'desc'}, abort.signal)).items;
      if (this.disposed || abort.signal.aborted || generation !== this.generation || !this.lifetime.current()) return;
      this.update(section, {items, cursor: '', pending: false, loaded: true});
    } catch (error) {
      if (!this.disposed && !abort.signal.aborted && generation === this.generation && this.lifetime.current()) this.update(section, {...current, pending: false, error: error ?? new Error('native_tv_people_page_failed')});
    }
  }
  private update(section: Section, value: PeopleSection<Row>): void {
    this.state = {...this.state, [section]: value} as PeoplePages;
    this.changed(this.state);
  }
}
