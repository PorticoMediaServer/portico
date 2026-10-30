import test from 'node:test';
import assert from 'node:assert/strict';
import {catalogue, createI18n, enUS, formatMessage, messageArguments, parseMessage, resolveLocale, resolveRegion, type MessageId} from '../../i18n/src/index.ts';
import {presentError} from '../src/presentation/index.ts';
import {ApiError} from '../src/index.ts';

test('ruling31 TV hand-off uses the genuine server name and exact bounded-state copy', () => {
  const {t} = createI18n(resolveRegion(undefined, {locales: ['en-US']}));
  assert.equal(t('next.auth.tv.requiredTwoStepBody', {server: 'Portico demo'}), 'Portico demo asks for two-step verification on this account. Turn it on from your phone or computer, then sign in again.');
  assert.equal(t('next.auth.tv.requiredPasswordBody', {server: 'Portico demo'}), 'Portico demo asks for a new password on this account. Choose one from your phone or computer, then sign in again.');
  assert.equal(t('next.auth.tv.handoffHint'), 'Scan the code with your phone, or go to this address on a computer.');
});

test('player panels retain the exact temporary-change and uninterrupted-listening frame copy', () => {
  const {t} = createI18n('en-US');
  assert.equal(t('player.opt.temporaryChanges'), 'Changes here last until you stop watching.');
  assert.equal(t('player.opt.defaultsUnchanged'), 'Your defaults stay as they are in Settings.');
  assert.equal(t('player.opt.hearingContinues'), "What you're hearing keeps playing.");
});

test('catalogue: every en-US message parses, and the regional catalogues keep its arguments', () => {
  for (const [id, source] of Object.entries(enUS)) assert.doesNotThrow(() => parseMessage(source), id);
  for (const locale of ['en-CA', 'en-GB'] as const) {
    for (const [id, source] of Object.entries(catalogue(locale))) {
      assert.ok(id in enUS, `${id} is not an en-US message`);
      assert.deepEqual(messageArguments(source!), messageArguments(enUS[id as MessageId]), `${locale} ${id}`);
    }
  }
});

test('catalogue: US spelling in the source; Canada and Britain spell by rule', () => {
  const us = Object.values(enUS).join('\n');
  assert.doesNotMatch(us, /\b(Favourite|favourite|Customise|customise|colour|Colour|normalisation|Cancelled)/);
  const ca = catalogue('en-CA'), gb = catalogue('en-GB');
  assert.equal(ca['saved.tab.favorites'], 'Favourites');
  assert.equal(ca['download.state.canceled'], 'Cancelled');
  // Canada keeps "-ize" and "program"; Britain does not.
  assert.equal(ca['home.customize.action'], undefined);
  assert.equal(gb['home.customize.action'], 'Customise Home');
  assert.equal(gb['guide.label'], 'Programme guide');
  // An argument name is not a word, and a language's own name is never respelled.
  assert.equal(gb['web.profile.colorName'], '{color} colour');
  assert.equal(gb['locale.en-US'], undefined);
});

test('catalogue: recovery actions use one verb, "Try again" (CON-17)', () => {
  // states.html#24 draws "Reload" on the web's last-resort page because that
  // button starts the app afresh; a retry verb would promise to resume the
  // failed work instead. The exemption is this exact ID and text only.
  const approvedPageReloads: Readonly<Record<string, string>> = {'next.states.reloadPage': 'Reload'};
  const offenders = Object.entries(enUS as Record<string, string>)
    .filter(([id, value]) => approvedPageReloads[id] !== value)
    .filter(([, value]) => /^(Retry|Try now|Reload)$/.test(value)).map(([id]) => id);
  assert.deepEqual(offenders, []);
  // Nothing else may borrow the exemption, and the approved line cannot drift.
  for (const [id, value] of Object.entries(approvedPageReloads)) {
    assert.equal((enUS as Record<string, string>)[id], value, id);
    assert.ok(/^(Retry|Try now|Reload)$/.test(value), id);
  }
});

