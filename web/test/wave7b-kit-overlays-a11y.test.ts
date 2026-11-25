import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {componentModule} from './helpers/component-harness.mjs';
import {all, createRenderer, tree} from './helpers/interaction-harness.mjs';
import {isPermittedOutsideOverlay} from '../src/kit/overlays/shared.ts';

const css = {default: new Proxy({}, {get: (_target, key) => String(key)})};

/** Extends the interaction harness react mock with real context propagation and useId. */
function makeReact(h: ReturnType<typeof createRenderer>) {
  let idCounter = 0;
  return {
    ...h.react,
    useId: () => `id-${++idCounter}`,
    createContext: <T,>(defaultValue: T) => {
      const ctx = {
        value: defaultValue,
        Provider({value, children}: {value: T; children: unknown}) {
          ctx.value = value;
          return children;
        },
      };
      return ctx;
    },
    useContext: <T,>(ctx: {value: T}) => ctx.value,
  };
}

async function withFakeDom<T>(run: (env: {observers: Array<{callback: () => void; target: unknown}>}) => Promise<T>): Promise<T> {
  const g = globalThis as Record<string, unknown>;
  const saved = {
    window: g.window,
    document: g.document,
    HTMLElement: g.HTMLElement,
    MutationObserver: g.MutationObserver,
    ResizeObserver: g.ResizeObserver,
  };
  const observers: Array<{callback: () => void; target: unknown}> = [];
  const noop = () => {};
  g.HTMLElement = class FakeHTMLElement {};
  g.document = {
    hidden: false,
    body: {},
    activeElement: null,
    addEventListener: noop,
    removeEventListener: noop,
  };
  g.window = {
    innerHeight: 844,
    visualViewport: {height: 844, offsetTop: 0, addEventListener: noop, removeEventListener: noop},
    addEventListener: noop,
    removeEventListener: noop,
    setTimeout: () => 1,
    clearTimeout: noop,
  };
  g.MutationObserver = class FakeMutationObserver {
    private readonly callback: () => void;
    constructor(callback: () => void) { this.callback = callback; }
    observe(target: unknown) { observers.push({callback: this.callback, target}); }
    disconnect() {}
  };
  g.ResizeObserver = class FakeResizeObserver {
    observe() {}
    disconnect() {}
  };
  try {
    return await run({observers});
  } finally {
    for (const [key, value] of Object.entries(saved)) {
      if (value === undefined) delete g[key];
      else g[key] = value;
    }
  }
}

