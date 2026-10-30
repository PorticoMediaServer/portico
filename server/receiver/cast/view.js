/* Cast receiver presentation. Only sanitized observations cross this port.
 * No session credentials, playback commands, queue, or fabricated progress.
 * Geometry/copy: cast-receiver.html at design 69f2d45.
 */
(function () {
  'use strict';
  var $ = function (id) { return document.getElementById(id); };
  var ui = $('ui'), metadata = null, image = '', lastIdentity = '', lastState = '';
  var overlayTimer = null, takeoverTimer = null;
  var promptIdentity = '', promptDuration = 0;
  var OVERLAY_VISIBLE_MS = 5000;
  function t(key, params) { return window.porticoCastText ? window.porticoCastText.t('cast.receiver.' + key, params) : ''; }
  function visible(id, on) {
    var node = $(id);
    // SVG does not reflect HTMLElement.hidden; the attribute governs both.
    if (on) node.removeAttribute('hidden'); else node.setAttribute('hidden', '');
  }
  function text(id, value) { $(id).textContent = typeof value === 'string' ? value : ''; }
  function line(id, value) { text(id, value); visible(id, !!value); }
  function state(value) {
    if (ui.dataset.state !== value) { clearTimeout(overlayTimer); ui.dataset.overlay = 'hidden'; }
    ui.dataset.state = value;
  }
  function isAudio(meta) { return !!meta && ['song', 'track', 'album', 'audiobook', 'audiobook_file', 'chapter'].indexOf(meta.kind) !== -1; }
  function identity(meta) {
    meta = meta || {};
    return JSON.stringify([meta.title, meta.subtitle, meta.seriesTitle, meta.season, meta.episode, meta.artist, meta.albumTitle, meta.kind]);
  }
  function episodeLine(id, meta, loading) {
    var node = $(id); node.replaceChildren();
    var isEpisode = meta && meta.kind === 'episode' && meta.seriesTitle;
    node.className = loading ? (isEpisode ? 'epl2' : 'sub') : 'epl';
    if (isEpisode) {
      if (Number.isSafeInteger(meta.season) && meta.season >= 0 && Number.isSafeInteger(meta.episode) && meta.episode >= 0) {
        var code = document.createElement('span'); code.textContent = t('episodeLabel', {season: meta.season, episode: meta.episode}); node.appendChild(code);
        var gap = document.createElement('i'); gap.className = 'gap'; node.appendChild(gap);
      }
      if (meta.title) { var title = document.createElement('b'); title.textContent = meta.title; node.appendChild(title); }
    } else if (meta) { node.textContent = isAudio(meta) ? meta.artist || '' : meta.subtitle || ''; }
    node.hidden = !node.textContent;
  }
  function renderIdentity(prefix, meta) {
    meta = meta || {};
    text(prefix + '-title', meta.kind === 'episode' && meta.seriesTitle ? meta.seriesTitle : meta.title || '');
    episodeLine(prefix + '-subtitle', meta, prefix === 'loading');
  }
  function artwork(prefix, url, square) {
    var art = $(prefix + '-art'), ambient = $(prefix + '-ambient'), tile = $(prefix + '-art-tile');
    // The engine resolves protected artwork with its own bearer. This port never fetches it.
    if (url) {
      if (art.getAttribute('src') !== url) art.src = url;
      if (ambient.getAttribute('src') !== url) ambient.src = url;
    } else { art.removeAttribute('src'); ambient.removeAttribute('src'); }
    art.hidden = !url; ambient.hidden = !url;
    visible(prefix + '-shade', !!url); visible(prefix + '-glyph', !url);
    tile.classList.toggle('none', !url); tile.classList.toggle('sq', !!square);
    $(prefix + '-glyph').firstElementChild.setAttribute('href', square ? '#i-music' : '#i-tv');
    art.onerror = function () { if (art.getAttribute('src') === url) artwork(prefix, '', square); };
  }
  function idle() {
    clearTimeout(overlayTimer); metadata = null; image = ''; lastIdentity = ''; lastState = '';
    artwork('loading', '', false); artwork('playing', '', true);
    takeover(null); notice(''); state('idle');
  }
  function loading(meta) {
    metadata = meta || null; image = ''; lastState = ''; lastIdentity = '';
    state('loading'); visible('loading-wait', false); renderIdentity('loading', metadata);
    line('loading-album', metadata && isAudio(metadata) ? metadata.albumTitle : '');
    artwork('loading', '', isAudio(metadata));
  }
  function preparing(elapsedMs) {
    // elapsedMs is supplied only after an actual 202; no view-owned preparation timer.
    visible('loading-wait', ui.dataset.state === 'loading' && Number.isFinite(elapsedMs) && elapsedMs >= 15000);
  }
  function showArtwork(url, square) { image = typeof url === 'string' ? url : ''; artwork('loading', image, square); }
  function clock(value) {
    var sec = Math.max(0, Math.floor(value)), hours = Math.floor(sec / 3600);
    var minutes = Math.floor(sec / 60) % 60, seconds = String(sec % 60).padStart(2, '0');
    return hours ? hours + ':' + String(minutes).padStart(2, '0') + ':' + seconds : minutes + ':' + seconds;
  }
  function reveal(audio, playingState) {
    clearTimeout(overlayTimer); ui.dataset.overlay = 'visible';
    if (!audio && playingState === 'playing') overlayTimer = setTimeout(function () { ui.dataset.overlay = 'hidden'; }, OVERLAY_VISIBLE_MS);
  }
  function playing(observation) {
    observation = observation || {};
    var previousIdentity = identity(metadata);
    if (observation.meta !== undefined) metadata = observation.meta;
    // An ordinary queue advance may have no next-title artwork yet. The prior
    // occurrence's resolved cover is never a fact about the new occurrence.
    if (identity(metadata) !== previousIdentity && observation.artworkUrl === undefined) image = '';
    if (observation.artworkUrl !== undefined) image = observation.artworkUrl || '';
    var audio = isAudio(metadata), nextIdentity = identity(metadata);
    var playingState = ['playing', 'paused', 'buffering'].indexOf(observation.state) !== -1 ? observation.state : 'playing';
    var changed = ui.dataset.state !== 'playing' || nextIdentity !== lastIdentity || playingState !== lastState;
    state('playing'); $('playing').dataset.audio = String(audio); $('playing').dataset.paused = String(playingState === 'paused');
    renderIdentity('playing', metadata);
    text('audio-title', metadata && metadata.title || '');
    line('audio-artist', metadata && metadata.artist); line('audio-album', metadata && metadata.albumTitle);
    artwork('playing', image, true);
    var at = observation.positionSeconds, duration = observation.durationSeconds;
    var knownAt = Number.isFinite(at) && at >= 0, knownDuration = Number.isFinite(duration) && duration >= 0;
    visible('time-played', knownAt); text('time-played', knownAt ? clock(at) : '');
    visible('time-left', knownAt && knownDuration); text('time-left', knownAt && knownDuration ? '−' + clock(Math.max(0, duration - at)) : '');
    visible('progress-track', knownAt && knownDuration && duration > 0);
    $('progress-fill').style.width = knownAt && knownDuration && duration > 0 ? Math.min(100, at / duration * 100) + '%' : '0%';
    visible('state-pill', playingState !== 'playing'); visible('state-pause', playingState === 'paused'); visible('state-buffering', playingState === 'buffering');
    $('state-pill').classList.toggle('buf', playingState === 'buffering');
    text('state-label', playingState === 'paused' ? t('paused') : playingState === 'buffering' ? t('buffering') : '');
    if (changed || observation.revealOverlay === true) reveal(audio, playingState);
    lastIdentity = nextIdentity; lastState = playingState;
  }
  function error(kind) {
    if (['unavailable', 'busy', 'unreachable', 'decode', 'generic'].indexOf(kind) === -1) kind = 'generic';
    state('error'); takeover(null); notice(''); text('error-title', t('error.' + kind + '.title')); text('error-body', t('error.' + kind + '.body'));
  }
  function takeover(prompt) {
    clearInterval(takeoverTimer); takeoverTimer = null; visible('takeover', !!prompt);
    if (!prompt) { promptIdentity = ''; promptDuration = 0; text('takeover-title', ''); text('takeover-body', ''); return; }
    text('takeover-title', t('takeover.title', {requester: prompt.requester || t('someone')}));
    text('takeover-body', t(prompt.controllerPresent ? 'takeover.present' : 'takeover.absent'));
    visible('takeover-keys', prompt.remoteKeysAvailable === true);
    var duration = Number.isFinite(prompt.timeoutMs) && prompt.timeoutMs > 0 ? prompt.timeoutMs : 0;
    var nextPromptIdentity = JSON.stringify([prompt.requester, prompt.controllerPresent]);
    // Key-observation updates carry the authority's remaining deadline. Keep the
    // full visual denominator; a capability update must not refill the line.
    if (nextPromptIdentity !== promptIdentity || duration >= promptDuration) promptDuration = duration;
    promptIdentity = nextPromptIdentity;
    var started = Date.now();
    function drain() { var left = promptDuration ? Math.max(0, (duration - (Date.now() - started)) / promptDuration) : 0; $('takeover-remaining').style.width = left * 100 + '%'; }
    drain(); if (duration) takeoverTimer = setInterval(drain, 100);
  }
  function notice(message) {
    // Binding listening ruling b4: transient recovery is silent. Only drawn actionable failures.
    var allowed = !!message && (message === t('notice.seekFailed') || message === t('notice.stopFailed'));
    visible('notice', allowed); text('notice-message', allowed ? message : '');
  }
  function pairingStatus(message, tone) { text('status', message); if (tone) $('status').dataset.tone = tone; else delete $('status').dataset.tone; }
  function browserPair(show) { visible('browser-pair', show === true); }
  function splash() { state('splash'); }
  function fit() { ui.style.setProperty('--receiver-scale', Math.min(window.innerWidth / 1920, window.innerHeight / 1080)); }
  document.querySelectorAll('[data-text]').forEach(function (node) { node.textContent = t(node.dataset.text); });
  $('browser-pair').setAttribute('aria-label', t('pair.label'));
  var marker = '\uFDD0', hint = t('idle.hint', {playOn: marker});
  hint.split(marker).forEach(function (part, index) {
    if (index) { var emphasis = document.createElement('b'); emphasis.textContent = t('idle.playOn'); $('idle-hint').appendChild(emphasis); }
    $('idle-hint').appendChild(document.createTextNode(part));
  });
  window.addEventListener('resize', fit); fit();
  window.addEventListener('pagehide', function () { clearTimeout(overlayTimer); clearInterval(takeoverTimer); });
  window.porticoCastView = Object.freeze({idle: idle, loading: loading, artwork: showArtwork, preparing: preparing, playing: playing, takeover: takeover, notice: notice, error: error, pairingStatus: pairingStatus, browserPair: browserPair, splash: splash});
  idle();
})();