test('catalogue: no engineering words reach people (CON-21)', () => {
  // The approved Settings copy (38b51b9, retained in 14a9a19) uses "slower"
  // for these concrete effects. An exception requires both this ID and exact text.
  const approvedSettingsCopy: Readonly<Record<string, string>> = {
    'next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.2.control.options.2.hint': 'Reads every file in full. Slower.',
    'next.settingsSpec.server.libraries.panels.library.tabs.3.groups.1.rows.1.control.options.1.hint': 'Slower scans',
    'next.settingsSpec.server.segments.advanced.groups.1.rows.1.control.options.1.hint': 'Slower, finds late intros',
    'next.settingsSpec.server.segments.advanced.groups.1.rows.2.control.options.1.hint': 'Slower, finds long credits',
    'next.settingsSpec.server.segments.advanced.groups.1.rows.3.control.options.1.hint': 'Slower, finds scenes after the credits',
    'next.settingsSpec.server.integrations.advanced.groups.0.rows.1.help': 'Slower answers count as not arrived.',
    'next.settingsSpec.server.streaming.advanced.groups.2.rows.0.help': 'Slower is smaller and cleaner, but heavier.',
    'next.settingsSpec.server.storage.groups.0.rows.3.help': 'Pages and videos start slower for a while.',
    'next.settingsSpec.server.storage.groups.0.rows.3.control.confirm': 'Empty the cache? Some pages and videos start slower while Portico rebuilds it. Nothing you added is lost.',
  };
  for (const [id, text] of Object.entries(enUS)) {
    const checked = approvedSettingsCopy[id] === text ? text.replace(/\bslower\b/gi, '') : text;
    assert.doesNotMatch(checked, /\b(lane|slower|Prepared copy|in this build|operation key|Retry shortly)\b/i, id);
  }
});

test('presentError messageIds exist in the catalogue', () => {
  const errors = [new TypeError('x'), new ApiError(401, 'unauthorized', 'x'), new ApiError(403, 'forbidden', 'x'), new ApiError(404, 'not_found', 'x'), new ApiError(409, 'x_conflict', 'x'), new ApiError(503, 'timeout', 'x'), new ApiError(503, 'x_busy', 'x'), new ApiError(429, 'rate_limited', 'x'), new ApiError(426, 'unsupported_version', 'x'), {code: 'invalid_home'}, {code: 'invalid_request'}, {code: 'storage_full'}, {code: 'x_unavailable'}, new Error('x')];
  for (const e of errors) {
    const p = presentError(e, 'home');
    assert.ok(p.messageId in enUS, p.messageId);
    assert.equal(createI18n().t(p.messageId as MessageId), p.body, p.messageId);
  }
});

test('ICU: arguments, plural with # and =0, select, ordinal, quoting', () => {
  const o = {locale: 'en-US'};
  assert.equal(formatMessage('Hello {name}', {name: 'Alex'}, o), 'Hello Alex');
  assert.equal(formatMessage('{n, plural, =0 {none} one {# title} other {# titles}}', {n: 0}, o), 'none');
  assert.equal(formatMessage('{n, plural, one {# title} other {# titles}}', {n: 1}, o), '1 title');
  assert.equal(formatMessage('{n, plural, one {# title} other {# titles}}', {n: 1234}, o), '1,234 titles');
  assert.equal(formatMessage('{k, select, a {Apple} other {Other}}', {k: 'a'}, o), 'Apple');
  assert.equal(formatMessage('{n, selectordinal, one {#st} two {#nd} few {#rd} other {#th}}', {n: 22}, o), '22nd');
  assert.equal(formatMessage("It''s '{literal}'", {}, o), "It's {literal}");
  assert.equal(formatMessage('{p, number, percent}', {p: 0.42}, o), '42%');
  assert.throws(() => parseMessage('{n, plural, one {x}}'));
});

test('locale resolution: exact, then the region\'s English, then en-US', () => {
  assert.equal(resolveLocale('en-CA'), 'en-CA');
  assert.equal(resolveLocale('en_CA'), 'en-CA');
  assert.equal(resolveLocale('en-GB'), 'en-GB');
  assert.equal(resolveLocale('en-AU'), 'en-GB');
  assert.equal(resolveLocale('en'), 'en-US');
  assert.equal(resolveLocale('en-PH'), 'en-US');
  assert.equal(resolveLocale(['fr-CA', 'en-CA']), 'en-CA');
  assert.equal(resolveLocale('fr-FR'), 'en-US');
  assert.equal(resolveLocale(undefined), 'en-US');
});