test('Task 1: phone Menu inside BottomSheet renders Tab-focusable button rows without orphaned menuitem roles, and closing Popover/SidePanel carry inert', async () => {
  await withFakeDom(async () => {
    const h = createRenderer();
    const react = makeReact(h);
    let phone = true;
    let present = true;
    const portalTarget = {tag: 'OVERLAY_ROOT'};
    const RadixPopover = {
      Root: ({children}: {children: unknown}) => children,
      Trigger: ({children}: {children: unknown}) => children,
      Anchor: () => null,
      Portal: ({children}: {children: unknown}) => children,
      Content: 'RadixPopoverContent',
    };
    const mod = await componentModule(new URL('../src/kit/overlays/Popover.tsx', import.meta.url), {
      react,
      'radix-ui': {Popover: RadixPopover},
      './portal-target': {useOverlayPortalTarget: () => portalTarget},
      '../Icon': {Icon: 'Icon'},
      './BottomSheet': {BottomSheet: 'BottomSheet'},
      './shared': {
        classes: (...values: (string | undefined | false)[]) => values.filter(Boolean).join(' '),
        focusElement() {},
        focusable: () => [],
        isPermittedOutsideOverlay: () => false,
        useDismissReason: () => ({current: 'action'}),
        useExclusiveAnchor: () => false,
        useInDialog: () => false,
        useOverlayKeys() {},
        usePhone: () => phone,
        usePresence: () => present,
        useSurfaceFocus: () => ({suppressRestore: {current: false}, contentRef() {}, onOpenAutoFocus() {}, onCloseAutoFocus() {}}),
        useSurfaceMotion: () => () => {},
      },
      './menu-focus': {menuInitialFocus: () => null, navigateMenuArrow: () => false, recoverMenuChildFocus() {}},
      './overlays.module.css': css,
    });

    // Phone responsive Menu inside BottomSheet: items are Tab-focusable native buttons (no role="menuitem", no tabIndex=-1).
    const phoneTree = tree(h.render(() => react.createElement(mod.Menu, {
      open: true,
      onOpenChange() {},
      title: 'Actions',
      anchorRef: {current: null},
      items: [
        {id: 'a', label: 'Play next', onSelect() {}},
        {id: 'b', label: 'Favorite', selected: true, onSelect() {}},
      ],
    })));
    assert.equal(all(phoneTree, 'BottomSheet').length, 1);
    const phoneButtons = all(phoneTree, 'button');
    assert.equal(phoneButtons.length, 2);
    for (const btn of phoneButtons) {
      assert.equal(btn.props.role, undefined, 'phone sheet rows must not emit orphaned menuitem roles');
      assert.equal(btn.props.tabIndex, undefined, 'phone sheet rows must remain in natural Tab order');
    }
    assert.equal(phoneButtons[1]!.props['aria-pressed'], true, 'selected phone sheet button exposes aria-pressed');

    // Desktop Menu retains role="menuitem"/"menuitemradio" and roving tabIndex={-1}.
    phone = false;
    const desktopTree = tree(h.render(() => react.createElement(mod.Menu, {
      open: true,
      onOpenChange() {},
      title: 'Actions',
      anchorRef: {current: null},
      items: [
        {id: 'a', label: 'Play next', onSelect() {}},
        {id: 'b', label: 'Favorite', selected: true, onSelect() {}},
      ],
    })));
    const desktopButtons = all(desktopTree, 'button');
    assert.equal(desktopButtons.length, 2);
    assert.equal(desktopButtons[0]!.props.role, 'menuitem');
    assert.equal(desktopButtons[0]!.props.tabIndex, -1);
    assert.equal(desktopButtons[1]!.props.role, 'menuitemradio');
    assert.equal(desktopButtons[1]!.props['aria-checked'], true);

    // Desktop Popover when closing (open=false, present=true) sets inert={true}.
    present = true;
    const closingPopover = tree(h.render(() => react.createElement(mod.Popover, {
      open: false,
      onOpenChange() {},
      title: 'Filter',
      anchorRef: {current: null},
    }, 'Body')));
    const content = all(closingPopover, 'RadixPopoverContent');
    assert.equal(content.length, 1);
    assert.equal(content[0]!.props.inert, true, 'closing Popover content must be inert');

    // SidePanel when closing sets inert={true}, and when open leaves inert undefined.
    const portals: Array<{node: unknown; container: unknown}> = [];
    const sidePanelMod = await componentModule(new URL('../src/kit/overlays/SidePanel.tsx', import.meta.url), {
      react,
      'react-dom': {createPortal: (node: unknown, container: unknown) => { portals.push({node, container}); return node; }},
      '@i18n': {defaultI18n: {t: (key: string) => key}},
      './portal-target': {useOverlayPortalTarget: () => portalTarget},
      '../controls/Button': {Button: 'Button'},
      './shared': {
        classes: (...values: (string | undefined | false)[]) => values.filter(Boolean).join(' '),
        focusElement() {},
        focusable: () => [],
        isPermittedOutsideOverlay: () => false,
        useOverlayKeys: () => () => true,
        usePresence: () => true,
        useSurfaceMotion: () => () => {},
      },
      './overlays.module.css': css,
    });
    const closingPanel = tree(h.render(() => react.createElement(sidePanelMod.SidePanel, {open: false, onOpenChange() {}, title: 'Info'}, 'Body')));
    assert.equal(all(closingPanel, 'aside')[0]!.props.inert, true, 'closing SidePanel must be inert');
    const openPanel = tree(h.render(() => react.createElement(sidePanelMod.SidePanel, {open: true, onOpenChange() {}, title: 'Info'}, 'Body')));
    assert.equal(all(openPanel, 'aside')[0]!.props.inert, undefined, 'open SidePanel must not be inert');
  });
});

