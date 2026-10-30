import assert from 'node:assert/strict';

// The caller owns one browser worker, Vite, the profile and evidence lifetime.
// No CSS state injection, fake hover/focus, API or application root is needed.
export async function checkTrackSelection(page, origin, onCase = async () => {}) {
  await page.setViewportSize({width: 1440, height: 900});
  await page.goto(`${origin}/dev/kit?family=browse&case=tracks-songs&capture=true`, {waitUntil: 'networkidle'});
  await page.locator('[data-gallery-ready=true]').waitFor();
  await page.evaluate(() => document.fonts.ready);
  const table = page.getByRole('table');
  const rows = table.locator('[role=rowgroup] > [role=row]');
  const row = rows.first();
  const circle = row.locator('button[data-selected]');
  const title = await circle.getAttribute('aria-label');
  await row.hover();
  await circle.click();
  assert.equal(await circle.getAttribute('aria-pressed'), 'true');
  const away = table.getByRole('columnheader').filter({has: page.getByRole('button')}).first().getByRole('button');
  await away.hover();
  await away.focus();
  const observe = () => circle.evaluate(element => ({
    pressed: element.getAttribute('aria-pressed'), selected: element.dataset.selected,
    circleDisplay: getComputedStyle(element.parentElement).display,
    hovered: element.closest('[role=row]').matches(':hover'),
    focused: element.closest('[role=row]').matches(':focus-within'),
  }));
  const pointerAway = await observe();
  await onCase({id: 'selected-track-away', locator: table, data: {title, pointerAway}});
  assert.equal(pointerAway.hovered, false);
  assert.equal(pointerAway.focused, false);
  assert.equal(pointerAway.circleDisplay, 'grid', 'a genuinely selected circle must persist after pointer and focus leave');
  assert.equal(await circle.isVisible(), true);
  // Exercise the real keyboard callback, clear it, then select again and leave
  // through the tab order. Selection remains owned by the supplied gallery.
  await circle.focus();
  await page.keyboard.press('Space');
  assert.equal(await circle.getAttribute('aria-pressed'), 'false');
  await page.keyboard.press('Space');
  assert.equal(await circle.getAttribute('aria-pressed'), 'true');
  for (let count = 0; count < 12 && await row.evaluate(e => e.matches(':focus-within')); count++) await page.keyboard.press('Tab');
  const keyboardAway = await observe();
  assert.equal(keyboardAway.hovered, false);
  assert.equal(keyboardAway.focused, false);
  assert.equal(keyboardAway.circleDisplay, 'grid');
  assert.equal(await circle.isVisible(), true);
  await circle.focus();
  await page.keyboard.press('Space');
  await away.focus();
  assert.equal((await observe()).circleDisplay, 'none', 'unselected circles still recede after leaving');
  return {title, pointerAway, keyboardAway, cleared: true};
}

