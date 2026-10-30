import assert from 'node:assert/strict';

// Run against an existing Vite server and an isolated Playwright page. The caller
// owns browser/profile lifetime, media routing and evidence; no API is required.
export async function checkGalleryRegistration(page, origin, onCase = async () => {}) {
  await page.addInitScript(() => {
    const roots = new Set();
    globalThis.__galleryRegistrationRoots = roots;
    const renderers = new Map();
    globalThis.__REACT_DEVTOOLS_GLOBAL_HOOK__ = {
      supportsFiber: true, renderers,
      inject(renderer) { const id = renderers.size + 1; renderers.set(id, renderer); return id; },
      onCommitFiberRoot: (_renderer, root) => roots.add(root),
      onCommitFiberUnmount() {},
    };
  });
  const results = [];
  for (const id of ['activity-r7-three', 'activity-r7-glyph-both', 'owner-state']) {
    const errors = [];
    const onError = error => errors.push(String(error));
    page.on('pageerror', onError);
    try {
      await page.setViewportSize({width: 1440, height: 900});
      await page.goto(`${origin}/dev/kit?family=shell&case=${id}&capture=true`, {waitUntil: 'networkidle'});
      assert.deepEqual(errors, [], `${id}: importing the real descriptor registry must not execute a standalone entry`);
      await page.locator('[data-gallery-ready=true]').waitFor();
      const result = await page.evaluate(async caseId => {
        const {loadGalleryFamilies} = await import('/src/dev/gallery-registry.ts');
        const families = await loadGalleryFamilies();
        const shell = families.find(family => family.id === 'shell');
        const specimen = shell?.cases.find(item => item.id === caseId);
        return {
          id: caseId, families: families.map(family => family.id),
          registered: !!specimen, roots: globalThis.__galleryRegistrationRoots.size,
          captures: document.querySelectorAll('[data-gallery-capture]').length,
          widgetDocument: [...document.documentElement.classList].some(name => name.includes('widgetDocument')),
          content: document.querySelector('[data-gallery-capture]')?.textContent,
        };
      }, id);
      assert.equal(result.registered, true);
      assert.ok(result.families.includes('dock'));
      assert.equal(result.roots, 1, `${id}: only the application may own a React root`);
      assert.equal(result.captures, 1);
      assert.equal(result.widgetDocument, false, `${id}: descriptor imports must not mutate the standalone document`);
      assert.ok(result.content?.trim());
      await onCase({id});
      assert.deepEqual(errors, []);
      results.push(result);
    } finally {
      page.off('pageerror', onError);
    }
  }
  return results;
}

export async function checkDockWidget(page, origin, onCase = async () => {}) {
  const specimens = await page.evaluate(async () => {
    const {default: family} = await import('/src/dev/families/dock.tsx');
    return family.cases.map(({id, width, height}) => ({id, width, height}));
  });
  const results = [];
  for (const specimen of specimens) {
    const errors = [];
    const onError = error => errors.push(String(error));
    page.on('pageerror', onError);
    try {
      await page.setViewportSize({width: specimen.width, height: specimen.height});
      await page.goto(`${origin}/src/dev/entries/dock-widget.html?case=${specimen.id}`, {waitUntil: 'networkidle'});
      await page.locator('[data-gallery-ready=true]').waitFor();
      const result = await page.evaluate(() => {
        const capture = document.querySelector('[data-gallery-capture]');
        return {
          roots: globalThis.__galleryRegistrationRoots.size,
          captures: document.querySelectorAll('[data-gallery-capture]').length,
          width: capture?.getBoundingClientRect().width,
          height: capture?.getBoundingClientRect().height,
          children: capture?.childElementCount,
          widgetDocument: [...document.documentElement.classList].some(name => name.includes('widgetDocument')),
        };
      });
      assert.deepEqual(errors, [], specimen.id);
      assert.equal(result.roots, 1, specimen.id);
      assert.equal(result.captures, 1, specimen.id);
      assert.equal(result.width, specimen.width, specimen.id);
      assert.equal(result.height, specimen.height, specimen.id);
      assert.ok(result.children > 0, specimen.id);
      assert.equal(result.widgetDocument, true, specimen.id);
      await onCase(specimen);
      assert.deepEqual(errors, [], specimen.id);
      results.push({...specimen, ...result});
    } finally {
      page.off('pageerror', onError);
    }
  }
  return results;
}