test('Task 2: BottomSheet enables body drag/scroll at half-detent and guards onInteractOutside / onPointerDownOutside for toasts and non-dismissible sheets', async () => {
  await withFakeDom(async () => {
    const h = createRenderer();
    const react = makeReact(h);
    const portalTarget = {tag: 'OVERLAY_ROOT'};
    const RadixDialog = {
      Root: ({children}: {children: unknown}) => children,
      Trigger: ({children}: {children: unknown}) => children,
      Portal: ({children}: {children: unknown}) => children,
      Overlay: 'RadixOverlay',
      Content: 'RadixContent',
      Title: 'RadixTitle',
      Description: 'RadixDescription',
    };
    let toastTarget = false;
    const mod = await componentModule(new URL('../src/kit/overlays/BottomSheet.tsx', import.meta.url), {
      react,
      'radix-ui': {Dialog: RadixDialog},
      '@i18n': {defaultI18n: {t: (key: string) => key}},
      '@design': {springs: {settle: {stiffness: 300, damping: 30, mass: 1}}},
      './portal-target': {useOverlayPortalTarget: () => portalTarget},
      '../controls/Button': {Button: 'Button'},
      './model': {
        releaseSheet: () => ({height: 744, index: 1}),
        resistedHeight: (v: number) => v,
        sheetDetents: () => [{height: 420, index: 0}, {height: 744, index: 1}],
        springFrames: () => [420, 744],
        sheetViewport: () => ({height: 844, bottom: 0}),
      },
      './shared': {
        classes: (...values: (string | undefined | false)[]) => values.filter(Boolean).join(' '),
        isPermittedOutsideOverlay: () => toastTarget,
        useDismissReason: () => ({current: 'action'}),
        useOverlayKeys() {},
        usePresence: () => true,
        useReducedMotion: () => false,
        useSurfaceFocus: () => ({suppressRestore: {current: false}, contentRef() {}, onOpenAutoFocus() {}, onCloseAutoFocus() {}}),
      },
      './overlays.module.css': css,
    });

    const rendered = tree(h.render(() => react.createElement(mod.BottomSheet, {
      open: true,
      onOpenChange() {},
      title: 'Details',
      detents: [420, 744],
      detent: 0,
    }, 'Content')));
    const content = all(rendered, 'RadixContent')[0]!;
    assert.equal(content.props['data-full-height'], undefined);
    assert.equal(content.props['data-body-drag'], true, 'half-detent sheet marks body draggable');
    const sheetBody = all(rendered, 'div').find(node => String(node.props.className ?? '').includes('sheetBody'));
    assert.ok(sheetBody?.props.onPointerDown, 'sheetBody attaches drag handler for half-detent expansion');

    // Guard prevents toast interactions from dismissing the sheet on both pointerDownOutside and interactOutside.
    toastTarget = true;
    let preventedPointer = false;
    content.props.onPointerDownOutside({preventDefault() { preventedPointer = true; }});
    assert.equal(preventedPointer, true);
    let preventedInteract = false;
    content.props.onInteractOutside({preventDefault() { preventedInteract = true; }});
    assert.equal(preventedInteract, true);

    // Ordinary outside interaction when dismissOnOutsidePress=true is allowed.
    toastTarget = false;
    let ordinaryPrevented = false;
    content.props.onInteractOutside({preventDefault() { ordinaryPrevented = true; }});
    assert.equal(ordinaryPrevented, false);

    // When dismissOnOutsidePress=false, onInteractOutside also prevents dismissal.
    const locked = tree(h.render(() => react.createElement(mod.BottomSheet, {
      open: true,
      onOpenChange() {},
      title: 'Locked',
      dismissOnOutsidePress: false,
    }, 'Content')));
    const lockedContent = all(locked, 'RadixContent')[0]!;
    let lockedInteractPrevented = false;
    lockedContent.props.onInteractOutside({preventDefault() { lockedInteractPrevented = true; }});
    assert.equal(lockedInteractPrevented, true);
  });
});

test('Task 2/3 helper: isPermittedOutsideOverlay recognizes toast host descendants safely', () => {
  const prevElement = globalThis.Element;
  class FakeElement {
    private readonly inToast: boolean;
    constructor(inToast: boolean) { this.inToast = inToast; }
    closest(selector: string) { return this.inToast && selector === '[data-toast-host]' ? this : null; }
  }
  (globalThis as {Element: unknown}).Element = FakeElement;
  try {
    const insideToast = new FakeElement(true);
    const outside = new FakeElement(false);
    assert.equal(isPermittedOutsideOverlay({target: insideToast} as unknown as Event), true);
    assert.equal(isPermittedOutsideOverlay({detail: {originalEvent: {target: insideToast}}} as unknown as Event), true);
    assert.equal(isPermittedOutsideOverlay({target: outside} as unknown as Event), false);
    assert.equal(isPermittedOutsideOverlay({target: null} as unknown as Event), false);
  } finally {
    (globalThis as {Element: unknown}).Element = prevElement;
  }
});

