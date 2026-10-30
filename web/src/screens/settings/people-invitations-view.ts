import type {ServerInvitation} from '@core/direct-account.ts';
import type {TableQuery} from '../../kit/settings/table-model';

export type InvitationPage = Readonly<{items: readonly ServerInvitation[]; nextCursor?: string; matchedCount: number; totalCount: number}>;
export type InvitationScope = Readonly<{query: TableQuery; life: AbortController}>;
export type ResidentInvitations = Readonly<{scope: InvitationScope; value: InvitationPage; life: AbortController}>;
export type InvitationRead = Readonly<{scope: InvitationScope; before: ResidentInvitations | undefined; life: AbortController}>;
export type InvitationOpening = {
  readonly page: ResidentInvitations;
  readonly row: ServerInvitation;
  readonly life: AbortController;
  readonly focus: {current: HTMLElement | null};
  committed: boolean;
  operationId?: string;
  running?: InvitationRevoke;
};
export type InvitationRevoke = Readonly<{opening: InvitationOpening; operationId: string}>;

/** Only the committed component owns this view. Tokens refer to their original
 * query/page/row/opening; none can acquire a replacement through a latest ref. */
export class InvitationsView {
  private active = true;
  private scope?: InvitationScope;
  private page?: ResidentInvitations;
  private read?: InvitationRead;
  private opening?: InvitationOpening;
  constructor(private readonly authorized: () => boolean) {}
  current() {try {return this.active && this.authorized();} catch {return false;}}
  scopeCurrent(scope: InvitationScope | undefined): scope is InvitationScope {
    return !!scope && this.current() && this.scope === scope && !scope.life.signal.aborted;
  }
  replace(query: TableQuery): InvitationScope {
    this.clearScope();
    const captured = Object.freeze({search: query.search, filters: Object.freeze({...query.filters}), sort: Object.freeze({...query.sort})});
    const scope = Object.freeze({query: captured, life: new AbortController()});
    if (this.current()) this.scope = scope; else scope.life.abort();
    return scope;
  }
  retireScope(scope: InvitationScope) {if (this.scope === scope) this.clearScope();}
  private clearOpening() {
    if (this.opening) {this.opening.committed = false; this.opening.life.abort(); this.opening = undefined;}
  }
  private clearPage() {this.clearOpening();this.page?.life.abort();this.page = undefined;}
  private clearScope() {
    this.clearPage();this.read?.life.abort();this.read = undefined;
    this.scope?.life.abort();this.scope = undefined;
  }
  retire() {this.active = false;this.clearScope();}
  pageCurrent(page: ResidentInvitations | undefined): page is ResidentInvitations {
    return !!page && this.scopeCurrent(page.scope) && this.page === page && !page.life.signal.aborted;
  }
  rowCurrent(page: ResidentInvitations, row: ServerInvitation) {
    return this.pageCurrent(page) && page.value.items.includes(row);
  }
  beginRead(scope: InvitationScope, before?: ResidentInvitations): InvitationRead | undefined {
    if (!this.scopeCurrent(scope) || this.read || (before ? !this.pageCurrent(before) : !!this.page)) return;
    const ticket = Object.freeze({scope, before, life: new AbortController()});
    this.read = ticket;return ticket;
  }
  readCurrent(ticket: InvitationRead) {
    return this.scopeCurrent(ticket.scope) && this.read === ticket && !ticket.life.signal.aborted
      && (ticket.before ? this.pageCurrent(ticket.before) : !this.page);
  }
  acceptRead(ticket: InvitationRead, next: InvitationPage): ResidentInvitations | undefined {
    if (!this.readCurrent(ticket)) return;
    const before = ticket.before?.value, ids = new Set(before?.items.map(row => row.id));
    const incoming = next.items.filter(row => !ids.has(row.id)).map(row => {
      const captured = {...row, allowedLibraries: [...row.allowedLibraries]};
      Object.freeze(captured.allowedLibraries);return Object.freeze(captured);
    });
    const value = Object.freeze({...next, items: Object.freeze([...(before?.items ?? []), ...incoming])});
    this.read = undefined;ticket.life.abort();this.clearPage();
    const page = Object.freeze({scope: ticket.scope, value, life: new AbortController()});
    this.page = page;return page;
  }
  refuseRead(ticket: InvitationRead) {
    if (!this.readCurrent(ticket)) return false;
    this.read = undefined;ticket.life.abort();return true;
  }
  open(page: ResidentInvitations, row: ServerInvitation, opener: HTMLElement | null): InvitationOpening | undefined {
    if (!this.rowCurrent(page, row) || (row.state !== 'pending' && row.state !== 'expired') || this.opening?.running) return;
    this.clearOpening();
    const opening: InvitationOpening = {page, row, life: new AbortController(), focus: {current: opener}, committed: false};
    this.opening = opening;return opening;
  }
  openingCurrent(opening: InvitationOpening | undefined): opening is InvitationOpening {
    return !!opening && this.opening === opening && !opening.life.signal.aborted && this.rowCurrent(opening.page, opening.row);
  }
  commitOpening(opening: InvitationOpening) {
    if (this.openingCurrent(opening)) opening.committed = true;
    return () => {opening.committed = false;};
  }
  cancel(opening: InvitationOpening) {
    if (!this.openingCurrent(opening) || opening.running) return false;
    this.clearOpening();return true;
  }
  beginRevoke(opening: InvitationOpening, id: () => string): InvitationRevoke | undefined {
    if (!this.openingCurrent(opening) || !opening.committed || opening.running) return;
    // Allocate before claiming: entropy failure must not leave a stuck gate.
    const operationId = opening.operationId ?? id();
    const ticket = Object.freeze({opening, operationId});
    opening.operationId = operationId;opening.running = ticket;return ticket;
  }
  revokeCurrent(ticket: InvitationRevoke) {
    return this.openingCurrent(ticket.opening) && ticket.opening.committed && ticket.opening.running === ticket;
  }
  settleRevoke(ticket: InvitationRevoke, accepted: boolean) {
    if (!this.revokeCurrent(ticket)) return false;
    ticket.opening.running = undefined;
    if (accepted) this.clearOpening();
    // A refusal/uncertain result retains the exact row, CAS and operation id.
    return true;
  }
}