export async function checkLiveFacts(page, origin, onCase = async () => {}, cases = [1440, 390].flatMap(width => ['home', 'rich'].map(kind => ({width, kind})))) {
  const results = [];
  for (const {width, kind} of cases) {
      await page.setViewportSize({width, height: 900});
      const [descriptor] = await Promise.all([
        page.waitForResponse(response => new URL(response.url()).pathname === '/src/dev/families/cards.tsx'),
        page.goto(`${origin}/dev/kit?family=cards&case=reference&capture=true`, {waitUntil: 'networkidle'}),
      ]);
      await page.locator('[data-gallery-ready=true]').waitFor();
      const expected = await page.evaluate(async ({kind, descriptor}) => {
        // Use the descriptor module the real registry loaded, including Vite's
        // HMR version if present, so the supplied node reaches that live store.
        const {setCardsReferenceCase} = await import(descriptor);
        const {suppliedLiveElement, factsFor} = await import('/test/fixtures/card-state-facts.tsx');
        setCardsReferenceCase(suppliedLiveElement(kind));
        return factsFor(kind);
      }, {kind, descriptor: descriptor.url()});
      const card = page.locator('[data-card-kind=live]');
      await card.waitFor();
      await page.evaluate(() => document.fonts.ready);
      await page.mouse.move(width - 1, 850);
      const observed = await card.evaluate(element => {
        const parts = [...element.querySelectorAll('[data-fact]')];
        const line = parts[0].parentElement;
        const box = line.getBoundingClientRect();
        return {
          cardWidth: element.getBoundingClientRect().width, line: box.toJSON(),
          gap: getComputedStyle(line).columnGap,
          facts: parts.map(part => {
            const bounds = part.getBoundingClientRect(), css = getComputedStyle(part);
            const text = document.createRange(); text.selectNodeContents(part);
            return {id: part.dataset.fact, role: part.dataset.role, text: part.textContent,
              box: bounds.toJSON(), textBox: text.getBoundingClientRect().toJSON(),
              scrollWidth: part.scrollWidth, clientWidth: part.clientWidth,
              overflowX: css.overflowX, textOverflow: css.textOverflow, whiteSpace: css.whiteSpace};
          }),
        };
      });
      const id = `live-${kind}-${width}`;
      await onCase({id, locator: card, data: observed});
      assert.deepEqual(observed.facts.map(({id, role, text}) => ({id, role, text})), expected, `${id}: preserve every canonical supplied fact, in order`);
      assert.equal(observed.gap, '12px');
      assert.ok(observed.facts.some(part => part.scrollWidth > part.clientWidth), `${id}: the fixture must actually overflow`);
      for (let index = 0; index < observed.facts.length; index++) {
        const part = observed.facts[index];
        assert.equal(part.overflowX, 'hidden', `${id}/${part.id}: glyph paint must be bounded to the fact span`);
        assert.equal(part.textOverflow, 'ellipsis', `${id}/${part.id}: clipped text must signal truncation`);
        assert.equal(part.whiteSpace, 'nowrap');
        assert.ok(part.box.x >= observed.line.x - .1 && part.box.right <= observed.line.right + .1);
        if (index) assert.ok(Math.abs(part.box.x - observed.facts[index - 1].box.right - 12) < .1, `${id}: preserve a real 12px gap between spans`);
      }
      results.push({id, ...observed});
  }
  return results;
}

export async function checkLiveChannelSpacing(page, origin, onCase = async () => {}) {
  await page.setViewportSize({width: 1440, height: 900});
  const [descriptor] = await Promise.all([
    page.waitForResponse(response => new URL(response.url()).pathname === '/src/dev/families/cards.tsx'),
    page.goto(`${origin}/dev/kit?family=cards&case=reference&capture=true`, {waitUntil: 'networkidle'}),
  ]);
  await page.locator('[data-gallery-ready=true]').waitFor();
  await page.evaluate(async descriptor => {
    const {setCardsReferenceCase} = await import(descriptor);
    const {suppliedChannelElement} = await import('/test/fixtures/card-state-facts.tsx');
    setCardsReferenceCase(suppliedChannelElement());
  }, descriptor.url());
  const card = page.locator('[data-card-kind=live]');
  await card.waitFor(); await page.evaluate(() => document.fonts.ready);
  await page.mouse.move(1439, 850);
  const data = await card.evaluate(element => {
    const name = element.querySelector('[class*=_channelName_]');
    const number = element.querySelector('[class*=_channelNumber_]');
    const flag = element.querySelector('[class*=_liveFlag_]');
    const text = document.createRange(); text.selectNode(name.firstChild);
    return {card: element.getBoundingClientRect().toJSON(), name: {text: name.firstChild.textContent, box: text.getBoundingClientRect().toJSON()}, number: {text: number.textContent, box: number.getBoundingClientRect().toJSON()}, flag: {text: flag.textContent, box: flag.getBoundingClientRect().toJSON()}, facts: [...element.querySelectorAll('[data-fact]')].map(part => ({id: part.dataset.fact, role: part.dataset.role, text: part.textContent}))};
  });
  await onCase({id: 'live-channel-spacing-1440', locator: card, data});
  assert.equal(data.card.width, 316);
  assert.equal(data.name.text, 'Northwind News'); assert.equal(data.number.text, '101');
  assert.equal(data.flag.text, 'Live');
  assert.deepEqual(data.facts, [{id: '0', role: 'supplement', text: 'Ends 4:30 PM'}]);
  // Pinned mock.html's name / 12px spacer / number are distinct flex items,
  // separated by the channel row's 10px gap on each side of the spacer.
  assert.ok(Math.abs(data.number.box.x - data.name.box.right - (10 + 12 + 10)) < .1);
  assert.ok(Math.abs(data.flag.box.right - (data.card.right - 18)) < .1, 'the badge keeps its original trailing alignment');
  return data;
}