test('Task 3: ToastHost unmasks modal aria-hidden/inert, exposes data-toast-host, and removes empty portal after dismissal', async () => {
  await withFakeDom(async ({observers}) => {
    const attrs = new Map<string, string>([['aria-hidden', 'true'], ['inert', '']]);
    const h = createRenderer({
      element: {
        matches: () => false,
        contains: () => false,
        animate: () => ({cancel() {}}),
        getAttribute: (name: string) => attrs.get(name) ?? null,
        hasAttribute: (name: string) => attrs.has(name),
        removeAttribute: (name: string) => { attrs.delete(name); },
      },
    });
    const react = makeReact(h);
    const portalTarget = {tag: 'OVERLAY_ROOT'};
    let present = true;
    const portals: Array<{node: unknown; container: unknown}> = [];
    const mod = await componentModule(new URL('../src/kit/overlays/Feedback.tsx', import.meta.url), {
      react,
      'react-dom': {createPortal: (node: unknown, container: unknown) => { portals.push({node, container}); return node; }},
      '@design': {springs: {snap: {stiffness: 300, damping: 30, mass: 1}}},
      './portal-target': {useOverlayPortalTarget: () => portalTarget},
      '../controls/Button': {Button: 'Button'},
      '../Icon': {Icon: 'Icon'},
      './model': {dismissToast: () => false, springFrames: () => [0]},
      './shared': {
        classes: (...values: (string | undefined | false)[]) => values.filter(Boolean).join(' '),
        useFrameForm: () => 'expanded',
        usePresence: () => present,
        useReducedMotion: () => false,
        useSurfaceMotion: () => () => {},
      },
      './overlays.module.css': css,
    });

    // Render with an active toast: mounts into portalTarget, strips modal aria-hidden/inert.
    const openTree = tree(h.render(() => react.createElement(mod.ToastHost, {
      toast: {id: 't1', message: 'Saved'},
      onDismiss() {},
    })));
    const hostDiv = all(openTree, 'div').find(node => node.props['data-toast-host'] === '');
    assert.ok(hostDiv, 'active ToastHost renders data-toast-host container');
    assert.equal(hostDiv!.props['data-state'], 'open');
    assert.equal(portals.at(-1)?.container, portalTarget);
    assert.equal(attrs.has('aria-hidden'), false, 'ToastHost strips aria-hidden stamped by modal dialog');
    assert.equal(attrs.has('inert'), false, 'ToastHost strips inert stamped by modal dialog');

    // If a modal dialog later re-applies aria-hidden/inert, the observer clears it immediately.
    attrs.set('aria-hidden', 'true');
    attrs.set('inert', '');
    assert.ok(observers.length >= 1);
    observers.at(-1)!.callback();
    assert.equal(attrs.has('aria-hidden'), false);
    assert.equal(attrs.has('inert'), false);

    // During exit fade (toast=null, present=true): keeps container with data-state="closed".
    const closingTree = tree(h.render(() => react.createElement(mod.ToastHost, {toast: null, onDismiss() {}})));
    const closingDiv = all(closingTree, 'div').find(node => node.props['data-toast-host'] === '');
    assert.equal(closingDiv?.props['data-state'], 'closed');

    // After exit presence completes (toast=null, present=false): unmounts portal container completely.
    present = false;
    const portalsBefore = portals.length;
    const emptyTree = h.render(() => react.createElement(mod.ToastHost, {toast: null, onDismiss() {}}));
    assert.equal(emptyTree, null, 'ToastHost must unmount its portal container after toast dismissal');
    assert.equal(portals.length, portalsBefore, 'no stale portal created after dismissal');
  });
});

