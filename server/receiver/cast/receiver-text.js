// Canonical receiver catalogue ac35269baf21d49a4ca65fa393cb862e097feb8d72b9ff9070329e3d6c7f56f9; formatter 6d1d104075b7bc1f85a36b628212119ffc89c50468b63ea27781e5b660e9b24c.
// Regenerate: node server/receiver/generate-cast-text.mjs
(function () {
'use strict';
/**
 * A small ICU MessageFormat implementation (the FormatJS / `intl-messageformat`
 * syntax subset Portico uses): `{arg}`, `{arg, number[, integer|percent|::group-off]}`,
 * `{arg, date|time[, short|medium|long|full]}`, `{arg, plural, [offset:n] =0 {…} one {…} other {…}}`
 * with `#`, `{arg, selectordinal, …}`, `{arg, select, key {…} other {…}}`, and
 * apostrophe quoting (`''` is a quote, `'{…}'` is literal).
 *
 * Why not the `intl-messageformat` package: the Apple app's Metro resolver
 * doesn't look outside `portico-react-native/apps/apple/node_modules`, and the shared
 * packages must load unchanged on web, iOS, tvOS and in node tests. Catalogue
 * files are plain ICU strings, so swapping in FormatJS later is a drop-in.
 */












class MessageSyntaxError extends Error {
           source        ;
  constructor(message        , source        ) { super(`${message} in message: ${source}`); this.name = 'MessageSyntaxError'; this.source = source; }
}

function parseMessage(source        )         {
  let i = 0;
  const fail = (why        )        => { throw new MessageSyntaxError(why, source); };
  const ws = () => { while (i < source.length && /\s/.test(source[i] )) i++; };
  const ident = ()         => { ws(); const start = i; while (i < source.length && /[^\s,{}#]/.test(source[i] )) i++; if (start === i) fail('expected a name'); return source.slice(start, i); };
  function nodes(inPlural         , depth        )         {
    const out         = [];
    let text = '';
    const flush = () => { if (text) { out.push({kind: 'text', value: text}); text = ''; } };
    while (i < source.length) {
      const c = source[i] ;
      if (c === "'") {
        if (source[i + 1] === "'") { text += "'"; i += 2; continue; }
        const next = source[i + 1];
        if (next === '{' || next === '}' || (inPlural && next === '#')) {
          const end = source.indexOf("'", i + 1);
          if (end < 0) { text += source.slice(i + 1); i = source.length; continue; }
          text += source.slice(i + 1, end).replace(/''/g, "'"); i = end + 1; continue;
        }
        text += c; i++; continue;
      }
      if (c === '}') { if (depth === 0) fail('unmatched }'); break; }
      if (c === '#' && inPlural) { flush(); out.push({kind: 'pound'}); i++; continue; }
      if (c === '{') { flush(); i++; out.push(argument(depth)); continue; }
      text += c; i++;
    }
    flush();
    return out;
  }
  function options(ordinalOrPlural         , depth        )                                                    {
    const opts                         = {};
    let offset = 0;
    ws();
    if (ordinalOrPlural && source.startsWith('offset:', i)) { i += 7; ws(); const m = /^\d+/.exec(source.slice(i)); if (!m) fail('bad offset'); offset = Number(m [0]); i += m [0].length; }
    for (;;) {
      ws();
      if (source[i] === '}') break;
      const key = ident();
      ws();
      if (source[i] !== '{') fail(`expected { after ${key}`);
      i++;
      opts[key] = nodes(ordinalOrPlural, depth + 1);
      if (source[i] !== '}') fail('unclosed option');
      i++;
    }
    if (!opts.other) fail('missing other');
    return {offset, options: opts};
  }
  function argument(depth        )       {
    const name = ident();
    ws();
    if (source[i] === '}') { i++; return {kind: 'arg', name}; }
    if (source[i] !== ',') fail('expected , or }');
    i++;
    const type = ident();
    ws();
    let node      ;
    if (type === 'plural' || type === 'selectordinal' || type === 'select') {
      if (source[i] !== ',') fail(`expected , after ${type}`);
      i++;
      const {offset, options: opts} = options(type !== 'select', depth);
      node = type === 'select' ? {kind: 'select', name, options: opts} : {kind: 'plural', name, ordinal: type === 'selectordinal', offset, options: opts};
    } else if (type === 'number' || type === 'date' || type === 'time') {
      let style                    ;
      if (source[i] === ',') { i++; style = ident(); ws(); }
      node = type === 'number' ? {kind: 'number', name, style} : {kind: type, name, style};
    } else {
      return fail(`unknown argument type ${type}`);
    }
    ws();
    if (source[i] !== '}') fail('unclosed argument');
    i++;
    return node;
  }
  const result = nodes(false, 0);
  if (i < source.length) fail('unexpected }');
  return result;
}



const pluralCache = new Map                                 ();
function pluralCategory(locale        , n        , ordinal         )         {
  const key = `${locale}|${ordinal}`;
  let rules = pluralCache.get(key);
  if (rules === undefined) {
    try { rules = typeof Intl !== 'undefined' && Intl.PluralRules ? new Intl.PluralRules(locale, {type: ordinal ? 'ordinal' : 'cardinal'}) : null; } catch { rules = null; }
    pluralCache.set(key, rules);
  }
  if (rules) return rules.select(n);
  // English fallback for runtimes without Intl.PluralRules.
  if (!ordinal) return n === 1 ? 'one' : 'other';
  const t = n % 10, h = n % 100;
  return t === 1 && h !== 11 ? 'one' : t === 2 && h !== 12 ? 'two' : t === 3 && h !== 13 ? 'few' : 'other';
}

function numberText(value        , o               , style         )         {
  try {
    // `::group-off` (an ICU skeleton): a number that is a name more than an amount, "1080p" and not "1,080p".
    const opts                           = style === 'integer' ? {maximumFractionDigits: 0} : style === 'percent' ? {style: 'percent', maximumFractionDigits: 0} : style === '::group-off' ? {useGrouping: false} : {};
    return new Intl.NumberFormat(o.locale, opts).format(value);
  } catch { return String(value); }
}

function dateText(value               , o               , kind                 , style = 'medium')         {
  const d = value instanceof Date ? value : new Date(value);
  if (!Number.isFinite(d.getTime())) return '';
  const s = (['short', 'medium', 'long', 'full'].includes(style) ? style : 'medium')                                        ;
  try {
    return new Intl.DateTimeFormat(o.locale, {...(kind === 'date' ? {dateStyle: s} : {timeStyle: s === 'full' || s === 'long' ? 'short' : s}), timeZone: o.timeZone, ...(kind === 'time' && o.hour12 !== undefined ? {hour12: o.hour12} : {})}).format(d);
  } catch { return kind === 'date' ? d.toDateString() : d.toTimeString().slice(0, 5); }
}

function render(nodes        , values               , o               , pound         )         {
  let out = '';
  for (const n of nodes) {
    switch (n.kind) {
      case 'text': out += n.value; break;
      case 'pound': out += pound === undefined ? '#' : numberText(pound, o); break;
      case 'arg': { const v = values[n.name]; out += v instanceof Date ? dateText(v, o, 'date') : typeof v === 'number' ? numberText(v, o) : v == null ? '' : String(v); break; }
      case 'number': { const v = Number(values[n.name]); out += Number.isFinite(v) ? numberText(v, o, n.style) : ''; break; }
      case 'date': case 'time': { const v = values[n.name]; out += v instanceof Date || typeof v === 'number' ? dateText(v, o, n.kind, n.style) : ''; break; }
      case 'plural': {
        const raw = Number(values[n.name]);
        const v = Number.isFinite(raw) ? raw : 0;
        const exact = n.options[`=${v}`];
        const branch = exact ?? n.options[pluralCategory(o.locale, v - n.offset, n.ordinal)] ?? n.options.other ;
        out += render(branch, values, o, v - n.offset);
        break;
      }
      case 'select': { const key = String(values[n.name] ?? 'other'); out += render(n.options[key] ?? n.options.other , values, o, pound); break; }
    }
  }
  return out;
}

const parsed = new Map                ();
/** Format an ICU message. Parses once per distinct source string. */
function formatMessage(source        , values                = {}, options                = {locale: 'en-US'})         {
  let nodes = parsed.get(source);
  if (!nodes) { nodes = parseMessage(source); parsed.set(source, nodes); }
  return render(nodes, values, options);
}

/** Argument names a message uses (for catalogue drift checks). */
function messageArguments(source        )           {
  const names = new Set        ();
  const walk = (nodes        ) => { for (const n of nodes) { if ('name' in n) names.add(n.name); if (n.kind === 'plural' || n.kind === 'select') Object.values(n.options).forEach(walk); } };
  walk(parseMessage(source));
  return [...names].sort();
}

const messages = Object.freeze(Object.assign(Object.create(null), {
  "cast.receiver.allow": "Allow",
  "cast.receiver.buffering": "Buffering",
  "cast.receiver.deny": "Don’t allow",
  "cast.receiver.episodeLabel": "S{season} E{episode}",
  "cast.receiver.error.busy.body": "Try again in a moment from your phone or computer.",
  "cast.receiver.error.busy.title": "Your server is busy right now",
  "cast.receiver.error.decode.body": "Try again from your phone or computer, or choose a lower quality.",
  "cast.receiver.error.decode.title": "This title couldn’t play on this TV",
  "cast.receiver.error.generic.body": "Try again from your phone or computer.",
  "cast.receiver.error.generic.title": "This title couldn’t play on this TV",
  "cast.receiver.error.unavailable.body": "It may have been removed, or your access may have changed.",
  "cast.receiver.error.unavailable.title": "This title isn’t available right now",
  "cast.receiver.error.unreachable.body": "Check that your server is on and that this TV is on the same network, then try again.",
  "cast.receiver.error.unreachable.title": "This TV can’t reach your server",
  "cast.receiver.gettingReady": "Getting ready",
  "cast.receiver.idle.hint": "On your phone or computer, open Portico, choose {playOn}, and pick this TV.",
  "cast.receiver.idle.playOn": "Play on",
  "cast.receiver.idle.title": "Ready to play from Portico",
  "cast.receiver.iPhoneOrIPad": "An iPhone or iPad",
  "cast.receiver.key.back": "Back",
  "cast.receiver.key.ok": "OK",
  "cast.receiver.notice.seekFailed": "That seek couldn’t be made.",
  "cast.receiver.notice.stopFailed": "Stop couldn’t be confirmed.",
  "cast.receiver.nowPlaying": "Now playing",
  "cast.receiver.pair.busy": "This TV is playing something for someone else. Ask them to stop, then try again.",
  "cast.receiver.pair.canceled": "That code was canceled.",
  "cast.receiver.pair.connect": "Connect",
  "cast.receiver.pair.connected": "Connected. Choose a title on your phone or computer.",
  "cast.receiver.pair.connecting": "Connecting…",
  "cast.receiver.pair.consumed": "That code has already been used. Try again from your phone or computer.",
  "cast.receiver.pair.enterCode": "Enter the code shown on your phone.",
  "cast.receiver.pair.expired": "That code has expired. Try again from your phone or computer.",
  "cast.receiver.pair.failed": "This TV couldn’t connect. Try again.",
  "cast.receiver.pair.invalid": "That code isn’t valid.",
  "cast.receiver.pair.label": "Pairing code",
  "cast.receiver.pair.rateLimited": "Too many attempts. Wait a moment, then try again.",
  "cast.receiver.paused": "Paused",
  "cast.receiver.preparingLong": "Your server is still preparing this for the TV.",
  "cast.receiver.someone": "Someone",
  "cast.receiver.takeover.absent": "Switching in a few seconds.",
  "cast.receiver.takeover.present": "Waiting for the person watching now to allow it.",
  "cast.receiver.takeover.title": "{requester} wants to play on this TV"
}));
window.porticoCastText = Object.freeze({
  t: function (key, params) {
    return Object.prototype.hasOwnProperty.call(messages, key)
      ? formatMessage(messages[key], params || {}, {locale: 'en-US'}) : '';
  }
});
})();
