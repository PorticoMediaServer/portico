import {test, expect} from '@playwright/test';

// Exercise the rendered site, then compare each release download with GitHub's
// latest release inventory and follow the actual link without downloading files.
test('website download links resolve to published release assets', async ({page, request}, info) => {
  await page.goto('/download');
  const links = page.locator('a.download-action');
  await expect(links.first()).toBeVisible();
  await page.screenshot({path: info.outputPath('downloads.png'), fullPage: true});
  const hrefs = await links.evaluateAll(nodes => nodes.map(node => node.href));
  expect(hrefs.length).toBeGreaterThan(0);
  expect(new Set(hrefs).size).toBe(hrefs.length);

  const release = await request.get('https://api.github.com/repos/PorticoMediaServer/portico/releases/latest', {
    headers: {Accept: 'application/vnd.github+json', ...(process.env.GITHUB_TOKEN ? {Authorization: `Bearer ${process.env.GITHUB_TOKEN}`} : {})},
  });
  expect(release.ok(), 'latest public release must exist').toBeTruthy();
  const assets = new Map((await release.json()).assets.map(asset => [asset.name, asset]));
  const prefix = 'https://github.com/PorticoMediaServer/portico/releases/latest/download/';
  let releaseLinks = 0;
  for (const href of hrefs) {
    if (href.startsWith(prefix)) {
      releaseLinks++;
      const name = decodeURIComponent(href.slice(prefix.length));
      const asset = assets.get(name);
      expect(asset, `${name} must be in the latest release`).toBeTruthy();
      expect(asset.size, `${name} must not be empty`).toBeGreaterThan(0);
      const response = await request.head(href, {maxRedirects: 10});
      expect(response.ok(), `${name} must resolve`).toBeTruthy();
      expect(response.headers()['content-type'] || '').not.toMatch(/text\/html/i);
    } else {
      // Docker is a package page, not a release file; still verify the link.
      const response = await request.get(href, {maxRedirects: 10});
      expect(response.ok(), `${href} must resolve`).toBeTruthy();
    }
  }
  expect(releaseLinks, 'at least one release download must be present').toBeGreaterThan(0);
});