test('Task 4: ShellScrim portals into useOverlayPortalTarget instead of hardcoded document.body', async () => {
  const h = createRenderer();
  const react = makeReact(h);
  const customPortal = {tag: 'FULLSCREEN_HOST'};
  let target: object | null = customPortal;
  const portals: Array<{node: unknown; container: unknown}> = [];
  const mod = await componentModule(new URL('../src/kit/shell/ShellScrim.tsx', import.meta.url), {
    react,
    'react-dom': {createPortal: (node: unknown, container: unknown) => { portals.push({node, container}); return node; }},
    '../overlays/portal-target': {useOverlayPortalTarget: () => target},
    '../overlays/shared': {usePresence: () => true, useSurfaceMotion: () => () => {}},
    './scrim.module.css': css,
  });

  h.render(() => react.createElement(mod.ShellScrim, {open: true, context: 'account'}));
  assert.equal(portals.length, 1);
  assert.equal(portals[0]!.container, customPortal);

  target = null;
  const empty = h.render(() => react.createElement(mod.ShellScrim, {open: true, context: 'activity'}));
  assert.equal(empty, null);
});

test('Task 5 & 7: Slider enforces 44px touch target & coarse-pointer thumb visibility; overlays CSS does not suppress :focus-visible', () => {
  const controlsCss = readFileSync(new URL('../src/kit/controls/controls.module.css', import.meta.url), 'utf8');
  assert.match(controlsCss, /\.slider\s*\{[^}]*height:\s*var\(--target-touch\)/, 'Slider uses 44px --target-touch height');
  assert.match(controlsCss, /@media\s*\(pointer:\s*coarse\)\s*\{[\s\S]*\.sliderThumb\s*\{[^}]*opacity:\s*1/, 'Slider thumb stays visible on coarse pointers');

  const overlaysCss = readFileSync(new URL('../src/kit/overlays/overlays.module.css', import.meta.url), 'utf8');
  assert.doesNotMatch(overlaysCss, /data-input="keyboard"/, 'overlays CSS must not suppress :focus-visible based on data-input');
  assert.match(overlaysCss, /\.toastHost\s*\{[^}]*pointer-events:\s*auto;/, 'toastHost remains pointer-interactive above modal body locks');
  assert.match(overlaysCss, /\.sheetBody\s*\{[^}]*overflow:\s*auto;/, 'sheetBody remains scrollable without half-detent deadlock');
});

test('Task 6: StateView uses h2 for inline/compact variants, h1 for full-page variant, and supports headingLevel override', async () => {
  const h = createRenderer();
  const react = makeReact(h);
  const mod = await componentModule(new URL('../src/kit/states/State.tsx', import.meta.url), {
    react,
    '@i18n': {defaultI18n: {t: (key: string) => key}},
    '../Icon': {Icon: 'Icon'},
    '../controls/Button': {Button: 'Button'},
    '../artwork': {Artwork: 'Artwork'},
    '../overlays/SidePanel': {SidePanel: 'SidePanel'},
    './model': {knownProgress: (v: number | null) => v},
    './states.module.css': css,
  });

  const inlineTree = tree(h.render(() => react.createElement(mod.StateView, {title: 'Inline state'})));
  assert.equal(all(inlineTree, 'h2').length, 1);
  assert.equal(all(inlineTree, 'h1').length, 0);
  assert.equal(all(inlineTree, 'h2')[0]!.props.className, 'title');

  const compactTree = tree(h.render(() => react.createElement(mod.StateView, {variant: 'compact', title: 'Compact state'})));
  assert.equal(all(compactTree, 'h2').length, 1);
  assert.equal(all(compactTree, 'h1').length, 0);

  const pageTree = tree(h.render(() => react.createElement(mod.StateView, {variant: 'page', title: 'Page state'})));
  assert.equal(all(pageTree, 'h1').length, 1);
  assert.equal(all(pageTree, 'h2').length, 0);

  const customTree = tree(h.render(() => react.createElement(mod.StateView, {variant: 'inline', headingLevel: 'h3', title: 'Panel state'})));
  assert.equal(all(customTree, 'h3').length, 1);
  assert.equal(all(customTree, 'h1').length, 0);
  assert.equal(all(customTree, 'h2').length, 0);

  const layoutTree = tree(h.render(() => react.createElement(mod.StateLayout, {title: 'Full layout'})));
  assert.equal(all(layoutTree, 'h1').length, 1);
});
