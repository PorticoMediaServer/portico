const controls = 'button,a[href],input,select,textarea,[tabindex]:not([tabindex="-1"])';
const surfaces = '[data-watch-screen]:not([hidden]):not([inert]),[data-watch-music]:not([inert]),[data-watch-audiobook]';
const overlays = '[role="dialog"],[role="menu"],[aria-modal="true"]';

function available(element: HTMLElement | null): element is HTMLElement {
  return !!element?.isConnected && !element.closest('[inert],[hidden],[aria-hidden="true"]')
    && !element.matches(':disabled,[aria-disabled="true"]') && element.getClientRects().length > 0;
}

/** One focus lifetime for the retained full player, shared by video and listening.
 * Route content stays mounted; only the covered route's input/a11y exposure changes. */
export class ExpandedPlaybackFocus {
  private host: HTMLElement | null = null;
  private route: HTMLElement | null = null;
  private routeInert = false;
  private routeHidden: string | null = null;
  private expanded = false;
  private entered = false;
  private opener: HTMLElement | null = null;
  private lastFocus: HTMLElement | null = null;
  private fallback: HTMLElement[] = [];
  private readonly document: Document;
  private readonly remember = () => {
    const active = this.document.activeElement as HTMLElement | null;
    if (!this.expanded && active && active !== this.document.body && !active.closest(surfaces)) this.lastFocus = active;
  };

  constructor(document: Document) {
    this.document = document;
    this.remember();
    document.addEventListener('focusin', this.remember);
  }

  update(host: HTMLElement | null, expanded: boolean) {
    this.host = host;
    if (!expanded) { this.close(); return; }
    if (!this.expanded) {
      this.expanded = true;
      const active = this.document.activeElement as HTMLElement | null;
      this.opener = active && active !== this.document.body && !active.closest(surfaces) ? active : this.lastFocus;
      const region = this.opener?.closest<HTMLElement>('[role="list"],[role="grid"],section,main,[data-playback-route]');
      const siblings = region ? Array.from(region.querySelectorAll<HTMLElement>(controls)) : [];
      const at = this.opener ? siblings.indexOf(this.opener) : -1;
      this.fallback = at < 0 ? [] : [...siblings.slice(at + 1), ...siblings.slice(0, at).reverse()];
      if (region) this.fallback.push(region);
    }
    // Listening may commit its owner on the next layout. Never hide the route
    // before there is a real full surface to receive focus.
    const surface = host && Array.from(host.querySelectorAll<HTMLElement>(surfaces)).find(available);
    if (!surface) return;
    const route = this.document.querySelector<HTMLElement>('[data-playback-route]');
    const active = this.document.activeElement as HTMLElement | null;
    // A source transition can temporarily hide or replace the full surface
    // without collapsing playback. Recover lost focus, retaining the invoker.
    if (!this.entered || active === this.document.body || !available(active)) {
      const validFocus = available(active);
      if (!(validFocus && surface.contains(active)) && !(validFocus && active.closest(overlays) && !route?.contains(active))) {
        const primary = surface.querySelector<HTMLElement>('[data-playback-primary]:not(:disabled)');
        const target = available(primary) ? primary : surface;
        target.focus({preventScroll: true});
      }
      this.entered = true;
    }
    if (route && route !== this.route) {
      this.releaseRoute();
      this.route = route; this.routeInert = route.inert; this.routeHidden = route.getAttribute('aria-hidden');
      // Focus leaves first, before this subtree becomes inaccessible.
      route.inert = true; route.setAttribute('aria-hidden', 'true');
    }
  }

  private releaseRoute() {
    if (!this.route) return;
    this.route.inert = this.routeInert;
    if (this.routeHidden === null) this.route.removeAttribute('aria-hidden');
    else this.route.setAttribute('aria-hidden', this.routeHidden);
    this.route = null;
  }

  private close() {
    if (!this.expanded) return;
    const active = this.document.activeElement as HTMLElement | null;
    const restore = !active || active === this.document.body || !!this.host?.contains(active)
      || !!active.closest('[inert],[hidden],[aria-hidden="true"]');
    this.releaseRoute();
    this.expanded = false; this.entered = false;
    if (restore && !active?.closest(overlays)) {
      const main = this.document.querySelector<HTMLElement>('[data-playback-route] main');
      const target = [this.opener, ...this.fallback, main].find(available);
      if (target) {
        if (!target.matches(controls) && !target.hasAttribute('tabindex')) target.tabIndex = -1;
        target.focus({preventScroll: true});
      }
    }
    this.opener = null; this.fallback = [];
    this.remember();
  }

  dispose() {
    this.close();
    this.document.removeEventListener('focusin', this.remember);
  }
}