test('region preferences: auto follows the device for words and formats; explicit values win; the time zone is always the device’s', () => {
  const device = {locales: ['en-CA'], timeZone: 'America/Halifax'};
  const auto = resolveRegion({locale: 'auto', hourCycle: 'auto'}, device);
  assert.deepEqual({ui: auto.uiLocale, fmt: auto.formatLocale, tz: auto.timeZone, h12: auto.hour12}, {ui: 'en-CA', fmt: 'en-CA', tz: 'America/Halifax', h12: undefined});
  assert.equal(resolveRegion(undefined, {locales: ['en-GB']}).uiLocale, 'en-GB');
  // A profile that chooses a catalogue gets it; a language we don't ship reads the device's English.
  assert.equal(resolveRegion({locale: 'en-CA'}, {locales: ['en-US']}).uiLocale, 'en-CA');
  const explicit = resolveRegion({locale: 'fr-FR', hourCycle: 'h12'}, device);
  assert.equal(explicit.uiLocale, 'en-CA');
  assert.equal(explicit.formatLocale, 'fr-FR');
  assert.equal(explicit.timeZone, 'America/Halifax');
  assert.equal(explicit.hour12, true);
  // A zone a stale profile document or an old caller still carries is not read at all.
  assert.equal(resolveRegion({locale: 'auto', timeZone: 'Europe/Paris'} as never, device).timeZone, 'America/Halifax');
  // A device that reports no zone, or one the runtime does not know, leaves the runtime's own.
  assert.equal(resolveRegion(undefined, {locales: ['en-CA']}).timeZone, undefined);
  assert.equal(resolveRegion(undefined, {locales: ['en-CA'], timeZone: 'Not/AZone'}).timeZone, undefined);
});

test('formatters: duration, bytes, relative time, clock, list, time zones and hour cycle', () => {
  const us = createI18n(resolveRegion({locale: 'en-US'}, {locales: ['en-US'], timeZone: 'UTC'}));
  assert.equal(us.duration(6120), '1h 42m');
  assert.equal(us.duration(3600, 'long'), '1 hour');
  assert.equal(us.duration(61 * 60, 'long'), '1 hour 1 minute');
  assert.equal(us.duration(0), '');
  assert.equal(us.bytes(1_500_000_000), '1.4 GB');
  assert.equal(us.bytes(undefined), '—');
  // Sizes step by 1024 and take the locale's own unit and number format; whole bytes are spelled out.
  assert.equal(us.bytes(512), '512 bytes');
  assert.equal(us.bytes(1), '1 byte');
  assert.equal(us.bytes(1536), '1.5 kB');
  assert.equal(us.bytes(12 * 1024 * 1024), '12 MB');
  const fr = createI18n(resolveRegion({locale: 'fr-FR'}, {locales: ['en-US'], timeZone: 'UTC'}));
  assert.equal(fr.bytes(1_500_000_000).replace(/\s/g, ' '), '1,4 Go');
  assert.equal(us.clock(3723), '1:02:03');
  const now = Date.parse('2026-09-22T12:00:00Z');
  assert.equal(us.relativeTime(now - 10_000, now), 'Just now');
  assert.equal(us.relativeTime(now - 5 * 60_000, now), '5 minutes ago');
  assert.equal(us.relativeTime(now - 3 * 3600_000, now), '3 hours ago');
  assert.equal(us.relativeTime(now - 26 * 3600_000, now), 'Yesterday');
  assert.equal(us.relativeTime(now - 4 * 86400_000, now), '4 days ago');
  assert.equal(us.list(['Movies', 'TV', 'Music']), 'Movies, TV, and Music');
  assert.equal(us.time(Date.parse('2026-09-22T15:05:00Z')), '3:05 PM');
  const h23 = createI18n(resolveRegion({locale: 'en-US', hourCycle: 'h23'}, {locales: ['en-US'], timeZone: 'UTC'}));
  assert.equal(h23.time(Date.parse('2026-09-22T15:05:00Z')), '15:05');
  // Date and time together are one format, so each locale joins them its own way and the clock choice holds.
  const afternoon = Date.parse('2026-09-22T15:05:00Z');
  assert.equal(us.dateTime(afternoon).replace(/\s/g, ' '), 'Sep 22, 2026, 3:05 PM');
  assert.equal(h23.dateTime(afternoon), 'Sep 22, 2026, 15:05');
  assert.equal(createI18n(resolveRegion({locale: 'ja-JP'}, {locales: ['en-US'], timeZone: 'UTC'})).dateTime(afternoon), '2026/09/22 15:05');
  assert.equal(us.dateTime('not a date'), '');
  // Other fields: the profile's locale and clock, the device's zone unless another is asked for.
  assert.equal(h23.formatDate(afternoon, {weekday: 'short', hour: 'numeric', minute: '2-digit'}), 'Tue 15:05');
  assert.equal(us.formatDate(afternoon, {hour: 'numeric', minute: '2-digit', timeZone: 'America/Halifax'}).replace(/\s/g, ' '), '12:05 PM');
  assert.equal(us.formatDate('1961-05-06', {year: 'numeric', month: 'long', day: 'numeric', timeZone: 'UTC'}), 'May 6, 1961');
  assert.equal(us.languageName('fr'), 'French');
  assert.equal(us.languageName('not a language'), 'not a language');
  assert.equal(us.list(['Apple', 'Google'], 'or'), 'Apple or Google');
  const ca = createI18n(resolveRegion({locale: 'en-CA'}, {locales: ['en-US'], timeZone: 'UTC'}));
  assert.equal(ca.t('saved.tab.favorites'), 'Favourites');
  assert.equal(ca.t('entry.markWatched'), 'Mark as watched'); // falls back to en-US
  assert.equal(ca.date(Date.parse('2026-09-22T15:05:00Z'), 'short'), '2026-09-22');
  assert.equal(us.t('home.row.recentlyAddedIn', {library: 'Movies'}), 'Recently added in Movies');
  assert.equal(us.t('count.titles', {count: 80}), '80 titles');
  assert.equal(us.t('error.title.load', {subject: 'library'}), 'This library couldn’t load');
});

