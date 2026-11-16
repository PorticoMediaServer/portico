// What the TV-web checks need from a page: signing in with the remote, and naming what has focus
// the way the focus map does ("Continue Watching, card 2", "Spotlight, Play", "Rail, Home").
//
// A control's test ID is `portico.focus.<region>.<child>` (kit/focus/registry.ts), each part
// URI-encoded with its dots escaped, so the ID splits on its dots into exactly four parts.
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));

/** Signs in to `server` at `address` with the remote, as a person would, and opens the main profile. */
export async function signIn(page, {address, username, password}) {
  await page.waitFor(`/tv-front-door/.test(document.activeElement?.getAttribute('data-testid') || '')`, 60000);
  for (let n = 0; n < 3 && (await page.now())?.id !== 'tv-front-door-local'; n++) await page.press('right');
  await page.press('ok'); await sleep(600);
  await page.press('ok'); await page.type(address);
  await page.press('down'); await page.press('ok');
  await page.waitFor(`document.body.innerText.includes('Use a password instead')`, 30000);
  for (let n = 0; n < 4 && !/password instead/i.test((await page.now())?.label ?? ''); n++) await page.press('left');
  await page.press('ok'); await sleep(600);
  await page.press('ok'); await page.type(username);
  await page.press('down'); await page.press('ok'); await page.type(password);
  await page.press('down'); await page.press('ok');
  await page.waitFor(`document.body.innerText.includes('Who’s watching') || document.body.innerText.includes("Who's watching")`, 30000);
  await sleep(500);
  // Open the owner's main profile: the sign-in is kept once a profile is open, as on every TV.
  await openOwnerProfile(page);
}

/** The focused control: its region and child, decoded, and its spoken label. */
export function focusedScript() {
  return `(() => {
    const e = document.activeElement;
    if (!e || e === document.body) return null;
    const id = e.getAttribute('data-testid') || '';
    const parts = id.startsWith('portico.focus.') ? id.split('.') : [];
    const decode = part => { try { return decodeURIComponent(part); } catch { return part; } };
    const rect = e.getBoundingClientRect();
    const region = parts.length === 4 ? decode(parts[2]) : '';
    // A card's place in its row, from where its cell is laid out (rows measured at Home's start).
    let index;
    const geometry = (window.__tvwebRows || {})[region];
    if (geometry) {
      let cell = e; while (cell && getComputedStyle(cell).position !== 'absolute') cell = cell.parentElement;
      if (cell && cell.parentElement) index = Math.round((cell.getBoundingClientRect().left - cell.parentElement.getBoundingClientRect().left - geometry.leading) / geometry.stride) + 1;
    }
    return {id, region, index, child: parts.length === 4 ? decode(parts[3]) : '',
      label: (e.getAttribute('aria-label') || e.innerText || '').replace(/\\s+/g, ' ').trim().slice(0, 80), left: Math.round(rect.left), top: Math.round(rect.top)};
  })()`;
}

/** Measures where each Home row lays out its first and second cards, while every row is at its start,
 * so a card's place can be read from its cell (`focusedScript`). */
export const measureRowsScript = `(() => {
  const rows = {};
  for (const e of document.querySelectorAll('[tabindex="0"]')) {
    const id = e.getAttribute('data-testid') || '';
    if (!id.startsWith('portico.focus.')) continue;
    const region = decodeURIComponent(id.split('.')[2]);
    if (!/\\.home\\.row\\./.test(region)) continue;
    let cell = e; while (cell && getComputedStyle(cell).position !== 'absolute') cell = cell.parentElement;
    if (!cell || !cell.parentElement) continue;
    (rows[region] = rows[region] || []).push(cell.getBoundingClientRect().left - cell.parentElement.getBoundingClientRect().left);
  }
  window.__tvwebRows = {};
  for (const [region, lefts] of Object.entries(rows)) {
    const sorted = [...new Set(lefts.map(Math.round))].sort((a, b) => a - b);
    if (sorted.length > 1) window.__tvwebRows[region] = {leading: sorted[0], stride: sorted[1] - sorted[0]};
  }
  return Object.keys(window.__tvwebRows).length;
})()`;

/** On "Who's watching", opens the owner's profile and waits for Home's Play. */
export async function openOwnerProfile(page) {
  for (const until = Date.now() + 45000; Date.now() < until; await sleep(250)) {
    const focus = await page.now();
    if (/\.home\.spotlight$/.test(focus?.region ?? '') && focus.child === 'play') return;
    if (/^tv\.auth\.profiles\./.test(focus?.region ?? '')) {
      if (!/owner/i.test(focus.label)) {await page.press('left'); continue;}
      await page.press('ok'); await sleep(1500);
    }
  }
  throw new Error('Home did not open on Play');
}

/** Adds the remote and a few readings to a CDP page (test/cdp.mjs). */
export function remote(page) {
  const keyDelay = Number(process.env.TVWEB_KEY_DELAY_MS ?? 350);
  return Object.assign(page, {
    async press(name, {holdMs = 0} = {}) {await page.key(name, {holdMs}); await sleep(keyDelay);},
    async presses(...names) {for (const name of names) await page.press(name);},
    async type(text) {await page.send('Input.insertText', {text}); await sleep(200);},
    now: () => page.evaluate(focusedScript()),
  });
}

/**
 * Names the focus map uses, as predicates over `now()`. A card is named by its row's id and its
 * 1-based place in that row, read from the server's own order for the row (`order`).
 */
export function names(order) {
  const ends = (region, tail) => region.endsWith(tail);
  const entryId = child => {try { return JSON.parse(child)[2]; } catch { return undefined; }};
  return {
    spotlight: button => ({what: `Spotlight, ${button}`, test: f => !!f && ends(f.region, '.home.spotlight') && f.child === button}),
    card: (row, n) => ({what: `${row}, card ${n}`, test: f => !!f && ends(f.region, `.home.row.${JSON.stringify(row)}`) && f.index === n}),
    anyCard: row => ({what: `${row}, a card`, test: f => !!f && ends(f.region, `.home.row.${JSON.stringify(row)}`)}),
    placeholder: row => ({what: `${row}, the skeleton card`, test: f => !!f && ends(f.region, `.home.row.${JSON.stringify(row)}`) && /^\["page"/.test(f.child)}),
    customize: {what: 'Customize Home', test: f => !!f && ends(f.region, '.home.customize') && f.child === 'customize'},
    rail: item => ({what: `Rail, ${item}`, test: f => !!f && ends(f.region, '.rail') && (item === 'profile' ? f.child.startsWith('profile:') : f.child === item)}),
    label: (what, pattern) => ({what, test: f => !!f && pattern.test(f.label)}),
    same: before => ({what: `${before?.label ?? 'nothing'} (unchanged)`, test: f => !!f && !!before && f.id === before.id}),
  };
}