test('string lint: flags human copy, ignores identifiers, expressions and allowed lines', async () => {
  const {countLiterals, isCopy} = await import('../../i18n/scripts/lint-strings.mjs');
  assert.equal(isCopy('Try again'), true);
  assert.equal(isCopy('Details'), true);
  assert.equal(isCopy('ghost'), false);
  assert.equal(isCopy('/v1/items'), false);
  assert.equal(isCopy('saved.tab.favorites'), false);
  const source = [
    '<Button variant="ghost" label="Try again" onClick={retry} />',
    '<Text>Nothing here yet</Text>',
    "{id: 'a', label: 'Add to Watchlist'}",
    '<Button label={i18n.t(\'action.refresh\')} />',
    '<Text>{count > 1 ? a : b}</Text>',
    '<Text>Deliberate</Text> // lint-strings-allow',
  ].join('\n');
  assert.equal(countLiterals(source).count, 3);
});

test('en-US catalogue: US spelling in every message (CON-19)', async () => {
  const {enUS} = await import('../../i18n/src/catalog/en-US.ts');
  // ICU select keys (`cancelled {Canceled}`) are server codes, not display text.
  const british = /\b(programmes?|favour(?:ite)?s?|colours?|cancelled|licence|behaviour|centre|catalogue|organis\w*|recognis\w*|normalis\w*)\b/i;
  const offenders = Object.entries(enUS as Record<string, string>).filter(([, v]) => british.test(v.replace(/(?<=(?:\}|select,|plural,)\s*)[\w=-]+\s*\{/g, '{'))).map(([k, v]) => `${k}: ${v}`);
  assert.deepEqual(offenders, []);
});

test('Hosted-backed surfaces name the Portico Account, never "your server"', () => {
  const offline = new TypeError('Failed to fetch');
  assert.match(presentError(offline, 'portico-account').body, /Portico Account/);
  assert.match(presentError(offline, 'claim').body, /Portico Account/);
  assert.match(presentError(offline, 'link').body, /Portico Account/);
  assert.match(presentError(offline, 'sign-in', {service: 'portico-account'}).body, /Portico Account/);
  assert.match(presentError(offline, 'sign-in').body, /your server/, 'a direct sign-in still means the server');
  assert.match(presentError(offline, 'portico-account', {retrying: true}).body, /Portico Accounts.*keeps trying/);
  assert.match(presentError(offline, 'portico-account', {deviceOnline: false}).body, /This device is offline/);
  assert.equal(presentError({status: 504, code: 'timeout'}, 'portico-account').messageId, 'error.account.timeout');
  assert.equal(presentError(offline, 'portico-account').title, 'Your Portico Account couldn’t load');
});

test('a picture height is a name, not an amount: 1080p, never 1,080p', () => {
  assert.equal(createI18n({locale: 'en-US'}).t('quality.height', {height: 1080}), '1080p');
  assert.equal(createI18n({locale: 'en-US'}).t('quality.height', {height: 2160}), '2160p');
});


test('credits cards preserve the published after-scene and automatic-countdown text', () => {
  const {t} = createI18n('en-US');
  assert.equal(t('player.upNext.startsAfterScene', {time: '1:28'}), 'Starts in 1:28, after the scene');
  assert.equal(t('player.upNext.sceneAfterCredits'), "There's a scene after the credits. Skip credits takes you straight to it.");
  assert.equal(t('player.upNext.playingIn', {seconds: 7}), 'Playing in 7s');
});
