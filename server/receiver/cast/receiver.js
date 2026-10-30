/*
 * Portico Cast receiver.
 *
 * The page is served by the Portico server it talks to (and, unchanged, at
 * cast.getportico.tv for every server). The receiver framework script from
 * Google and its documented CAF player dependencies are the external resources.
 *
 * Pairing: a sender asks its server for a one-use code (POST /v1/cast/bootstrap)
 * and delivers it over the `urn:x-cast:tv.getportico.cast` channel (a Cast device
 * has no keyboard); this page redeems it (POST /v1/cast/redeem) for two
 * credentials:
 *
 *   deviceToken  device-scoped, kept in localStorage, accepted only by
 *                POST /v1/cast/reconnect, rotated on every reconnect; used here
 *                only to renew an expired access token mid-session
 *   accessToken  an ordinary short-lived viewer bearer
 *
 * Control (SEC-14, CAST-04, D-FEAT-10): the sender that paired is the
 * controller. Only it may load, play, seek, change tracks or read status; any
 * sender may pause or stop (anyone in the room can do that with the remote).
 * A different sender that pairs while something is playing doesn't interrupt
 * it: the TV shows who is asking, the controller is asked, and the TV switches
 * only if the controller allows it, stops, or has left (then after 10 seconds).
 * The same profile on the same server switches straight away. The old
 * pairing is kept until the new one is adopted, and nothing is sent to other
 * senders but "busy".
 *
 * Sender protocol:
 *   pair  {displayName}                         → paired | pair-pending | pair-failed {code: 'tv_busy' | …}
 *   load  {title, subtitle, seriesTitle, season, episode, kind, artworkPath, subtitleTrackId, versionId, audioTrackId}
 *   controller receives takeover-requested {requester} and replies takeover {allow}
 *   a replaced controller receives {type: 'replaced'}
 *
 * Playback uses Playback v1, the session contract every Portico client plays through:
 * POST /v1/playback/sessions, the presentation URL it answers with, timeline reports
 * (progress and lease renewal), PATCH for a seek the server must produce, DELETE to end.
 * CAF owns media loading, its standard player draws playback (V-11), and the
 * overlays in index.html cover idle, preparing, errors and takeover requests.
 * Pairing credentials never enter media URLs or sender messages.
 */
(function () {
  'use strict';

  var STORAGE_KEY = 'portico.cast.deviceToken';
  var PROTOCOL = '1.0';
  var PROGRESS_INTERVAL_MS = 10000;
  var NAMESPACE = 'urn:x-cast:tv.getportico.cast';
  var ORIGIN_KEY = 'portico.cast.serverOrigin';
  var TAKEOVER_ASK_MS = 30000;     // controller present: no answer means no
  var TAKEOVER_ABSENT_MS = 10000;  // controller gone: the TV says who is taking over, then switches
  var ERROR_VISIBLE_MS = 20000;

  // Which Portico server this receiver talks to. Served by a server, that is the server itself.
  // Served from the shared page at cast.getportico.tv, the sender names the viewer's server when
  // it pairs; only an HTTPS origin is accepted, because a Cast device will not load anything else.
  var base = '';
  function serverOrigin(value) {
    if (!value) { return ''; }
    try {
      var url = new URL(String(value));
      if (url.protocol !== 'https:' || url.username || url.password) { return null; }
      return url.origin === window.location.origin ? '' : url.origin;
    } catch (e) { return null; }
  }
  try { base = serverOrigin(window.localStorage.getItem(ORIGIN_KEY)) || ''; } catch (e) { base = ''; }
  var broadcast = function () {};

  // --- Presentation ---------------------------------------------------------

  function $(id) { return document.getElementById(id); }
  var ui = $('ui');
  var status = $('status');
  var errorTimer = null, noticeTimer = null;
  var viewGeneration = 0, viewLoading = false, pictureReady = false, viewBuffering = false;
  var currentArtwork = '', preparingSince = null, preparingTimer = null, observedRemoteKeys = false, activeTrackKey = null;

  // The view owns drawing only. No credential, session, grant or control callback crosses this port.
  function viewCall(method, args) {
    var view = window.porticoCastView;
    if (!view || typeof view[method] !== 'function') return false;
    try { view[method].apply(view, args || []); return true; } catch (e) { return false; }
  }
  // The composed receiver loads its generated canonical catalogue before this engine.
  function copy(key, params) {
    var catalogue = window.porticoCastText;
    return catalogue && typeof catalogue.t === 'function' ? catalogue.t('cast.receiver.' + key, params) : '';
  }
  function remoteKeysAvailable() {
    try { if (castContext && castContext.getDeviceCapabilities().dpad_input_supported === true) return true; } catch (e) { /* unknown */ }
    return observedRemoteKeys;
  }
  function stopPreparing() { clearTimeout(preparingTimer); preparingTimer = null; preparingSince = null; }
  function preparing() {
    if (preparingSince !== null) return;
    preparingSince = Date.now();
    var generation = viewGeneration;
    function tick() {
      if (generation !== viewGeneration || preparingSince === null) return;
      viewCall('preparing', [Math.max(0, Date.now() - preparingSince)]);
      preparingTimer = setTimeout(tick, 1000);
    }
    tick();
  }
  function resetViewPlayback() {
    stopPreparing(); viewGeneration++; viewLoading = false; pictureReady = false; viewBuffering = false;
    currentArtwork = ''; activeTrackKey = null;
  }
  function clockFacts() {
    var facts = {};
    if (!playback || !manager) return facts;
    try { var at = manager.getCurrentTimeSec(); if (typeof at === 'number' && Number.isFinite(at) && at >= 0) facts.positionSeconds = at; } catch (e) { /* unknown */ }
    try { var length = typeof manager.getDurationSec === 'function' ? manager.getDurationSec() : undefined; if (typeof length === 'number' && Number.isFinite(length) && length >= 0) facts.durationSeconds = length; } catch (e) { /* unknown */ }
    return facts;
  }
  function playingView(revealOverlay) {
    if (!pictureReady || !playback || !manager) return;
    var state = viewBuffering ? 'BUFFERING' : manager.getPlayerState();
    if (['PLAYING','PAUSED','BUFFERING'].indexOf(state) < 0) return;
    // Observe selected tracks rather than treating a requested track change as applied.
    try {
      var audio = manager.getAudioTracksManager(), textTracks = manager.getTextTracksManager();
      var key = JSON.stringify([typeof audio.getActiveId === 'function' ? audio.getActiveId() : null, typeof textTracks.getActiveIds === 'function' ? textTracks.getActiveIds() : null]);
      if (activeTrackKey !== null && key !== activeTrackKey) revealOverlay = true;
      activeTrackKey = key;
    } catch (e) { /* no track observation */ }
    var snapshot = Object.assign({meta: meta ? Object.assign({},meta) : null, state: state.toLowerCase(), remoteKeysAvailable: remoteKeysAvailable()}, clockFacts());
    if (currentArtwork) snapshot.artworkUrl = currentArtwork;
    if (revealOverlay === true) snapshot.revealOverlay = true;
    viewCall('playing', [snapshot]);
  }

  function show(state) { if (ui && ui.setAttribute) ui.setAttribute('data-state', state); }
  function text(id, value) { var el = $(id); if (el) el.textContent = value || ''; }
  function visible(id, on) { var el = $(id); if (el) el.hidden = !on; }
  function say(message, tone) {
    if (viewCall('pairingStatus', [message || '', tone])) return;
    if (!status) return;
    status.textContent = message || '';
    if (tone) { status.setAttribute('data-tone', tone); } else { status.removeAttribute('data-tone'); }
  }
  /** An approved notice over whatever is on screen. Transient recovery is silent. */
  function notice(message, ms) {
    clearTimeout(noticeTimer);
    if (viewCall('notice', [message || ''])) {
      if (message && ms) noticeTimer = setTimeout(function () { viewCall('notice', ['']); }, ms);
      return;
    }
    text('notice', message); visible('notice', !!message);
    if (message && ms) noticeTimer = setTimeout(function () { visible('notice', false); }, ms);
  }
  function showIdle() { clearTimeout(errorTimer); resetViewPlayback(); if (!viewCall('idle')) show('idle'); }
  function showLoading(meta) {
    clearTimeout(errorTimer);
    resetViewPlayback(); viewLoading = true;
    if (window.porticoCastView && typeof window.porticoCastView.loading === 'function' && !(meta && meta.title)) { viewLoading = false; return; }
    if (viewCall('loading', [meta ? Object.assign({},meta) : null])) return;
    text('loading-eyebrow', copy('gettingReady'));
    text('loading-title', meta && meta.title || '');
    text('loading-subtitle', meta && (meta.subtitle || meta.seriesTitle) || '');
    visible('loading-art', false);
    show('loading');
  }
  function showArtwork(url, square) {
    if (!url) return;
    currentArtwork = url;
    if (viewCall('artwork', [url, !!square])) return;
    var art = $('loading-art');
    if (!art || !url) return;
    art.src = url;
    if (art.classList) art.classList.toggle('square', !!square);
    art.hidden = false;
  }
  function showPlaying() { clearTimeout(errorTimer); if (window.porticoCastView && typeof window.porticoCastView.playing === 'function') { playingView(); return; } show('playing'); }
  function errorKind(code) {
    code = String(code || '');
    if (/not_found|unavailable|forbidden|not_permitted|permission/.test(code)) return 'unavailable';
    if (/busy|capacity|cap$|occupied|timeout/.test(code)) return 'busy';
    if (/network|fetch|http_5|unreach|abort/.test(code)) return 'unreachable';
    if (/decode|stalled|playback_failed/.test(code)) return 'decode';
    return 'generic';
  }
  /** A visible error card: the TV always says what went wrong (CAST-01). Never shows raw codes. */
  function showError(code) {
    var resumePicture = pictureReady && !!playback;
    stopPreparing(); viewGeneration++; pictureReady = false; viewLoading = false;
    if (!viewCall('error', [errorKind(code)])) {
      text('error-title', copy('error.' + errorKind(code) + '.title'));
      text('error-body', copy('error.' + errorKind(code) + '.body'));
      show('error');
    }
    clearTimeout(errorTimer);
    // The card stays 20 seconds, then returns to what was on screen: the picture
    // when something is still playing, otherwise idle.
    errorTimer = setTimeout(function () {
      if (!playback) { showIdle(); return; }
      var playingState = manager ? manager.getPlayerState() : 'IDLE';
      if (resumePicture && ['PLAYING','PAUSED','BUFFERING'].indexOf(playingState) >= 0) { pictureReady = true; showPlaying(); return; }
      // Nothing is playing: the failed occurrence ends with its card instead of leaving a
      // session (and any transcode behind it) open under an idle screen, and the controller
      // is told the title failed here so it can offer the retry the card names.
      if (controllerSender) send(controllerSender, {type: 'load-failed', code: 'playback_failed'});
      serial(function () { return stopPlayback().catch(function () {}); }).then(broadcast).catch(function () {});
    }, ERROR_VISIBLE_MS);
  }

  // --- Credentials and requests --------------------------------------------

  var session = null; // {accessToken, expiresAt}
  var scope = null;   // {accountId, profileId, authority, capabilities}
  // The Playback v1 session playing here: {id, revision, itemId, generation, serverSeek}.
  var playback = null;
  var shuttingDown = false, activeRequests = new Set();
  var mediaAddresses = new Map(); // bounded receiver-private addresses; never placed in CAF status
  function mediaAddress(request) { return request && request.media ? mediaAddresses.get(request.media.contentId) || '' : ''; }
  function queuePath(id) { return '/v1/queues/' + encodeURIComponent(id); }
  function queueRef(value) { return value && typeof value.queueId === 'string' && typeof value.entryId === 'string' && /^[A-Za-z0-9._:-]{1,128}$/.test(value.queueId) && /^[A-Za-z0-9._:-]{1,128}$/.test(value.entryId) && (value.transferId===undefined||typeof value.transferId==='string'&&/^[A-Za-z0-9._:-]{1,128}$/.test(value.transferId)) ? {queueId:value.queueId,entryId:value.entryId,...(value.transferId?{transferId:value.transferId}:{})} : null; }
  var transferStarts=new Map(); // bounded private request/ACK identity, never exposed in CAF status
  var manager = null, castContext = null, profiled = false;
  // CAF routes PlayerManager's own calls through the same interceptors as the
  // media channel. Only these exact engine-created objects may bypass sender
  // gates; an external JSON request cannot reproduce their private identity.
  var localRequests = new WeakSet(), localRequestId = 0;
  var cafOccurrence = null,platformOccurrence=null;
  function selectedTracks() {
    var selected = {};
    if (!manager) return selected;
    var audio = manager.getAudioTracksManager(), textTracks = manager.getTextTracksManager();
    if (typeof audio.getActiveId === 'function') {
      var id = audio.getActiveId();
      if (Number.isSafeInteger(id) && id >= 0) selected.audio = id;
    }
    if (typeof textTracks.getActiveIds === 'function') {
      var ids = textTracks.getActiveIds();
      if (Array.isArray(ids) && ids.length <= 1 && ids.every(function(id){return Number.isSafeInteger(id) && id >= 0;}) && (ids.length || textTracks.getTracks().length)) selected.text = ids.slice();
    }
    return selected;
  }
  function restoreTracks(occurrence) {
    var selected = occurrence && occurrence.tracks, media = manager && manager.getMediaInformation();
    if (!selected || platformOccurrence !== occurrence || !playback || playback.id !== occurrence.id || playback.generation !== occurrence.generation || !media || media.contentId !== occurrence.contentId) return;
    var audio = manager.getAudioTracksManager(), textTracks = manager.getTextTracksManager();
    if (selected.audio !== undefined && audio.getTracks().some(function(track){return track.trackId === selected.audio;})) {
      audio.setActiveById(selected.audio); delete selected.audio;
    }
    if (selected.text !== undefined && selected.text.every(function(id){return textTracks.getTracks().some(function(track){return track.trackId === id;});})) {
      textTracks.setActiveByIds(selected.text); delete selected.text;
    }
  }
  async function loadMedia(request,lease,attempt,selected) {
    requireLease(lease);
    // Bind the single CAF deck to the exact accepted server presentation.
    var owner=playback, occurrence={id:owner&&owner.id,generation:owner&&owner.generation,contentId:request.media.contentId,finished:false,tracks:selected&&Object.assign({},selected)};
    if (selected && selected.text !== undefined && request.media.tracks) request.activeTrackIds = selected.text.filter(function(id){return request.media.tracks.some(function(track){return track.trackId === id;});});
    cafOccurrence=null;platformOccurrence=occurrence;if(attempt)attempt.caf=occurrence;localRequests.add(request);
    await manager.load(request);
    requireLease(lease);
    if(playback===owner){cafOccurrence=occurrence;restoreTracks(occurrence);}
  }
  function finishedOccurrence(owner, occurrence) {
    var media=manager&&manager.getMediaInformation(),element=$('player');
    return !!owner&&!!occurrence&&cafOccurrence===occurrence&&owner.id===occurrence.id&&owner.generation===occurrence.generation&&
      !!media&&media.contentId===occurrence.contentId&&!!element&&element.ended===true;
  }
  function mediaCommand(type, fields) {
    var request=Object.assign({type:type,requestId:++localRequestId},fields);
    localRequests.add(request);manager.sendLocalMediaRequest(request);
  }

  var observer = null, reportTimer = null, reportFailures = 0, recoveries = 0, recovering = null, networkRetries = 0, networkRetryPosition = 0, bufferingTimer = null;
  var commands = Promise.resolve();
  function serial(work) { var result = commands.then(work); commands = result.catch(function () {}); return result; }
  function token(bytes) { var a = new Uint8Array(bytes || 24); window.crypto.getRandomValues(a); return Array.from(a, function (x) { return x.toString(16).padStart(2, '0'); }).join(''); }
  function position() { return manager ? manager.getCurrentTimeSec() || 0 : 0; }
  function paused() { return !manager || manager.getPlayerState() === 'PAUSED'; }
  function sessionPath(id) { return '/v1/playback/sessions/' + encodeURIComponent(id); }
  function wait(ms) { return new Promise(function (resolve) { setTimeout(resolve, ms); }); }

  function readToken() {
    try { return window.localStorage.getItem(STORAGE_KEY) || ''; } catch (e) { return ''; }
  }
  function writeToken(value) {
    try {
      if (value) { window.localStorage.setItem(STORAGE_KEY, value); }
      else { window.localStorage.removeItem(STORAGE_KEY); }
    } catch (e) { /* a receiver with storage disabled simply re-pairs each boot */ }
  }

  // deviceId is stable for the life of the stored pairing. A receiver that has
  // been unpaired generates a new one, so an old device row is never reused.
  function deviceId() {
    var key = 'portico.cast.deviceId';
    var value = '';
    try { value = window.localStorage.getItem(key) || ''; } catch (e) { /* ignore */ }
    if (!value) {
      var bytes = new Uint8Array(16);
      (window.crypto || window.msCrypto).getRandomValues(bytes);
      value = 'cast-';
      for (var i = 0; i < bytes.length; i++) { value += bytes[i].toString(16).padStart(2, '0'); }
      try { window.localStorage.setItem(key, value); } catch (e) { /* ignore */ }
    }
    return value;
  }

  // Each command keeps its original SDK connection, adoption and credential.
  // A queued job never borrows whichever receiver happens to be current later.
  var controllerGeneration=0,adoptionGeneration=0,senderConnections=new Map(),disconnectedSenders=new Set(),connectionSequence=0;
  function bumpConnection(senderId){senderConnections.delete(senderId);senderConnections.set(senderId,++connectionSequence);while(senderConnections.size>64){var oldest=senderConnections.keys().next().value;senderConnections.delete(oldest);disconnectedSenders.delete(oldest);}}
  function connectionGeneration(senderId){if(!senderConnections.has(senderId))bumpConnection(senderId);return senderConnections.get(senderId);}
  function capturePairConnection(senderId){
    // senderId is supplied by the SDK channel event, never by message JSON.
    if(disconnectedSenders.has(senderId)||!castContext||typeof castContext.getSender!=='function'||!castContext.getSender(senderId))return null;
    return Object.freeze({senderId:senderId,generation:connectionGeneration(senderId)});
  }
  function pairConnectionCurrent(witness){return !!witness&&!disconnectedSenders.has(witness.senderId)&&connectionGeneration(witness.senderId)===witness.generation;}
  function requirePairInstallation(installation,installed){
    if(!installation)return;
    if(!pairConnectionCurrent(installation.connection)||controllerGeneration!==installation.generation||installed&&(base!==installation.origin||session!==installation.result.session||scope!==installation.result.scope)){var e=new Error('controller_retired');e.retired=true;throw e;}
  }
  function captureController(senderId,adoption){return Object.freeze({senderId:senderId,adoption:!!adoption,
    connection:connectionGeneration(senderId),controllerGeneration:controllerGeneration,adoptionGeneration:adoptionGeneration,
    owner:controllerSender,binding:controllerBinding,proof:controllerToken,session:session,bearer:session&&session.accessToken,scope:scope,origin:base,occurrence:playback});}
  function leaseCurrent(lease){return !lease||!shuttingDown&&lease.origin===base&&!!session&&lease.session===session&&lease.bearer===session.accessToken&&lease.scope===scope&&lease.binding===controllerBinding&&lease.proof===controllerToken&&
    lease.connection===connectionGeneration(lease.senderId)&&lease.controllerGeneration===controllerGeneration&&
    (lease.adoption?lease.adoptionGeneration===adoptionGeneration&&lease.owner===controllerSender:controllerPresent&&lease.senderId===controllerSender);}
  function controllerStamp(lease){return lease&&lease.binding?{bindingId:lease.binding.id,controllerGeneration:lease.controllerGeneration,connectionGeneration:lease.connection}:null;}
  function occurrenceFact(owner){return owner?{sessionId:owner.id,generation:owner.generation,itemId:owner.itemId}:null;}
  function remoteIdentity(lease,owner){var controller=controllerStamp(lease),occurrence=occurrenceFact(owner);return controller&&occurrence?{controller:controller,occurrence:occurrence}:null;}
  function readRemoteIdentity(value){
    var c=value&&value.controller,o=value&&value.occurrence,id=function(v){return typeof v==='string'&&/^[A-Za-z0-9._:-]{1,128}$/.test(v);},generation=function(v){return Number.isSafeInteger(v)&&v>0;};
    if(!c||!o||!id(c.bindingId)||!generation(c.controllerGeneration)||!generation(c.connectionGeneration)||!id(o.sessionId)||!id(o.itemId)||!generation(o.generation))return null;
    return {controller:{bindingId:c.bindingId,controllerGeneration:c.controllerGeneration,connectionGeneration:c.connectionGeneration},occurrence:{sessionId:o.sessionId,generation:o.generation,itemId:o.itemId}};
  }
  function sameControllerStamp(a,b){return !!a&&!!b&&a.bindingId===b.bindingId&&a.controllerGeneration===b.controllerGeneration&&a.connectionGeneration===b.connectionGeneration;}
  function sameRemoteIdentity(a,b){return !!a&&!!b&&sameControllerStamp(a.controller,b.controller)&&!!a.occurrence&&!!b.occurrence&&a.occurrence.sessionId===b.occurrence.sessionId&&a.occurrence.generation===b.occurrence.generation&&a.occurrence.itemId===b.occurrence.itemId;}
  function requireLease(lease){if(!leaseCurrent(lease)){var e=new Error('controller_retired');e.retired=true;throw e;}}
  function commandQueue(lease,work){return serial(function(){requireLease(lease);if(lease&&playback!==lease.occurrence)throw new Error('occurrence_replaced');return work();});}
  async function cleanupResource(owned,witness){
    // Final refusal stays final. Never renew, get latest, send progress or
    // downgrade this captured original generation to an ordinary Stop.
    if(!witness||!owned||!Number.isSafeInteger(owned.generation)||owned.generation<1)return;
    await request(sessionPath(owned.id),{method:'DELETE',origin:witness.origin,bearer:witness.bearer,body:{expectedGeneration:owned.generation}}).catch(function(){});
  }
  async function cleanupOccurrence(owned,lease){
    if(playback&&owned&&playback.id===owned.id)return;
    await cleanupResource(owned,lease);
  }
  async function cleanupFailedLoad(attempt){
    if(!attempt||attempt.adopted||attempt.cleaned||!attempt.owner||!attempt.owned)return;
    attempt.cleaned=true;
    // A distinct local owner may already hold this exact durable resource.
    // Never retire that installed handoff as the failed attempt's cleanup.
    if(playback&&playback!==attempt.owner&&playback.id===attempt.owned.id&&playback.generation===attempt.owned.generation)return;
    if(playback===attempt.owner){
      playback=null;meta=null;
      var oldReport=reportTimer,oldBuffering=bufferingTimer;
      reportTimer=null;bufferingTimer=null;
      if(cafOccurrence===attempt.caf)cafOccurrence=null;
      if(platformOccurrence===attempt.caf)platformOccurrence=null;
      clearTimeout(oldReport);clearTimeout(oldBuffering);
    }
    if(attempt.contentId)mediaAddresses.delete(attempt.contentId);
    await cleanupResource(attempt.owned,attempt.witness);
  }
  /** One request, bounded in time and size. Resolves {status, body} for 2xx; throws with
   * {code, status, current} otherwise (`current` is a 412's session). */
  async function request(path, options) {
    if(shuttingDown)throw new Error('receiver_shutdown');
    // This receiver reports playback with observations; the server refuses playback to one that doesn't.
    var headers = Object.assign({'Content-Type': 'application/json', 'Portico-Playback': '2'}, options.headers || {});
    if (options.bearer) headers.Authorization = 'Bearer ' + options.bearer;
    var abort = new AbortController(), timer = setTimeout(function () { abort.abort(); }, options.timeoutMs || 15000);
    var active={abort:abort,timer:timer};activeRequests.add(active);
    try {
      var response = await fetch((options.origin === undefined ? base : options.origin) + path, {method: options.method || 'POST', headers: headers, signal: abort.signal, body: options.body === undefined ? undefined : JSON.stringify(options.body)});
      var responseHeaders={};response.headers.forEach(function(value,name){responseHeaders[name]=value;});
      var payload;
      try {
      var text = '';
      if (response.body) {
        var reader = response.body.getReader(), decoder = new TextDecoder(), size = 0;
        try { for (;;) { var chunk = await reader.read(); if (chunk.done) break; size += chunk.value.byteLength; if (size > 1048576) throw new Error('response_too_large'); text += decoder.decode(chunk.value, {stream: true}); } text += decoder.decode(); } finally { reader.releaseLock(); }
      }
      payload = text ? JSON.parse(text) : {};
      } catch(error) {
        if(!options.raw||!(response.status>=200&&response.status<300)&&response.status<500&&response.status!==429)throw error;
        // Unreadable accepted/proxy responses keep same-key lookup custody.
        payload=undefined;
      }
      if (!response.ok && !options.raw) {
        var failure = payload.error || {};
        var error = new Error(failure.code || 'http_' + response.status); error.code = error.message; error.status = response.status; error.current = failure.current;
        throw error;
      }
      return {status: response.status, body: payload, headers:responseHeaders};
    } finally { activeRequests.delete(active);clearTimeout(timer);abort.abort(); }
  }
  function post(path, body, bearer, origin) { return request(path, {body: body, bearer: bearer, origin: origin}).then(function (r) { return r.body; }); }
  /** A request with the receiver's bearer. An expired bearer is renewed once through the device
   * token; a transport failure or 5xx is retried once with the identical request (the same
   * Idempotency-Key, If-Match or timeline sequence), which the server treats as the same request. */
  var PAIRING_ENDED = ['cast_device_revoked', 'cast_device_not_found', 'cast_device_expired'];
  var renewalFlight=null,renewalOffer=null,renewedStampRequired=false;
  function sameRenewalBinding(oldBinding,nextBinding){
    if(!oldBinding||!nextBinding||!oldBinding.scope||!nextBinding.scope)return false;
    return ['id','bootstrapId','receiverId','targetId'].every(function(k){return oldBinding[k]===nextBinding[k];})&&
      ['serverId','authority','accountId','profileId','senderDeviceId','senderFamilyId'].every(function(k){return oldBinding.scope[k]===nextBinding.scope[k];})&&
      typeof nextBinding.targetFamilyId==='string'&&nextBinding.targetFamilyId!==oldBinding.targetFamilyId&&/^\d+$/.test(nextBinding.targetGeneration)&&Number(nextBinding.targetGeneration)>Number(oldBinding.targetGeneration);
  }
  async function renewReceiver(credential,origin,originalScope,originalToken){
    if(renewalFlight){if(renewalFlight.credential!==credential)throw new Error('receiver_replaced');return renewalFlight.promise;}
    var sender=controllerSender,connection=sender&&capturePairConnection(sender),binding=controllerBinding,proof=controllerToken,generation=controllerGeneration,wasPresent=controllerPresent;
    var flight={credential:credential};renewalFlight=flight;
    flight.promise=(async function(){
      var renewed;
      try{renewed=await post('/v1/cast/reconnect',{protocolVersion:PROTOCOL,deviceToken:originalToken,deviceId:deviceId()},undefined,origin);}
      catch(refused){if(PAIRING_ENDED.indexOf(refused.code)>=0)refused.pairingEnded=true;throw refused;}
      if(session!==credential||base!==origin||scope!==originalScope)throw new Error('receiver_replaced');
      if(!renewed.session||!renewed.session.accessToken||!renewed.deviceToken||!renewed.scope||renewed.scope.accountId!==originalScope.accountId||renewed.scope.profileId!==originalScope.profileId||renewed.scope.authority!==originalScope.authority||JSON.stringify(renewed.scope.capabilities)!==JSON.stringify(originalScope.capabilities)){var changed=new Error('pairing_scope_changed');changed.pairingEnded=true;throw changed;}
      var same=wasPresent&&connection&&pairConnectionCurrent(connection)&&sender===controllerSender&&binding===controllerBinding&&proof===controllerToken&&generation===controllerGeneration;
      var previousStamp=same?controllerStamp(captureController(sender,false)):null;
      controllerGeneration++;adoptionGeneration++;controllerPresent=false;renewalOffer=null;renewedStampRequired=true;
      session=renewed.session;scope=renewed.scope;writeToken(renewed.deviceToken);
      if(same&&binding&&proof){
        try{
          var checked=await post('/v1/cast/controllers/check',{bindingId:binding.id,controlToken:proof},session.accessToken,origin);
          if(session!==renewed.session||scope!==renewed.scope||base!==origin||controllerSender!==sender||controllerBinding!==binding||controllerToken!==proof||controllerGeneration!==generation+1||!pairConnectionCurrent(connection)||!sameRenewalBinding(binding,checked.binding))throw new Error('controller_retired');
          var next=checked.binding,offer={id:'renew-'+token(16),sender:sender,connection:connection,binding:next,proof:proof,session:session,scope:scope,origin:base,generation:controllerGeneration};
          controllerBinding=next;renewalOffer=offer;
          offer.timer=setTimeout(function(){if(renewalOffer===offer){renewalOffer=null;send(sender,{type:'pair-required'});}},15000);
          send(sender,{type:'renewal-offer',attemptId:offer.id,binding:next,previousController:previousStamp,controller:controllerStamp(captureController(sender,false))});
        }catch(_){if(session===renewed.session&&controllerSender===sender)send(sender,{type:'pair-required'});}
      }
      return renewed;
    })();
    try{return await flight.promise;}finally{if(renewalFlight===flight)renewalFlight=null;}
  }
  async function api(path, options, lease,installation) {
    options=options||{};requireLease(lease);requirePairInstallation(installation,true);
    var credential=lease?lease.session:session,origin=lease?lease.origin:base,originalScope=lease?lease.scope:scope;
    var originalToken=readToken();
    var attempt=async function(){
      requireLease(lease);requirePairInstallation(installation,true);
      if(!credential||(!lease&&(session!==credential||base!==origin||scope!==originalScope)))throw new Error('receiver_replaced');
      var answer=await request(path,Object.assign({},options,{origin:origin,bearer:lease?lease.bearer:credential.accessToken}));
      // Observe an actual response before a lifetime fence can discard it.
      if(options.observeAnswer)options.observeAnswer(answer);
      if(lease&&!leaseCurrent(lease)){
        var retired=new Error('controller_retired');retired.retired=true;
        throw retired;
      }
      requireLease(lease);requirePairInstallation(installation,true);
      if(!lease&&(session!==credential||base!==origin||scope!==originalScope))throw new Error('receiver_replaced');
      return answer;
    };
    try{return await attempt();}
    catch(e){
      if(e.retired)throw e;
      requireLease(lease);requirePairInstallation(installation,true);
      // A single attempt (an observation) is never sent twice: its 401 starts the renewal the
      // next one uses, and any other failure is simply replaced by the next sample.
      if(options.single){if(e.status===401&&originalToken&&!installation&&!options.noRenew){try{await renewReceiver(credential,origin,originalScope,originalToken);}catch(renewal){if(renewal&&renewal.pairingEnded)throw renewal;}}throw e;}
      // Never replay a command captured under the retired bearer. A fresh
      // command can follow the correlated renewal acknowledgment.
      if(e.status===401&&originalToken&&!installation&&!options.noRenew){
        await renewReceiver(credential,origin,originalScope,originalToken);
        if(lease){var retired=new Error('controller_retired');retired.retired=true;throw retired;}
        credential=session;originalScope=scope;return attempt();
      }
      if(e.retired||e.message==='receiver_replaced'||e.status&&e.status<500)throw e;
      return attempt();
    }
  }

  // Progressive decoder probes use their real media type, not an MP4 wrapper
  // for FLAC/Ogg/WAV. These are capabilities, never observed output formats.
  var progressiveAudioProbes = {aac:'audio/mp4; codecs="mp4a.40.2"',mp3:'audio/mpeg',opus:'audio/ogg; codecs="opus"',vorbis:'audio/ogg; codecs="vorbis"',flac:'audio/flac',pcm:'audio/wav; codecs="1"'};
  function buildProfile(context) {
    var caps = {};
    try { caps = context ? context.getDeviceCapabilities() || {} : {}; } catch (_) { /* no output facts */ }
    function supports(codec, width, height, fps) { try { var parts=codec.split('; codecs=');return !!context && context.canDisplayType(parts[0],parts[1]?parts[1].replace(/"/g,''):'',width,height,fps); } catch (_) { return false; } }
    var client = {family:'cast',form:'cast',platform:'cast',app:'portico-cast'};
    try {
      var info = context && typeof context.getDeviceInformation === 'function' ? context.getDeviceInformation() : null;
      if (info && typeof info['model-name'] === 'string' && info['model-name'].trim() && info['model-name'].trim().toLowerCase()!=='unknown') client.model=info['model-name'].trim().slice(0,64);
    } catch (_) { /* a codec probe is not a device identity */ }
    var fourK = supports('video/mp4; codecs="hev1.2.4.L153.B0"', 3840, 2160, 60);
    var ranges = ['sdr'];
    if (caps.is_hdr_supported === true) ranges.push('hdr10');
    var width = fourK ? 3840 : 1920, height = fourK ? 2160 : 1080;
    // Every Chromecast since the 2nd generation decodes H.264 High@4.2 1080p60; probe it rather than assume 30 fps (CAST-08).
    var h264At60 = supports('video/mp4; codecs="avc1.64002A"', 1920, 1080, 60);
    var video = [{codec:'h264',bitDepths:[8],maxWidth:1920,maxHeight:1080,maxLevel:h264At60?42:41,maxFrameRate:h264At60?60:30,dynamicRanges:['sdr'],evidence:h264At60?'probed':'declared'},{codec:'vp8',bitDepths:[8],maxWidth:1920,maxHeight:1080,maxFrameRate:30,dynamicRanges:['sdr'],evidence:'declared'}];
    [['hevc','hev1.2.4.L153.B0'],['vp9','vp09.02.10.10'],['av1','av01.0.08M.10']].forEach(function (pair) {
      if (supports('video/mp4; codecs="' + pair[1] + '"', width, height, 60)) video.push({codec:pair[0],bitDepths:[8,10],maxWidth:width,maxHeight:height,maxFrameRate:60,dynamicRanges:ranges.filter(function (x) {return x !== 'dolby_vision' || pair[0] === 'hevc';}),dolbyVisionProfiles:[],evidence:'probed'});
    });
    var hevc = video.filter(function(v) { return v.codec === 'hevc'; })[0];
    if (hevc && caps.is_dv_supported === true) {
      hevc.dolbyVisionProfiles = [5,8].filter(function(profile) { return supports('video/mp4; codecs="dvhe.0' + profile + '.07"', width, height, 30); });
      if (hevc.dolbyVisionProfiles.length) { hevc.dynamicRanges.push('dolby_vision'); ranges.push('dolby_vision'); }
    }
    // Do not turn a MIME/codec answer into a fabricated decoded channel count. The SDK's
    // passthrough flag is separate from ordinary E-AC-3 acceptance, notably on newer Streamers.
    var audio = [];
    [['aac','audio/mp4; codecs="mp4a.40.2"'],['mp3','audio/mpeg'],['opus','audio/ogg; codecs="opus"'],['vorbis','audio/ogg; codecs="vorbis"'],['flac','audio/flac'],['pcm','audio/wav; codecs="1"']].forEach(function(pair) {
      var probed = supports(pair[1]);
      // Keep a conservative declared operating point when this platform has no
      // exact rate query. Google's published FLAC ceiling is 96 kHz/24 bit;
      // the other defaults are app limits, not a claim about the output sink.
      if (probed || pair[0] === 'aac' || pair[0] === 'mp3') audio.push({codec:pair[0],maxChannels:2,maxSampleRate:pair[0]==='flac'?96000:48000,
        ...(pair[0]==='flac'?{maxBitDepth:24}:pair[0]==='pcm'?{maxBitDepth:16}:{}),evidence:probed?'mixed':'declared'});
    });
    var passthrough = [];
    [['ac3','ac-3',6],['eac3','ec-3',8]].forEach(function (pair) {
      if (supports('audio/mp4; codecs="' + pair[1] + '"')) {
        var atmos = pair[0] === 'eac3' && caps.is_dolby_atmos_supported === true;
        passthrough.push({codec:pair[0],variants:pair[0] === 'ac3' ? ['ac3'] : atmos ? ['eac3','eac3_joc'] : ['eac3'],maxChannels:pair[2],evidence:'probed',objectAudio:atmos ? ['atmos'] : []});
      }
    });
    if (caps.display_supported === false) passthrough = [];
    var fmp4Audio=audio.filter(function(a){return ({aac:'mp4a.40.2',mp3:'mp4a.69',flac:'fLaC',opus:'Opus'})[a.codec]&&supports('audio/mp4; codecs="'+({aac:'mp4a.40.2',mp3:'mp4a.69',flac:'fLaC',opus:'Opus'})[a.codec]+'"');}).map(function(a){return a.codec;}).concat(passthrough.map(function(a){return a.codec;}));
    var hlsAudio=audio.filter(function(a){return ['aac','mp3'].indexOf(a.codec)>=0;}).map(function(a){return a.codec;}).concat(passthrough.map(function(a){return a.codec;}));
    var names=video.map(function (v) {return v.codec;});
    var transports=[{transport:'direct',containers:['mp4','m4v','mov'],video:names.filter(function (c) {return c!=='vp9'&&c!=='vp8';}),audio:fmp4Audio},{transport:'hls_ts',containers:['mpegts'],video:['h264'],audio:hlsAudio},{transport:'hls_fmp4',containers:['fmp4'],video:names.filter(function (c) {return c!=='vp9'&&c!=='vp8';}),audio:fmp4Audio}];
    var webmAudio=audio.filter(function(a){return ['opus','vorbis'].indexOf(a.codec)>=0&&supports('audio/webm; codecs="'+a.codec+'"');}).map(function(a){return a.codec;});
    transports.push({transport:'direct',containers:['webm'],video:names.filter(function(c){return c==='vp8'||c==='vp9'||c==='av1';}),audio:webmAudio});
    // Each container tuple belongs only to the decoder that was admitted for it.
    // The previous Cartesian list wrongly declared, for example, PCM in Ogg and
    // FLAC in an unprobed MP4 container.
    audio.forEach(function(a){
      var containers=({aac:['aac','adts'],mp3:['mp3'],flac:['flac'],opus:['ogg','opus'],vorbis:['ogg'],pcm:['wav']})[a.codec];
      if(containers&&(supports(progressiveAudioProbes[a.codec])||a.codec==='aac'||a.codec==='mp3'))transports.push({transport:'direct',containers:containers,video:[],audio:[a.codec]});
      var mp4Codec=({aac:'mp4a.40.2',mp3:'mp4a.69',flac:'fLaC',opus:'Opus'})[a.codec];
      if(mp4Codec&&supports('audio/mp4; codecs="'+mp4Codec+'"'))transports.push({transport:'direct',containers:['m4a','m4b','mp4'],video:[],audio:[a.codec]});
    });
    // The capability document (version 2): the device, and what its one player engine can do.
    // Display presence and codec acceptance do not identify the active audio route.
    return {version:2,client:client,engineSetting:'automatic',display:{dynamicRanges:ranges},audioOutput:{route:'',maxChannels:0,spatial:false},engines:{native:{name:'caf-shaka',evidence:'mixed',video:video,audio:audio,audioPassthrough:passthrough,transports:transports,subtitleRendering:['text']}}};
  }
  /** CAF's type probe has no channel/rate arguments. Where the receiver also exposes the
   * MediaCapabilities API, ask exact decoded configurations; silence/timeout stays stereo. */
  async function probeAudioOutput(profile) {
    var capabilities = typeof navigator !== 'undefined' && navigator.mediaCapabilities;
    if (!capabilities || typeof capabilities.decodingInfo !== 'function') return profile;
    await Promise.all(profile.engines.native.audio.map(async function(codec) {
      var mime = progressiveAudioProbes[codec.codec];
      if (!mime) return;
      async function accepts(channels, rate) {
        var timer;
        try { return (await Promise.race([capabilities.decodingInfo({type:'file',audio:{contentType:mime,channels:String(channels),samplerate:rate,bitrate:({mp3:320000,aac:256000,opus:192000,vorbis:192000})[codec.codec]||Math.min(8000000,rate*channels*(codec.maxBitDepth||16))}}),new Promise(function(resolve){timer=setTimeout(function(){resolve(null);},1500);})]))?.supported === true; }
        catch (_) { return false; } finally { clearTimeout(timer); }
      }
      // A bounded set of actual configurations; a positive 192 kHz answer does
      // not stand in for an untested 176.4 kHz operating point.
      for (var channels of [8,6,2]) { if (await accepts(channels,48000)) { codec.maxChannels=channels;break; } }
      var rates=[8000,11025,12000,16000,22050,24000,32000,44100,48000,88200,96000,176400,192000];
      var ceiling=({flac:96000,opus:48000,mp3:48000,aac:96000})[codec.codec]||192000;
      var accepted=await Promise.all(rates.filter(function(rate){return rate<=ceiling;}).map(async function(rate){return await accepts(codec.maxChannels,rate)?rate:0;}));
      accepted=accepted.filter(function(rate){return rate>0;});
      if(accepted.length){codec.sampleRates=accepted;codec.maxSampleRate=Math.max.apply(null,accepted);codec.evidence='mixed';}
    }));
    // Decoding support does not measure the active output route or its channels.
    return profile;
  }
  /** What this TV decodes, published for the session's device before anything plays. */
  async function prepareProfile(lease,installation) {
    requireLease(lease);requirePairInstallation(installation,true);
    if (profiled) return;
    var context = window.cast && cast.framework.CastReceiverContext.getInstance();
    var profile=await probeAudioOutput(buildProfile(context));
    requireLease(lease);requirePairInstallation(installation,true);
    await api('/v1/me/devices/current/capabilities',{method:'PUT',body:profile},lease,installation);
    requireLease(lease);requirePairInstallation(installation,true);
    profiled = true;
  }

  var installedPairings=new Map();
  function pairingIdentity(result,origin){return result&&result.device&&typeof result.device.id==='string'&&typeof result.device.generation==='string'?JSON.stringify([origin,result.device.id,result.device.generation]):undefined;}
  function adopt(result,installation) {
    requirePairInstallation(installation,false);
    session = result.session;
    scope = result.scope;
    var identity=pairingIdentity(result,base);if(identity){installedPairings.delete(identity);installedPairings.set(identity,true);while(installedPairings.size>16)installedPairings.delete(installedPairings.keys().next().value);}
    writeToken(result.deviceToken);
    if(stopMutationScope())stopCoordinator().start().catch(function(){notice(copy('notice.stopFailed'),5000);});
    profiled = false;
    say('');
    return prepareProfile(undefined,installation).then(function () { requirePairInstallation(installation,true);return result; });
  }

  // A reconnect whose answer was lost left the old token in storage; the server accepts it once
  // more, for a short while, with this receiver's deviceId, so one retry recovers it.
  function reconnect() {
    var stored = readToken();
    if (!stored) { return Promise.reject(new Error('no_stored_pairing')); }
    var ask = function () { return post('/v1/cast/reconnect', {protocolVersion: PROTOCOL, deviceToken: stored, deviceId: deviceId()}); };
    return ask().catch(function (e) { if (e && e.status) throw e; return ask(); }).then(adopt);
  }

  /** Redeem a code with a server without touching the current pairing (kept until the new one is adopted). */
  function previewAt(origin, code) {
    return post('/v1/cast/preview', {protocolVersion:PROTOCOL,code:code,deviceId:deviceId()}, undefined, origin);
  }
  function redeemAt(origin, code, expectedBootstrapId) {
    return post('/v1/cast/redeem', {protocolVersion:PROTOCOL,code:code,deviceId:deviceId(),expectedBootstrapId:expectedBootstrapId}, undefined, origin);
  }
  /** Make a redeemed pairing current: stop what's playing, forget the old credential, adopt the new one. */
  async function switchTo(result, origin,installation) {
    requirePairInstallation(installation,false);
    var credential=session,originalScope=scope,originalOrigin=base;
    await stopPlayback();
    requirePairInstallation(installation,false);
    if(installation&&(credential!==session||originalScope!==scope||originalOrigin!==base))throw new Error('receiver_replaced');
    base = origin;
    try { if (base) window.localStorage.setItem(ORIGIN_KEY, base); else window.localStorage.removeItem(ORIGIN_KEY); } catch (_) { /* ignore */ }
    profiled=false;transferStarts.clear();
    return adopt(result,installation);
  }
  /** Pairing by typing a code: only offered when the page is opened in an ordinary browser. */
  async function redeem(code) {
    var origin=base, beforeSession=session, beforeScope=scope, beforeGeneration=controllerGeneration, beforePlayback=playback;
    var offer=await previewAt(origin,code);
    if(base!==origin||session!==beforeSession||scope!==beforeScope||controllerGeneration!==beforeGeneration||playback!==beforePlayback)throw new Error('receiver_replaced');
    var result=await redeemAt(origin,code,offer.controllerBootstrapId);
    if(base!==origin||session!==beforeSession||scope!==beforeScope||controllerGeneration!==beforeGeneration||playback!==beforePlayback){discardPair({origin:origin,result:result});throw new Error('receiver_replaced');}
    return switchTo(result, origin);
  }

  // A Cast device (Chromecast, Google TV) has no keyboard: the code box is only for this page opened
  // in an ordinary browser, for diagnosis. Never ask a TV to type (CAST-01, V-11).
  var ua = (typeof navigator !== 'undefined' && navigator.userAgent) || '';
  var castDevice = /CrKey|Android|Google ?TV|Fuchsia|AFT/i.test(ua);
  var browserPair = $('browser-pair');
  viewCall('browserPair', [!castDevice]);
  if (!castDevice && browserPair) {
    browserPair.hidden = false;
    browserPair.addEventListener('submit', function (event) {
      if (event && event.preventDefault) event.preventDefault();
      var input = $('code-input'), button = $('pair');
      var code = ((input && input.value) || '').trim().toUpperCase();
      if (code.length < 4) { say(copy('pair.enterCode'), 'error'); return; }
      if (button) button.disabled = true;
      say(copy('pair.connecting'));
      serial(function(){return redeem(code);}).then(function () {
        if (button) button.disabled = false;
        if (input) input.value = '';
        say(copy('pair.connected'));
      }).catch(function (error) {
        if (button) button.disabled = false;
        say(pairingMessage(error), 'error');
      });
    });
  }

  function pairingMessage(error) {
    switch (error && error.code) {
      case 'cast_code_expired': return copy('pair.expired');
      case 'cast_code_consumed': return copy('pair.consumed');
      case 'cast_code_revoked': return copy('pair.canceled');
      case 'cast_code_not_found': return copy('pair.invalid');
      case 'rate_limited': return copy('pair.rateLimited');
      case 'tv_busy': return copy('pair.busy');
      default: return copy('pair.failed');
    }
  }

  // --- Playback --------------------------------------------------------------

  var meta = null; // genuine controller or authorized catalogue display identity
  function cleanText(value, max) { return typeof value === 'string' ? value.replace(/[\u0000-\u001f\u007f]/g, ' ').trim().slice(0, max || 200) : ''; }
  function displayMeta(message) {
    if (!message || typeof message !== 'object') return null;
    var kind = cleanText(message.kind, 32);
    var season = Number.isSafeInteger(message.season) ? message.season : undefined;
    var episode = Number.isSafeInteger(message.episode) ? message.episode : undefined;
    var artworkPath = typeof message.artworkPath === 'string' && /^\/v[12]\/[^\s\\]{1,500}$/.test(message.artworkPath) ? message.artworkPath : '';
    var m = {title: cleanText(message.title), subtitle: cleanText(message.subtitle), seriesTitle: cleanText(message.seriesTitle), kind: kind, season: season, episode: episode, artworkPath: artworkPath};
    return m.title || m.subtitle || m.artworkPath ? m : null;
  }
  /** Artwork is protected: fetch it with the receiver's own bearer and hand CAF a data URL. */
  async function artworkUrl(path,lease) {
    requireLease(lease);
    if (!path || !session || typeof FileReader === 'undefined') return '';
    var abort = new AbortController(), timer = setTimeout(function () { abort.abort(); }, 8000);
    try {
      var origin=lease?lease.origin:base,credential=lease?lease.session:session;
      var response = await fetch(origin + path, {headers: {Authorization: 'Bearer ' + credential.accessToken}, signal: abort.signal,redirect:'error',credentials:'omit'});
      requireLease(lease);
      if (!response.ok) return '';
      var blob = await response.blob();
      requireLease(lease);
      if (!/^image\//.test(blob.type) || blob.size > 3145728) return '';
      return await new Promise(function (resolve) { var reader = new FileReader(); reader.onload = function () { resolve(String(reader.result || '')); }; reader.onerror = function () { resolve(''); }; reader.readAsDataURL(blob); });
    } catch (e) { return ''; } finally { clearTimeout(timer); }
  }
  /** Kinds the drawing gives the square cover and the "Now playing" screen (design frames 4 and 9). */
  var AUDIO_KINDS = {song:1, track:1, album:1, audiobook:1, audiobook_file:1, chapter:1};
  function audioKind(kind) { return AUDIO_KINDS[kind] === 1; }
  function castMetadata(m, image) {
    if (!m) return undefined;
    var sharedImage=displayImage(image),images=sharedImage?[{url:sharedImage}]:[];
    if (m.kind === 'episode') return {metadataType: 2, title: m.title, seriesTitle: m.seriesTitle || m.subtitle, season: m.season, episode: m.episode, images: images};
    if (m.kind === 'movie') return {metadataType: 1, title: m.title, subtitle: m.subtitle, images: images};
    // Structured catalogue fields are facts; the old joined subtitle is not an artist.
    if (audioKind(m.kind)) return Object.assign({metadataType: 3, title: m.title, images: images},m.artist?{artist:m.artist}:{},m.albumTitle?{albumName:m.albumTitle}:{});
    return {metadataType: 0, title: m.title, subtitle: m.subtitle, images: images};
  }
  /** A same-origin path the catalogue itself gave us; never a sender-supplied address. */
  function cataloguePath(value) { return typeof value === 'string' && /^\/(?!\/)[^\s\\]{1,500}$/.test(value) ? value : ''; }
  /** A song's own record has no cover, and neither has an audiobook chapter: the album's or the
   * book's square cover is the drawn artwork (frames 4 and 9). The related id comes from the
   * current item's own projection, read with this TV's credential, and only its own kind counts. */
  async function relatedCover(id, kind,lease) {
    requireLease(lease);
    if (typeof id !== 'string' || !id || id.length > 128 || !kind) return '';
    var related;
    try { related = (await api('/v1/items/' + encodeURIComponent(id), {method: 'GET'},lease)).body; }
    catch (e) { return ''; }
    if (!related || related.id !== id || related.missing || related.kind !== kind) return '';
    return cataloguePath(related.posterUrl);
  }

  // GET /v1/items/{id} is the existing ScopeRead catalogue projection. Display
  // enrichment never blocks media or gains authority over a playback session.
  async function refreshCatalogueDisplay(owner, contentId,lease) {
    var generation=viewGeneration, origin=base, account=scope&&scope.accountId, profile=scope&&scope.profileId;
    function current() { return leaseCurrent(lease)&&playback&&playback.id===owner.id&&playback.generation===owner.presentation.generation&&playback.itemId===owner.itemId&&viewGeneration===generation&&base===origin&&scope&&scope.accountId===account&&scope.profileId===profile; }
    function publish() {
      if(!current())return;
      if(pictureReady)playingView();
      else if(meta&&meta.title){
        viewLoading=true;viewCall('loading',[Object.assign({},meta)]);
        // A catalogue enrichment is still this occurrence: loading clears the
        // view, so restore its genuine current cover even if no new URL arrived.
        if(currentArtwork)showArtwork(currentArtwork,audioKind(meta.kind));
      }
      try {
        var media=typeof manager.getMediaInformation==='function'?manager.getMediaInformation():null;
        if(media&&media.contentId===contentId&&typeof manager.setMediaInformation==='function')manager.setMediaInformation(Object.assign({},media,{metadata:castMetadata(meta,currentArtwork)}),false);
      } catch(e) { /* A display update never changes media/session authority. */ }
    }
    try {
      var item=(await api('/v1/items/'+encodeURIComponent(owner.itemId),{method:'GET'},lease)).body;
      if(!current()||!item||item.id!==owner.itemId||item.missing)return;
      var year=item.kind==='movie'&&Number.isSafeInteger(item.year)&&item.year>0&&item.year<=9999?String(item.year):undefined;
      var identity=displayMeta({title:item.title,kind:item.kind,subtitle:year,artworkPath:item.posterUrl});
      if(!identity)return;
      if(item.song&&typeof item.song==='object'){
        var artist=cleanText(item.song.artist),album=cleanText(item.song.albumTitle);
        if(artist)identity.artist=artist;if(album)identity.albumTitle=album;
      }
      if(item.episode&&typeof item.episode==='object'){
        identity.seriesTitle=cleanText(item.episode.showTitle);
        if(Number.isSafeInteger(item.episode.seasonNumber))identity.season=item.episode.seasonNumber;
        if(Number.isSafeInteger(item.episode.number))identity.episode=item.episode.number;
      }
      // An audiobook's screen shows the chapter, then the book, then the author (frame 9).
      if(item.bookFile&&typeof item.bookFile==='object'){
        var author=cleanText(item.author),book=cleanText(item.bookFile.bookTitle);
        if(author)identity.artist=author;if(book)identity.albumTitle=book;
      }
      meta=identity;publish();
      var coverPath=identity.artworkPath;
      if(!coverPath&&audioKind(item.kind)){
        var coverId='',coverKind='';
        if(item.song&&typeof item.song==='object'){coverId=item.song.albumId;coverKind='album';}
        else if(item.bookFile&&typeof item.bookFile==='object'){coverId=item.bookFile.bookId;coverKind='book';}
        coverPath=await relatedCover(coverId,coverKind,lease);
        if(!current())return;
        if(coverPath){identity.artworkPath=coverPath;meta=identity;publish();}
      }
      if(coverPath){
        var image=await artworkUrl(coverPath,lease);
        if(current()&&image){
          currentArtwork=image;
          // loading(meta) resets the view's art. Publish that identity before
          // its artwork, while CAF metadata receives the same genuine image.
          publish();
        }
      }
    } catch(e) { /* Missing/refused metadata remains absent; no notice or old-title fallback. */ }
  }

  // Playback v1 (the same session contract every Portico client plays through): start a session
  // for the item, load its presentation URL, report the timeline (progress, lease renewal and
  // observation in one POST), and end it. Play and pause are the player's own and are reported;
  // a seek goes through the server only when it must produce the stream again. Media URLs are
  // accepted only from the server's answer. Commands share one serialized lane.
  var directTypes = {mp3:'audio/mpeg',flac:'audio/flac',m4a:'audio/mp4',m4b:'audio/mp4',aac:'audio/aac',adts:'audio/aac',ogg:'audio/ogg',opus:'audio/ogg',wav:'audio/wav',webm:'video/webm',mp4:'video/mp4',m4v:'video/mp4',mov:'video/mp4'};
  /** Whether a seek must go through the server: only when something is transcoded or burned in. */
  function serverSeek(session) {
    var d = session.presentation.decision || {};
    return [d.video, d.audio, d.subtitles].some(function (x) { return !!x && (x.action === 'transcode' || x.action === 'burn'); });
  }
  function track(session) {
    return {id: session.id, revision: session.revision, itemId: session.itemId || '', versionId: session.versionId, audioTrackId: session.presentation.audio && session.presentation.audio.trackId, generation: session.presentation.generation, serverSeek: serverSeek(session), queue: queueRef(session.queue), presentation: session.presentation, subtitleTrackId: session.presentation.subtitle ? session.presentation.subtitle.trackId : 'off'};
  }
  /** CAF's load request for a session's presentation. */
  function loadRequest(session, image, autoplay,lease) {
    requireLease(lease);
    var p = session.presentation;
    if (typeof p.url !== 'string' || !/^\/v1\/media\/[^\\\s]+$/.test(p.url)) throw new Error('invalid_media_url');
    var container = String(p.container || '');
    // The server may deliver an audio conversion through the presentation URL while
    // its general delivery decision still describes the source container. Only the
    // playable audio plan for this exact URL can supply the delivered audio type.
    var audioOnly = p.decision && !p.decision.video && !!p.decision.audio;
    var render = p.audioRender;
    if (p.mode !== 'stream' && audioOnly && render && (render.mode === 'direct' || render.mode === 'converted') && render.url === p.url) {
      container = String(render.container || '');
    }
    var contentId = 'portico-' + token(16);
    mediaAddresses.set(contentId, (lease?lease.origin:base) + p.url);
    while (mediaAddresses.size > 8) mediaAddresses.delete(mediaAddresses.keys().next().value);
    var media = {contentId: contentId, streamType: 'BUFFERED'};
    if (p.mode === 'stream') {
      media.contentType = 'application/x-mpegurl';
      media.hlsSegmentFormat = media.hlsVideoSegmentFormat = container === 'fmp4_hls' ? 'FMP4' : 'TS';
    } else {
      if (!directTypes[container]) throw new Error('unsupported_media_container');
      media.contentType = audioOnly && ['mp4','mov','m4v'].indexOf(container)>=0 ? 'audio/mp4' : audioOnly && container==='webm' ? 'audio/webm' : directTypes[container];
    }
    // Title and artwork for CAF's standard player, the Google TV ambient UI and the Google Home app (CAST-01 §2).
    var metadata = castMetadata(meta, image);
    if (metadata) media.metadata = metadata;
    var load = {media: media, currentTime: Math.max(0, (p.startPositionMs || 0) / 1000), autoplay: autoplay !== false};
    if (p.mode !== 'stream' && p.subtitle && p.subtitle.delivery === 'sidecar') {
      var subtitle = (p.subtitles || []).filter(function(file){return file.trackId === p.subtitle.trackId && file.format === 'webvtt';})[0];
      // The server's WebVTT endpoint has already applied the saved/session offset.
      // Only this presentation's own grant can supply its selected text track.
      var address = subtitle && /^\/v1\/media\/subtitles\/([^/\\\s?#]+)\/([^/\\\s?#]+)\/([1-9][0-9]*)\.vtt$/.exec(subtitle.url);
      if (!subtitle || !address || address[1] !== p.url.split('/')[3] || address[2] !== subtitle.trackId || subtitle.timeDomain !== 'source-relative' || subtitle.offsetMs !== 0) throw new Error('unsupported_subtitle_presentation');
      media.tracks = [{trackId: 1, type: 'TEXT', subtype: 'SUBTITLES', trackContentId: (lease ? lease.origin : base) + subtitle.url, trackContentType: 'text/vtt'}];
      load.activeTrackIds = [1];
    }
    // Only the actual current media enters CAF. The server owns the next item;
    // preloadTime remains a hint and never establishes that bytes were prefetched.
    if (queueRef(session.queue)) load.queueData = {items:[{media:media,autoplay:load.autoplay,startTime:load.currentTime,preloadTime:10}],startIndex:0};
    return load;
  }

  var stopMutations=null,stopClockOffset=0,completedStops=new Map();
  function stopMutationScope(){
    var current=session,viewer=current&&current.viewer;
    if(!current||!viewer||!current.sessionFamilyId)return undefined;
    return {serverId:viewer.serverId,origin:base||window.location.origin,authority:viewer.authority,accountId:viewer.accountId,profileId:viewer.profileId,familyId:current.sessionFamilyId,deviceId:current.deviceId};
  }
  function stopCoordinator(){
    if(stopMutations)return stopMutations;
    var contracts=PorticoCastContracts;
    var exchange=async function(entry,path,method,body,signal){
      var current=stopMutationScope();
      if(!current||!contracts.sameUserMutationScope(current,entry.scope))throw new contracts.UserMutationError('mutation_scope_changed',entry.key);
      var credential=session;
      var result=await request(path,{method:method,body:body,headers:entry.headers,bearer:credential.accessToken,origin:base,raw:true});
      current=stopMutationScope();if(!current||!contracts.sameUserMutationScope(current,entry.scope))throw new contracts.UserMutationError('mutation_scope_changed',entry.key);
      var retry=result.headers&&result.headers['retry-after'];if(retry&&!/^[0-9]+$/.test(retry.trim())){var date=Date.parse(retry);if(Number.isFinite(date))result.headers['retry-after']=String(Math.max(0,Math.ceil((date-(Date.now()+stopClockOffset))/1000)));}
      return result;
    };
    stopMutations=new contracts.UserMutationCoordinator({store:new contracts.IndexedDBUserMutationStore(),scope:stopMutationScope,now:function(){return Date.now();},transport:{
      send:function(entry,signal){return exchange(entry,entry.path,entry.method,entry.body,signal);},
      lookup:function(entry,signal){return exchange(entry,'/v1/user-mutations/'+encodeURIComponent(entry.key),'GET',undefined,signal);},
      recover:async function(entry,receipt){if(receipt.status!=='applied'&&receipt.status!=='already_stopped')throw new contracts.UserMutationError('mutation_needs_review',entry.key,receipt);return {status:204,headers:{}};}
    }});
    return stopMutations;
  }
  async function originalStopReceipt(lease,key,target) {
    requireLease(lease);
    if(!sameControllerStamp(target.controller,controllerStamp(lease)))throw new Error('controller_replaced');
    var originalScope=stopMutationScope();if(!originalScope)throw new Error('mutation_scope_changed');
    // Read with the captured original bearer/family. Never renew or borrow a
    // successor credential while reconciling an absent local command.
    var result=await request('/v1/user-mutations/'+encodeURIComponent(key),{method:'GET',bearer:lease.bearer,origin:lease.origin,raw:true});
    requireLease(lease);
    var currentScope=stopMutationScope();
    if(!currentScope||!PorticoCastContracts.sameUserMutationScope(originalScope,currentScope))throw new Error('mutation_scope_changed');
    if(result.status===404&&result.body&&result.body.error&&result.body.error.code==='receipt_unknown')return false;
    if(result.status!==200)throw new Error('mutation_receipt_pending');
    var receipt=PorticoCastContracts.decodeGetUserMutationResponse(result.body);
    if(receipt.key!==key||receipt.operation!=='stop_playback_session'||receipt.targetId!==target.occurrence.sessionId||receipt.generation!==target.occurrence.generation||receipt.expiresMs<receipt.committedMs||
      !(receipt.status==='applied'&&['applied','unchanged'].includes(receipt.positionStatus)||receipt.status==='already_stopped'&&receipt.positionStatus==='already_stopped'))throw new Error('mutation_needs_review');
    return true;
  }
  async function stopPlayback(ended,lease,expected,mutationKey,preserveError) {
    requireLease(lease);if(expected&&playback!==expected)return;
    if (!playback) return;
    var owner=playback,id=owner.id,generation=owner.generation,identity='stop:'+id+':'+generation;
    // Caller custody survives policy/IDB failure. Capture the final evidence only
    // once, before either await; retry never samples a later renderer position.
    var frozen=owner.stopIntent;
    if(!frozen)frozen=owner.stopIntent={scope:stopMutationScope(),clock:clockFacts(),body:Object.freeze({generation:generation,positionMs:Math.max(0,Math.round(position()*1000))}),key:PorticoCastContracts.isUserMutationKey(mutationKey||'')?mutationKey:null};
    if(!frozen.scope||!stopMutationScope()||!PorticoCastContracts.sameUserMutationScope(frozen.scope,stopMutationScope()))throw new Error('mutation_scope_changed');
    if(frozen.key&&PorticoCastContracts.isUserMutationKey(mutationKey||'')&&mutationKey!==frozen.key)throw new Error('idempotency_conflict');
    if(!frozen.key){
      var policy=await api('/v1/user-mutations/policy',{method:'GET'},lease);
      if(!Number.isSafeInteger(policy.body.serverTimeMs))throw new Error('mutation_not_queued');
      stopClockOffset=policy.body.serverTimeMs-Date.now();frozen.key=PorticoCastContracts.newUserMutationKey(policy.body.serverTimeMs);
    }
    requireLease(lease);
    if(playback!==owner||!stopMutationScope()||!PorticoCastContracts.sameUserMutationScope(frozen.scope,stopMutationScope()))throw new Error('occurrence_replaced');
    var coordinator=stopCoordinator();
    var releaseRenderer=function(){
      var accepted=coordinator.snapshot().some(function(entry){return entry.intentId===identity&&entry.key===frozen.key&&PorticoCastContracts.sameUserMutationScope(entry.scope,frozen.scope);});
      if(!accepted||playback!==owner)return;
      clearTimeout(reportTimer);clearTimeout(bufferingTimer);
      playback=null;meta=null;cafOccurrence=null;platformOccurrence=null;
      if(manager)mediaCommand('STOP');
      mediaAddresses.clear();if(!preserveError)showIdle();
    };
    var unsubscribe=coordinator.subscribe(releaseRenderer);
    try{
      await coordinator.run({operation:'stop_playback_session',method:'DELETE',path:sessionPath(id),target:identity,intentId:identity,key:frozen.key,body:frozen.body});
      releaseRenderer();
      var entry=coordinator.snapshot().find(function(entry){return entry.intentId===identity&&entry.state==='confirmed';});
      if(entry)await coordinator.acknowledge(entry.key);
      return frozen.clock;
    }finally{unsubscribe();}
  }

  /** Starts a v1 session for the item and answers CAF's load request for it. A session already
   * playing here is replaced in the same request, so this TV holds one playback slot. */
  async function resolvePlayback(itemId, startSeconds, identity, subtitleTrackId, queue,lease,pending,attempt,versionId,audioTrackId) {
    requireLease(lease);
    if (!session || !manager || !/^[A-Za-z0-9_-]{1,128}$/.test(itemId)) throw new Error('playback_unavailable');
    if (versionId !== undefined && (typeof versionId !== 'string' || !/^[A-Za-z0-9._:-]{1,128}$/.test(versionId))) throw new Error('invalid_cast_version');
    if (audioTrackId !== undefined && (typeof audioTrackId !== 'string' || !/^[A-Za-z0-9._:-]{1,128}$/.test(audioTrackId))) throw new Error('invalid_cast_audio');
    clearTimeout(reportTimer); clearTimeout(bufferingTimer);
    var previousOwner=playback,replaces=previousOwner&&previousOwner.id,requestedMeta=identity||null;
    showLoading(requestedMeta);
    var art = requestedMeta&&requestedMeta.artworkPath?artworkUrl(requestedMeta.artworkPath,lease).catch(function(){return '';}):Promise.resolve('');
    await prepareProfile(lease);
    requireLease(lease);
    var body = {itemId: itemId, state: 'playing'};
    if (versionId !== undefined) body.versionId = versionId;
    // The server resolves this semantic track ID for the receiver's own version.
    if (audioTrackId !== undefined) body.audio = {trackId: audioTrackId};
    if (Number.isFinite(startSeconds)) body.startPositionMs = Math.round(Math.max(0, startSeconds) * 1000); else body.startFrom = 'resume';
    if (replaces) body.replacesSessionId = replaces;
    if(queue&&!queueRef(queue))throw new Error('invalid_cast_queue');
    if (queueRef(queue)) {
      body.queue={queueId:queue.queueId,entryId:queue.entryId};delete body.itemId;
      if(queue.transferId)body.castTransferId=queue.transferId;
    }
    // The sender's subtitle choice ("off", or a track id). This TV draws text only; the server
    // delivers a styled or image track burned into the picture, or a styled track's plain words
    // when the viewer turned "Styled subtitles" off.
    if (subtitleTrackId === 'off') body.subtitles = {trackId: null};
    else if (typeof subtitleTrackId === 'string' && /^[A-Za-z0-9._:-]{1,128}$/.test(subtitleTrackId)) body.subtitles = {trackId: subtitleTrackId};
    var key = token(16), answer, preparingIdentity=null, startSession=session, startScope=scope, startOrigin=base;
    function requireStart(){requireLease(lease);if(session!==startSession||scope!==startScope||base!==startOrigin)throw new Error('receiver_replaced');}
    if(queue&&queue.transferId){
      var captured=transferStarts.get(queue.transferId);
      if(!captured)throw new Error('cast_transfer_required');
      if(captured.body)body=captured.body;else captured.body=Object.freeze(body);
      key='cast-'+queue.transferId;
    }
    var cleanupWitness={origin:startOrigin,bearer:startSession&&startSession.accessToken},ownedStart=null,pendingSequence=0;
    function observeStart(result){
      // Body authority comes only from the complete canonical schema graph.
      // Status is a separate protocol fence: a valid 202 never completes LOAD.
      var value;
      try {
        if(result.status!==201&&result.status!==202)throw new Error('unexpected_start_status');
        value=PorticoCastContracts.decodeStartPlaybackSessionResponse(result.body);
        if(result.status===202&&(!Number.isSafeInteger(value.retryAfterMs)||value.retryAfterMs<1||value.retryAfterMs>10000))throw new Error('invalid_retry');
        if(result.status===201&&Object.prototype.hasOwnProperty.call(value,'retryAfterMs'))throw new Error('unexpected_retry');
        if(!Object.prototype.hasOwnProperty.call(value,'id')){
          if(result.status!==202)throw new Error('missing_session');
          return; // Only the decoder's closed retry-only branch reaches here.
        }
        if(!/^[A-Za-z0-9._:-]{1,128}$/.test(value.id)||!Number.isSafeInteger(value.presentation.generation)||value.presentation.generation<1||
          (body.queue?(!value.queue||value.queue.queueId!==body.queue.queueId||value.queue.entryId!==body.queue.entryId):value.itemId!==body.itemId))throw new Error('start_binding_changed');
      } catch (cause) {
        var malformed=new Error('invalid_preparation');malformed.retired=true;throw malformed;
      }
      if(ownedStart&&(ownedStart.id!==value.id||ownedStart.generation!==value.presentation.generation)){
        var replaced=new Error('preparation_replaced');replaced.retired=true;throw replaced;
      }
      // Capture before any currentness, view or controller callback can retire
      // this request. The immutable witness always carries its original bearer.
      ownedStart=Object.freeze({id:value.id,generation:value.presentation.generation});
      if(result.status===202)preparingIdentity=ownedStart;
      result.body=value;
    }
    try {
      // A valid 202 keeps the original request and its cleanup witness alive.
      for (;;) {
        requireStart();
        answer=await api('/v1/playback/sessions',{body:body,headers:{'Idempotency-Key':key},noRenew:true,observeAnswer:observeStart},lease);
        if(answer.status!==202)break;
        // Any canonical, original-current 202 is non-authoritative progress.
        // Retry-only evidence never creates a resource or cleanup witness.
        if(pending){requireStart();pending({startId:key,sequence:++pendingSequence});requireStart();}
        preparing();await wait(Math.max(250,answer.body.retryAfterMs));requireStart();
      }
      requireStart();stopPreparing();
      var started=answer.body;
      if(!started||!started.presentation)throw new Error('playback_not_ready');
      if(preparingIdentity&&(preparingIdentity.id!==started.id||preparingIdentity.generation!==started.presentation.generation))throw new Error('preparation_replaced');
      if(started.state==='ended')throw new Error('session_ended');
      var artTimer,image;
      try{image=await Promise.race([art,new Promise(function(resolve){artTimer=setTimeout(function(){resolve('');},1500);})]);}
      finally{clearTimeout(artTimer);}
      requireStart();if(playback!==previousOwner)throw new Error('occurrence_replaced');
      meta=requestedMeta;var load=loadRequest(started,image,true,lease);
      // The sender's own display cover belongs to this admitted occurrence. Show
      // it now so a later catalogue identity read keeps it instead of clearing
      // the loading view's art; it is never a media or session fact.
      if(image)showArtwork(image,audioKind(requestedMeta.kind));
    }catch(e){
      if(ownedStart)await cleanupOccurrence(ownedStart,cleanupWitness);
      throw e;
    }

    playback = track(started);
    if(attempt){attempt.owner=playback;attempt.owned=ownedStart;attempt.witness=cleanupWitness;attempt.contentId=load.media.contentId;}
    observer = null; reportFailures = 0;
    return load;
  }
  function startPlayback(itemId,startSeconds,identity,subtitleTrackId,queue,lease,pending,versionId,audioTrackId) { return commandQueue(lease,async function(){
    requireLease(lease);recoveries=0;networkRetries=0;
    var captured,attempt={adopted:false};
    if(queue&&queue.transferId){
      var digest=JSON.stringify({itemId:itemId,startSeconds:startSeconds,subtitleTrackId:subtitleTrackId,queue:queue,versionId:versionId,audioTrackId:audioTrackId}),now=Date.now();
      transferStarts.forEach(function(value,key){if(value.expires<=now)transferStarts.delete(key);});
      captured=transferStarts.get(queue.transferId);
      if(captured&&(captured.digest!==digest||captured.scope!==scope||captured.origin!==base))throw new Error('cast_transfer_changed');
      if(captured&&captured.accepted)return captured.accepted;
      if(!captured){if(transferStarts.size>=8)throw new Error('cast_transfer_busy');captured={digest:digest,scope:scope,origin:base,expires:now+180000};transferStarts.set(queue.transferId,captured);}
    }
    try{var load=await resolvePlayback(itemId,startSeconds,identity,subtitleTrackId,queue,lease,pending,attempt,versionId,audioTrackId);var startedOwner=attempt.owner;if(playback!==startedOwner)throw new Error('occurrence_replaced');recoveries=0;networkRetries=0;await loadMedia(load,lease,attempt);requireLease(lease);if(playback!==startedOwner)throw new Error('occurrence_replaced');attempt.adopted=true;showPlaying();scheduleReport(0);refreshCatalogueDisplay({id:playback.id,itemId:playback.itemId,presentation:{generation:playback.generation}},load.media.contentId,lease);if(captured)captured.accepted=playback;return playback;}
    catch(e){await cleanupFailedLoad(attempt);if(leaseCurrent(lease)&&(!attempt.owner||!playback||playback===attempt.owner))showError(e&&(e.code||e.message));throw e;}
  }); }

  // V1 music is progressive: the next server occurrence starts only after the
  // actual current media finishes. CAF preloadTime is a hint, not a buffer or
  // gapless promise; there is no private progressive PRELOAD or held response.
  async function advanceQueue(owner) {
    if (!owner || playback!==owner || !owner.queue) return false;
    var head = (await api(queuePath(owner.queue.queueId),{method:'GET'})).body;
    if (playback !== owner) return false;
    if(!head.current||head.current.entryId!==owner.queue.entryId)throw new Error('cast_queue_changed');
    if (!head.next || !head.next.available) return false;
    var answer = await api(queuePath(owner.queue.queueId) + ':advance',{body:{reason:'completion',completionSessionId:owner.id,completionGeneration:owner.generation},headers:{'If-Match':head.revision,'Idempotency-Key':token(16)}});
    if (playback !== owner || !answer.body.session) return false;
    var current=answer.body.session;
    playback=track(current);meta=null;observer=null;reportFailures=0;recoveries=0;networkRetries=0;
    showLoading(null); // Never label the new occurrence with the old title or artwork.
    var load;
    try { load=loadRequest(current,'',true);await loadMedia(load); }
    catch(e) { var stopped=stopPlayback(false,undefined,undefined,undefined,true);showError(e&&(e.code||e.message));await stopped;throw e; }
    showPlaying();scheduleReport(0);refreshCatalogueDisplay(current,load.media.contentId);broadcast();return true;
  }

  // --- Observations: the latest state once a second, never queued or resent -------

  function timelineState() {
    var s = manager ? manager.getPlayerState() : 'IDLE';
    return s === 'PLAYING' ? 'playing' : s === 'BUFFERING' ? 'buffering' : 'paused';
  }
  function clock() { return typeof performance !== 'undefined' && performance.now ? performance.now() : Date.now(); }
  /** A seek the receiver performed or saw: the next sample says the position jumped. */
  function observedSeek() { if (observer) observer.seek++; }
  /** One sample. The first claims the session's observer epoch (again, naming the old one, after
   * the server restarted); the device's own clocks start at the claim. One attempt, 1.5 s. */
  async function report(state) {
    if (!playback) return;
    var id = playback.id, generation = playback.generation, now = clock();
    if (!observer || observer.id !== id) observer = {id: id, epoch: null, replaces: null, seq: 0, at: now, play: 0, since: null, seek: 0};
    var o = observer, s = state || timelineState();
    if (o.since !== null) { o.play += now - o.since; o.since = null; }
    if (s === 'playing') o.since = now;
    try {
      if (!o.epoch) {
        var grant = (await api(sessionPath(id) + '/observer', {body: o.replaces ? {generation: generation, replaces: o.replaces} : {generation: generation}, single: true, timeoutMs: 5000})).body;
        if (observer !== o || !grant || typeof grant.epoch !== 'string') return;
        o.epoch = grant.epoch; o.replaces = null; o.seq = 0; o.at = clock(); o.play = 0; if (o.since !== null) o.since = o.at;
      }
      var mono = Math.max(0, Math.floor(clock() - o.at)), played = Math.floor(o.play + (o.since !== null ? clock() - o.since : 0));
      await api(sessionPath(id) + '/observation', {method: 'PUT', single: true, timeoutMs: 1500, body: {epoch: o.epoch, seq: ++o.seq, generation: generation, state: s, positionMs: Math.round(Math.max(0, position()) * 1000), rate: s === 'playing' ? 1 : 0, monotonicMs: mono, playClockMs: Math.min(mono, played), seekSerial: o.seek}});
      reportFailures = 0; notice('');
    } catch (e) {
      if (e.code === 'observation_epoch_changed') { if (observer === o) { o.replaces = o.epoch; o.epoch = null; } return; }
      // The server no longer has this session (stopped elsewhere, moved, ended by an administrator),
      // or no longer accepts this TV's pairing: with no sender connected, nothing else would stop it.
      if (e.status === 404 || e.status === 410 || e.code === 'session_ended' || e.pairingEnded) { if (playback && playback.id === id) { playback = null; meta = null; if (manager) mediaCommand('STOP'); showIdle(); broadcast(); } return; }
      reportFailures++; // b4: transient engine events are silent.
      throw e;
    }
  }
  /** The next sample: once a second, whatever happened to the last one. Samples never wait in
   * the command queue: a command does not wait behind a sample, nor a sample behind a command. */
  var reporting = null;
  function scheduleReport(ms) {
    clearTimeout(reportTimer);
    if (!playback) return;
    if (ms === undefined) ms = 1000;
    reportTimer = setTimeout(function () {
      if (!reporting) reporting = report().catch(function () {}).finally(function () { reporting = null; });
      reporting.finally(function () { scheduleReport(); });
    }, ms);
  }
  /** Reload the media after the server produced a new presentation (a server seek, a recovery). */
  async function adoptPresentation(updated, autoplay,lease,expected,recovery) {
    requireLease(lease);if(expected&&playback!==expected)return;
    if (!playback || updated.id !== playback.id) return;
    var fresh = updated.presentation.generation !== playback.generation, selected = fresh ? recovery ? recovery.tracks : selectedTracks() : null;
    playback = track(updated);
    if (recovery && recovery.owner === expected) recovery.owner = playback;
    if (!fresh) return;
    var adopted=playback;await loadMedia(loadRequest(updated, '', autoplay,lease),lease,undefined,selected);
    requireLease(lease);if(playback!==adopted)return;showPlaying();
  }
  /** PATCH the session. A 412 carries the server's session: adopt it and apply the change to it. */
  async function change(body,lease,expected,recovery) {
    requireLease(lease);var owner=expected||playback;
    if(!owner||playback!==owner)return;
    var id=owner.id;
    var patch=function(){requireLease(lease);if(playback!==owner)throw new Error('occurrence_replaced');return api(sessionPath(id),{method:'PATCH',body:body,headers:{'If-Match':owner.revision,'Content-Type':'application/merge-patch+json'}},lease);};
    var answer;
    try{answer=await patch();}
    catch(e){
      requireLease(lease);
      if(e.status!==412||!e.current||!e.current.presentation||e.current.id!==id||playback!==owner)throw e;
      await adoptPresentation(e.current,recovery?recovery.autoplay:!paused(),lease,owner,recovery);requireLease(lease);
      if(!playback||playback.id!==id)throw new Error('occurrence_replaced');owner=playback;answer=await patch();
    }
    requireLease(lease);if(playback!==owner||!answer.body||answer.body.id!==id)return;
    await adoptPresentation(answer.body,recovery?recovery.autoplay:!paused(),lease,owner,recovery);
  }
  function seekTo(seconds,lease) {
    var owner=playback;
    return commandQueue(lease,async function(){
      requireLease(lease);if(!owner||playback!==owner)return;
      var at=Math.max(0,seconds);
      if(!owner.serverSeek){mediaCommand('SEEK',{currentTime:at});return;}
      await change({seek:{positionMs:Math.round(at*1000),id:token(16)}},lease,owner);
    });
  }

  function terminalPlayback(owner, code) {
    return serial(async function(){
      if (shuttingDown || !owner || playback !== owner) return;
      // Keep the terminal card visible while durable acceptance retires the
      // renderer; the frozen owner keeps final evidence if storage refuses it.
      var stopped = stopPlayback(false,undefined,owner,undefined,true);
      showError(code);
      if (controllerSender) send(controllerSender,{type:'load-failed',code:'playback_failed'});
      broadcast();
      await stopped.catch(function(error){notice(copy('notice.stopFailed'),5000);throw error;});
    });
  }
  function recover(code, expected) {
    var owner = expected === undefined ? playback : expected;
    if (shuttingDown || !owner || playback !== owner || recovering && recovering.owner === owner) return;
    var network = code === 'network_error';
    // Network retries never consume decoder fallbacks or poison route memory.
    if ((network ? networkRetries : recoveries) >= 3) { terminalPlayback(owner,code); return; }
    var operation = {owner:owner}, at = position(), wasPaused = paused(), identity = meta, selected = selectedTracks();
    operation.autoplay = !wasPaused;operation.tracks = selected;
    recovering = operation;
    var delay = network ? [1000,2000,5000][networkRetries++] : 0;
    if (network) networkRetryPosition = at; else recoveries++;
    clearTimeout(bufferingTimer);broadcast();
    // Waiting outside the command lane leaves Stop and replacement loads usable.
    function attemptRecovery() { return (delay ? wait(delay) : Promise.resolve()).then(function(){return serial(async function(){
      if (shuttingDown || playback !== operation.owner) return;
      if (network) {
        var retry = Object.assign({},owner,{presentation:Object.assign({},owner.presentation,{startPositionMs:Math.round(Math.max(0,at)*1000)})});
        await loadMedia(loadRequest(retry,'',!wasPaused),undefined,undefined,selected);
        if (playback === operation.owner) { showPlaying();scheduleReport(0); }
        return;
      }
      // The server remembers a decoder/stall failure and replans this occurrence.
      try { await change({failure:{code:code,detail:''},positionMs:Math.round(Math.max(0,at)*1000)},undefined,owner,operation); return; }
      catch (e) { if (e.code!=='unsupported_media'||code!=='stalled_without_data') throw (e.code==='unsupported_media'?new Error('playback_failed'):e); }
      if (playback !== operation.owner) return;
      // A stall restart retains the current queue occurrence and session choices.
      var subtitle = selected.text && selected.text.length === 0 && owner.presentation.subtitle && owner.presentation.subtitle.delivery === 'sidecar' ? 'off' : owner.subtitleTrackId;
      var load = await resolvePlayback(owner.itemId,at,identity,subtitle,owner.queue,undefined,undefined,undefined,owner.versionId,owner.audioTrackId);
      operation.owner = playback;load.autoplay = !wasPaused;
      await loadMedia(load,undefined,undefined,selected);showPlaying();scheduleReport(0);
    });}).catch(function(e){
      if (network && !shuttingDown && playback === operation.owner && networkRetries < 3) { delay = [1000,2000,5000][networkRetries++];return attemptRecovery(); }
      throw e;
    }); }
    attemptRecovery().then(function(){if(!shuttingDown&&playback===operation.owner)notice('');}).catch(function(e){
      if (shuttingDown || playback !== operation.owner) return;
      notice('');return terminalPlayback(operation.owner,network?'network_error':code==='decode_error'?'decode':e&&(e.code||e.message));
    }).finally(function(){if(recovering===operation)recovering=null;if(!shuttingDown)broadcast();});
  }

  // --- Control: who may drive this TV (SEC-14, CAST-04, D-FEAT-10) ------------

  var controllerToken = '';
  var controllerSender = null;   // the sender that paired in this session
  var controllerPresent = false; // still connected
  var pendingPair = null;        // {senderId, requester, origin, result, timer}

  function send(senderId, body) { try { if (castContext && senderId) castContext.sendCustomMessage(NAMESPACE, senderId, body); } catch (e) { /* the sender left */ } }
  function requesterName(message, senderId) {
    var name = cleanText(message && message.displayName, 64);
    if (name) return name;
    // The SDK's own sender fact, not an inferred model: a sender that gave no
    // description but is an iPhone or iPad is named as one (frame 15).
    try {
      var sender = castContext && typeof castContext.getSender === 'function' ? castContext.getSender(senderId) : null;
      if (sender && /iPhone|iPad/.test(String(sender.userAgent || ''))) return copy('iPhoneOrIPad');
    } catch (e) { /* the sender left */ }
    return copy('someone');
  }
  function hideTakeover() { if (!viewCall('takeover', [null])) visible('takeover', false); }
  function takeoverView(controllerHere, remaining) {
    return viewCall('takeover', [{requester: pendingPair.requester, controllerPresent: controllerHere, timeoutMs: remaining, remoteKeysAvailable: remoteKeysAvailable()}]);
  }
  function askTakeover(requester, controllerHere) {
    var timeoutMs = controllerHere ? TAKEOVER_ASK_MS : TAKEOVER_ABSENT_MS;
    if (pendingPair) {
      pendingPair.viewDeadline = Date.now() + timeoutMs;
      pendingPair.viewControllerPresent = controllerHere;
      if (takeoverView(controllerHere, timeoutMs)) return;
    }
    text('takeover-title', copy('takeover.title', {requester:requester}));
    text('takeover-body', controllerHere ? copy('takeover.present') : copy('takeover.absent'));
    visible('takeover', true);
  }
  function discardPair(p) {
    if(p&&installedPairings.has(pairingIdentity(p.result,p.origin)))return;
    if(p&&p.result&&p.result.device&&p.result.device.id&&p.result.session)request('/v1/cast/devices/'+encodeURIComponent(p.result.device.id),{method:'DELETE',origin:p.origin,bearer:p.result.session.accessToken,headers:{'If-Match':p.result.device.generation}}).catch(function(){});
  }
  function denyPending(code) {
    var p = pendingPair; if (!p) return;
    pendingPair = null; clearTimeout(p.timer); hideTakeover();p.code='';
    send(p.senderId, {type: 'pair-failed', code: code || 'tv_busy', message: pairingMessage({code: code || 'tv_busy'})});
  }
  function allowPending() {
    var p = pendingPair; if (!p) return;
    pendingPair = null; clearTimeout(p.timer); hideTakeover();
    serial(function () { return takeControl(p); }).catch(function (error) {
      send(p.senderId, {type: 'pair-failed', code: error.code || '', message: pairingMessage(error)});
    });
  }
  var controllerBinding=null,controllerAttempt=null;
  async function controllerAuthority(senderId, adoption, captured) {
    var lease=captured||captureController(senderId,adoption);
    requireLease(lease);
    if(!lease.binding||!lease.proof||!lease.session)throw new Error('controller_required');
    var body={bindingId:lease.binding.id,controlToken:lease.proof};
    if(adoption){body.attemptId=adoption.attemptId;body.ticket=adoption.ticket;}
    var checked=await api('/v1/cast/controllers/check',{body:body},lease);
    requireLease(lease);
    if(playback!==lease.occurrence||!checked.body||!checked.body.binding||checked.body.binding.id!==lease.binding.id)throw new Error('controller_replaced');
    return lease;
  }
  async function controllerStatus(senderId, attemptId, captured) {
    var owner=playback,lease=await controllerAuthority(senderId,undefined,captured);
    if(owner)await api(sessionPath(owner.id),{method:'GET'},lease);
    requireLease(lease);
    if(owner!==playback||!isController(senderId))return;
    send(senderId,Object.assign(statusBody(lease),attemptId?{attemptId:attemptId}:{}));
  }
  var offerSequence=0;
  function offerCurrent(offer){return offer.sequence===offerSequence&&pairConnectionCurrent(offer.connection)&&offer.owner===controllerSender&&offer.generation===controllerGeneration&&offer.session===session&&offer.scope===scope&&offer.base===base&&offer.binding===controllerBinding&&offer.proof===controllerToken&&offer.playback===playback&&offer.present===controllerPresent;}
  async function takeControl(offer) {
    var result=null,senderId=offer.senderId,origin=offer.origin,connection=offer.connection;
    try{
      if(!offerCurrent(offer)||offer.accepted)throw new Error('controller_retired');
      offer.accepted=true;
      result=await redeemAt(origin,offer.code,offer.result.controllerBootstrapId);
      offer.code='';
      if(!offerCurrent(offer))throw new Error('controller_retired');
      var previous=controllerSender,generation=++controllerGeneration;adoptionGeneration++;controllerPresent=false;
      var installation=Object.freeze({connection:connection,generation:generation,result:result,origin:origin});
      await switchTo(result,origin,installation);requirePairInstallation(installation,true);
      var proof=token(24),binding=null;
      controllerToken=null;controllerBinding=null;controllerAttempt=null;
      if(result.controllerBootstrapId){
        var registered=await api('/v1/cast/controllers/register',{body:{bootstrapId:result.controllerBootstrapId,controlToken:proof}},undefined,installation);
        binding=registered.body.binding;
      }
      requirePairInstallation(installation,true);
      controllerToken=proof;controllerBinding=binding;controllerSender=senderId;controllerPresent=true;renewedStampRequired=false;
      if(previous&&previous!==senderId)send(previous,{type:'replaced'});
      send(senderId,{type:'paired',controlToken:proof,binding:binding,controller:controllerStamp(captureController(senderId,false))});
    }catch(error){offer.code='';if(result)discardPair({origin:origin,result:result});throw error;}
  }
  function handlePair(senderId, message,connection) {
    if(!pairConnectionCurrent(connection)){send(senderId,{type:'pair-failed',code:'pair_failed'});return;}
    var named = serverOrigin(message.origin);
    if (named === null) { send(senderId, {type: 'pair-failed', code: 'origin_refused', message: pairingMessage({code:'origin_refused'})}); return; }
    var requester=requesterName(message,senderId), code=String(message.code||'').trim().toUpperCase();
    if(pendingPair)denyPending('tv_busy');
    var offer={sequence:++offerSequence,connection:connection,senderId:senderId,requester:requester,origin:named,code:code,owner:controllerSender,generation:controllerGeneration,session:session,scope:scope,base:base,binding:controllerBinding,proof:controllerToken,playback:playback,present:controllerPresent};
    previewAt(named,code).then(function(result){
      if(!offerCurrent(offer))throw new Error('controller_retired');
      offer.result=result;
      var sameViewer=!!(scope&&result.scope&&result.scope.accountId===scope.accountId&&result.scope.profileId===scope.profileId&&result.scope.authority===scope.authority&&named===base);
      var busy=!!playback&&controllerSender!==senderId&&!sameViewer;
      if(!busy)return serial(function(){return takeControl(offer);});
      pendingPair=offer;
      send(senderId,{type:'pair-pending'});
      var here=!!controllerSender&&controllerPresent;
      askTakeover(requester,here);
      if(here){
        send(controllerSender,{type:'takeover-requested',requester:requester,controller:controllerStamp(captureController(controllerSender,false)),requestId:result.controllerBootstrapId});
        offer.timer=setTimeout(function(){if(pendingPair===offer)denyPending('tv_busy');},TAKEOVER_ASK_MS);
      }else offer.timer=setTimeout(function(){if(pendingPair===offer)allowPending();},TAKEOVER_ABSENT_MS);
    }).catch(function(error){offer.code='';send(senderId,{type:'pair-failed',code:error.code||'',message:pairingMessage(error)});});
  }
  // The TV remote (where a Google TV delivers keys to the page): Select allows, Back declines.
  if (window.addEventListener) window.addEventListener('keydown', function (event) {
    if (['Enter',' ','Escape','Backspace','GoBack','BrowserBack','ArrowUp','ArrowDown','ArrowLeft','ArrowRight'].indexOf(event.key) >= 0 && !observedRemoteKeys) {
      observedRemoteKeys = true;
      if (pendingPair) takeoverView(pendingPair.viewControllerPresent, Math.max(0, pendingPair.viewDeadline - Date.now()));
      playingView();
    }
    if (!pendingPair) return;
    if (event.key === 'Enter' || event.key === ' ') allowPending();
    else if (event.key === 'Escape' || event.key === 'Backspace' || event.key === 'GoBack' || event.key === 'BrowserBack') denyPending('tv_busy');
  });
  function isController(senderId) { return !!controllerSender && controllerPresent && senderId === controllerSender; }
  function statusBody(lease) {
    var status = {
      type:'status',...(controllerStamp(lease)?{controller:controllerStamp(lease)}:{}),...(playback?{occurrence:occurrenceFact(playback)}:{}),paired:!!session,itemId: playback ? playback.itemId || '' : '',...(playback&&playback.versionId?{versionId:playback.versionId}:{}),
      audioTracks: manager ? manager.getAudioTracksManager().getTracks().map(function(t){return {id:t.trackId,language:t.language||'',name:t.name||''};}) : [],
      textTracks: manager ? manager.getTextTracksManager().getTracks().map(function(t){return {id:t.trackId,language:t.language||'',name:t.name||''};}) : [],
      paused: paused(), state: recovering&&recovering.owner===playback?'recovering':playback?(paused()?'paused':'playing'):'idle'
    };
    // A bookkeeping fallback zero is not a receiver clock fact.
    return Object.assign(status, clockFacts());
  }

  // Root51: this is deliberately shared Cast/Google Home presentation. It
  // has no recipient authority. Only a fixed active-item display allow-list
  // leaves CAF; private Portico state stays on the validated custom channel.
  function displayImage(value){return typeof value==='string'&&value.length<=40960&&/^data:image\/(?:png|jpeg|webp|gif|avif|svg\+xml);base64,[A-Za-z0-9+/=]+$/.test(value)?value:'';}
  function platformMetadata(value){
    if(!value||typeof value!=='object')return;
    var out={};
    if([0,1,2,3,4].indexOf(value.metadataType)>=0)out.metadataType=value.metadataType;
    ['title','subtitle','seriesTitle','artist','albumName','albumArtist'].forEach(function(key){var text=cleanText(value[key],200);if(text)out[key]=text;});
    ['season','episode','trackNumber','discNumber'].forEach(function(key){if(Number.isSafeInteger(value[key])&&value[key]>=0)out[key]=value[key];});
    if(Array.isArray(value.images)){var images=value.images.slice(0,1).map(function(image){var url=displayImage(image&&image.url);return url?{url:url}:null;}).filter(Boolean);if(images.length)out.images=images;}
    return out;
  }
  function platformTracks(value){return !Array.isArray(value)?[]:value.slice(0,32).filter(function(track){return track&&Number.isSafeInteger(track.trackId)&&track.trackId>=0&&['TEXT','AUDIO','VIDEO'].indexOf(track.type)>=0;}).map(function(track){
    var out={trackId:track.trackId,type:track.type},name=cleanText(track.name,100);
    if(name&&!/^(?:[a-z][a-z0-9+.-]*:|[\\/]|[a-z]:[\\/])/i.test(name))out.name=name;
    if(typeof track.language==='string'&&/^[A-Za-z0-9-]{1,35}$/.test(track.language))out.language=track.language;
    if(['SUBTITLES','CAPTIONS','DESCRIPTIONS','CHAPTERS','METADATA'].indexOf(track.subtype)>=0)out.subtype=track.subtype;
    return out;
  });}
  function utf8Size(text){var size=0;for(var i=0;i<text.length;i++){var cp=text.codePointAt(i);size+=cp<=127?1:cp<=2047?2:cp<=65535?3:4;if(cp>65535)i++;}return size;}
  function platformEnvelopeSize(status,kind){
    // Count the whole encoded status + documented media-status envelope and
    // even an escaped outer payload, not string.length or the image alone.
    // A 48 KiB ceiling leaves 16 KiB of the 64 KiB transport limit for SDK
    // routing/protobuf overhead; no recipient fields are guessed or inferred.
    return utf8Size(JSON.stringify({namespace:'urn:x-cast:com.google.cast.media',payloadUtf8:JSON.stringify({type:kind,requestId:Number.MAX_SAFE_INTEGER,status:[status]})}));
  }
  function fitPlatformStatus(status,kind){
    var limit=48*1024,media=status.media;
    if(platformEnvelopeSize(status,kind)<=limit)return status;
    if(media&&media.metadata)delete media.metadata.images;
    while(media&&media.tracks&&media.tracks.length&&platformEnvelopeSize(status,kind)>limit){media.tracks.pop();if(status.activeTrackIds)status.activeTrackIds=status.activeTrackIds.filter(function(id){return media.tracks.some(function(track){return track.trackId===id;});});}
    if(media&&media.metadata)['albumArtist','albumName','artist','subtitle','seriesTitle','discNumber','trackNumber','season','episode'].forEach(function(key){if(platformEnvelopeSize(status,kind)>limit)delete media.metadata[key];});
    // Keep a final measured bound as well as the per-field limits. Optional
    // display fields are omitted individually; ordinary status is not disabled.
    if(media&&media.metadata&&platformEnvelopeSize(status,kind)>limit)delete media.metadata;
    if(media&&media.tracks&&platformEnvelopeSize(status,kind)>limit){delete media.tracks;delete status.activeTrackIds;}
    if(media&&platformEnvelopeSize(status,kind)>limit)delete status.media;
    return status;
  }
  function platformStatus(status,kind){
    kind=kind==='CLOUD_STATUS'?'CLOUD_STATUS':'MEDIA_STATUS';
    if(!status||typeof status!=='object')return null;
    var out={type:['MEDIA_STATUS','CLOUD_STATUS'].indexOf(status.type)>=0?status.type:kind};
    if(Number.isSafeInteger(status.mediaSessionId)&&status.mediaSessionId>=0)out.mediaSessionId=status.mediaSessionId;
    if(Number.isSafeInteger(status.requestId)&&status.requestId>=0)out.requestId=status.requestId;
    if(!playback){out.playerState='IDLE';return fitPlatformStatus(out,kind);}
    var occurrence=platformOccurrence,media=status.media||(manager&&manager.getMediaInformation());
    if(!occurrence||occurrence.id!==playback.id||occurrence.generation!==playback.generation||!media||media.contentId!==occurrence.contentId||!/^portico-[a-f0-9]{32}$/.test(media.contentId))return null;
    var safe={contentId:media.contentId};
    if(typeof media.contentType==='string'&&/^(?:audio|video|application)\/[A-Za-z0-9.+-]{1,80}$/.test(media.contentType))safe.contentType=media.contentType;
    if(['BUFFERED','LIVE','NONE'].indexOf(media.streamType)>=0)safe.streamType=media.streamType;
    if(Number.isFinite(media.duration)&&media.duration>=0)safe.duration=media.duration;
    var metadata=platformMetadata(media.metadata);if(metadata)safe.metadata=metadata;
    var tracks=platformTracks(media.tracks);if(tracks.length)safe.tracks=tracks;
    out.media=safe;
    var state=manager&&manager.getPlayerState(),time=manager&&manager.getCurrentTimeSec();
    if(['IDLE','PLAYING','PAUSED','BUFFERING'].indexOf(state)>=0)out.playerState=state;
    if(Number.isFinite(time)&&time>=0)out.currentTime=time;
    if(Number.isFinite(status.playbackRate)&&status.playbackRate>=0&&status.playbackRate<=4)out.playbackRate=status.playbackRate;
    if(Number.isSafeInteger(status.supportedMediaCommands)&&status.supportedMediaCommands>=0)out.supportedMediaCommands=status.supportedMediaCommands;
    if(['CANCELLED','INTERRUPTED','FINISHED','ERROR'].indexOf(status.idleReason)>=0)out.idleReason=status.idleReason;
    if(Array.isArray(status.activeTrackIds))out.activeTrackIds=status.activeTrackIds.slice(0,32).filter(function(id){return tracks.some(function(track){return track.trackId===id;});});
    return fitPlatformStatus(out,kind);
  }

  // --- Cast framework wiring ------------------------------------------------

  function attachCastFramework() {
    if (!window.cast || !window.cast.framework) { return; }
    var context = cast.framework.CastReceiverContext.getInstance();
    castContext=context; manager = context.getPlayerManager();
    // CAF's documented resolver keeps grant-bearing URLs out of its public media/queue status.
    manager.setMediaUrlResolver(mediaAddress);
    var types=cast.framework.messages.MessageType;
    if(types.MEDIA_STATUS)manager.setMessageInterceptor(types.MEDIA_STATUS,function(status){return platformStatus(status,'MEDIA_STATUS');});
    if(types.CLOUD_STATUS)manager.setMessageInterceptor(types.CLOUD_STATUS,function(status){return platformStatus(status,'CLOUD_STATUS');});
    // Media-channel commands from a sender that isn't the controller: pause and stop are allowed
    // (anyone in the room can do that with the remote); load, play and seek are not.
    function fromOther(request) {
      if(!request||!isController(request.senderId))return true;
      // A delayed CAF request cannot inherit the renewed controller generation.
      var custom=request.customData||(request.media&&request.media.customData);
      return renewedStampRequired&&!sameControllerStamp(custom&&custom.controller,controllerStamp(captureController(request.senderId,false)));
    }
    // A LOAD carries only a Portico item id in customData. It never carries a
    // media URL, a bearer or a media grant: the receiver resolves everything
    // itself with its own credential.
    manager.setMessageInterceptor(types.LOAD, async function (request) {
      if(request&&localRequests.has(request))return request;
      var custom = (request && request.media && request.media.customData) || {};
      if (!custom.itemId || fromOther(request)) { return null; }
      var lease;try{lease=await controllerAuthority(request.senderId);}catch(e){return null;}
      var identity = displayMeta(custom) || (request.media.metadata ? displayMeta({title: request.media.metadata.title, subtitle: request.media.metadata.subtitle || request.media.metadata.seriesTitle}) : null);
      return commandQueue(lease,function(){recoveries=0;networkRetries=0;return resolvePlayback(custom.itemId,custom.startSeconds,identity,custom.subtitleTrackId,undefined,lease,undefined,undefined,custom.versionId,custom.audioTrackId);}).then(function(load){requireLease(lease);platformOccurrence={id:playback.id,generation:playback.generation,contentId:load.media.contentId};showPlaying();scheduleReport(0);return load;}).catch(function(e){if(leaseCurrent(lease))showError(e&&(e.code||e.message));return null;});
    });
    // Play and pause are the player's own; the timeline reports them. A seek the server must
    // produce again is sent to it and the new presentation loaded here instead.
    // Progressive audio cannot use CAF's adaptive preload. No speculative URL
    // or server preparation is produced; ordinary completion owns the transition.
    if (types.PRELOAD) manager.setMessageInterceptor(types.PRELOAD, function(){return null;});
    // Standard queue mutation commands must pass the same controller gate as LOAD.
    ['GET_STATUS','QUEUE_GET_ITEMS','QUEUE_GET_ITEM_IDS'].forEach(function(name){if(types[name])manager.setMessageInterceptor(types[name],async function(request){if(fromOther(request))return null;try{var lease=await controllerAuthority(request.senderId),owner=playback;if(owner)await api(sessionPath(owner.id),{method:'GET'},lease);requireLease(lease);return playback===owner?request:null;}catch(e){return null;}});});
    // CAF maps QUEUE_NEXT/PREV to QUEUE_UPDATE (jump +/-1); those enum values
    // are not accepted by setMessageInterceptor, even though the SDK exposes them.
    ['QUEUE_INSERT','QUEUE_UPDATE','QUEUE_REMOVE','QUEUE_REORDER','QUEUE_LOAD'].forEach(function(name){if(types[name])manager.setMessageInterceptor(types[name],function(){return null;});});
    manager.setMessageInterceptor(types.PAUSE,function(request){return request;});
    manager.setMessageInterceptor(types.PLAY,async function(request){if(request&&localRequests.has(request))return request;if(fromOther(request))return null;try{var lease=await controllerAuthority(request.senderId);requireLease(lease);return request;}catch(e){return null;}});
    manager.setMessageInterceptor(types.SEEK,async function(request){
      if(request&&localRequests.has(request))return request;
      if (fromOther(request) || !playback || !Number.isFinite(request.currentTime)) return null;
      var owner=playback,lease;try{lease=await controllerAuthority(request.senderId);requireLease(lease);}catch(e){return null;}
      if(playback!==owner)return null;
      if (!owner.serverSeek) return request;
      seekTo(request.currentTime,lease).catch(function(){if(leaseCurrent(lease))notice(copy('notice.seekFailed'), 5000);});
      return null;
    });
    manager.setMessageInterceptor(types.STOP,function(request){if(request&&localRequests.has(request))return request;return serial(function(){return stopPlayback();}).then(function(){return request;}).catch(function(){return null;});});
    // A Cast device has no keyboard, so the sender delivers the pairing code it was issued
    // and then drives playback over this channel. What plays is always the library of the
    // person who paired, and only they drive it.
    context.addCustomMessageListener(NAMESPACE, async function (event) {
      var message = event.data || {};
      if (typeof message === 'string') { try { message = JSON.parse(message); } catch (e) { message = {}; } }
      var senderId = event.senderId;
      var reply = function (body) { send(senderId, body); };
      if(message.type==='renewal-ack'){
        var offer=renewalOffer;
        if(!offer||message.attemptId!==offer.id||senderId!==offer.sender||message.bindingId!==offer.binding.id||message.controlToken!==offer.proof||typeof message.ticket!=='string')return;
        try{
          var result=await post('/v1/cast/controllers/check',{bindingId:offer.binding.id,controlToken:offer.proof,attemptId:offer.id,ticket:message.ticket},offer.session.accessToken,offer.origin);
          if(renewalOffer!==offer||session!==offer.session||scope!==offer.scope||base!==offer.origin||controllerBinding!==offer.binding||controllerToken!==offer.proof||controllerGeneration!==offer.generation||controllerSender!==senderId||!pairConnectionCurrent(offer.connection)||JSON.stringify(result.binding)!==JSON.stringify(offer.binding))throw new Error('controller_retired');
          clearTimeout(offer.timer);renewalOffer=null;controllerPresent=true;
          send(senderId,{type:'renewal-complete',attemptId:offer.id,controller:controllerStamp(captureController(senderId,false))});
        }catch(_){if(renewalOffer===offer){clearTimeout(offer.timer);renewalOffer=null;reply({type:'pair-required'});}}
        return;
      }
      if(message.type==='adopt'){
        var originalBinding=controllerBinding;
        if(!originalBinding||message.bindingId!==originalBinding.id||message.controlToken!==controllerToken||typeof message.attemptId!=='string'||typeof message.ticket!=='string'){reply({type:'pair-required'});return;}
        adoptionGeneration++;
        var adoptionLease=captureController(senderId,true);
        try{
          var adoptionOwner=playback;
          await controllerAuthority(senderId,message,adoptionLease);
          if(adoptionOwner)await api(sessionPath(adoptionOwner.id),{method:'GET'},adoptionLease);
          requireLease(adoptionLease);if(playback!==adoptionOwner)throw new Error('occurrence_replaced');
          controllerGeneration++;controllerSender=senderId;controllerPresent=true;controllerAttempt=message.attemptId;
          send(senderId,Object.assign(statusBody(captureController(senderId,false)),{attemptId:message.attemptId}));
        }catch(e){if(leaseCurrent(adoptionLease))reply({type:'pair-required'});}
        return;
      }
      if (message.type === 'pair') { var pairConnection=capturePairConnection(senderId);handlePair(senderId, message,pairConnection); return; }
      if (message.type === 'takeover') {
        var offered=pendingPair,takeoverLease=captureController(senderId,false);try{await controllerAuthority(senderId,undefined,takeoverLease);requireLease(takeoverLease);}catch(e){if(leaseCurrent(takeoverLease))reply({type:'pair-required'});return;}
        if((renewedStampRequired||message.controller!==undefined)&&!sameControllerStamp(message.controller,controllerStamp(takeoverLease)))return;
        if(!offered||pendingPair!==offered||message.requestId!==undefined&&message.requestId!==offered.result.controllerBootstrapId)return;
        if (isController(senderId) && pendingPair) { if (message.allow === true) allowPending(); else denyPending('tv_busy'); }
        return;
      }
      if (!isController(senderId)) {
        // An occurrence-scoped private command never becomes a room command.
        if(message.target!==undefined){reply({type:'pair-required'});return;}
        // Nothing about what's playing, and no control, for a sender that didn't pair.
        if (message.type === 'pause' && playback) { mediaCommand('PAUSE'); return; }
        if (message.type === 'stop' && playback) { serial(stopPlayback).catch(function(){}); return; }
        reply(controllerSender ? {type: 'busy'} : {type: 'pair-required'});
        return;
      }
      var commandLease=captureController(senderId,false);try{await controllerAuthority(senderId,undefined,commandLease);requireLease(commandLease);}catch(e){if(leaseCurrent(commandLease))reply({type:'pair-required'});return;}
      var requestedTarget=readRemoteIdentity(message.target);
      if(renewedStampRequired&&!sameControllerStamp(message.controller||(requestedTarget&&requestedTarget.controller),controllerStamp(commandLease)))return;
      // Re-proved original authority may reconcile an old Stop after its live
      // occurrence is gone (or a successor is playing). Never execute a room Stop.
      if(message.type==='stop'&&requestedTarget&&typeof message.requestId==='string'){
        if(!sameControllerStamp(requestedTarget.controller,controllerStamp(commandLease))){reply({type:'control-failed',requestId:message.requestId,target:requestedTarget});return;}
        var priorStop=completedStops.get(message.requestId),liveStopScope=stopMutationScope();
        if(priorStop&&priorStop.until>Date.now()&&PorticoCastContracts.sameUserMutationScope(priorStop.scope,liveStopScope)&&sameRemoteIdentity(priorStop.target,requestedTarget)){
          reply(Object.assign({type:'stopped',requestId:message.requestId,target:requestedTarget},priorStop.clock));return;
        }
        var durableStop=stopMutations&&stopMutations.snapshot().find(function(entry){return entry.key===message.requestId&&entry.operation==='stop_playback_session'&&entry.intentId==='stop:'+requestedTarget.occurrence.sessionId+':'+requestedTarget.occurrence.generation&&PorticoCastContracts.sameUserMutationScope(entry.scope,liveStopScope);});
        if(durableStop&&sameControllerStamp(requestedTarget.controller,controllerStamp(commandLease))){
          try{
            await stopMutations.run({operation:durableStop.operation,method:durableStop.method,path:durableStop.path,target:durableStop.target,intentId:durableStop.intentId,key:durableStop.key,body:durableStop.body});
            requireLease(commandLease);
            var completed=stopMutations.snapshot().find(function(entry){return entry.key===durableStop.key&&entry.state==='confirmed';});
            if(completed)await stopMutations.acknowledge(completed.key);
            requireLease(commandLease);
            completedStops.set(message.requestId,{target:requestedTarget,scope:liveStopScope,until:Date.now()+30*86400000});
            while(completedStops.size>32)completedStops.delete(completedStops.keys().next().value);
            reply({type:'stopped',requestId:message.requestId,target:requestedTarget});
          }catch(e){if(leaseCurrent(commandLease))reply({type:'control-failed',requestId:message.requestId,target:requestedTarget});}
          return;
        }
        if(PorticoCastContracts.isUserMutationKey(message.requestId)){
          try{
            if(await originalStopReceipt(commandLease,message.requestId,requestedTarget)){
              // The server receipt survives receiver restart/history eviction.
              // It proves only this original occurrence; no current Stop is sent.
              reply({type:'stopped',requestId:message.requestId,target:requestedTarget});return;
            }
          }catch(e){if(leaseCurrent(commandLease))reply({type:'control-failed',requestId:message.requestId,target:requestedTarget});return;}
        }
      }
      if(message.target!==undefined&&(!requestedTarget||!sameRemoteIdentity(requestedTarget,remoteIdentity(commandLease,playback)))){
        if(requestedTarget&&typeof message.requestId==='string'&&/^[A-Za-z0-9._:-]{1,128}$/.test(message.requestId))reply({type:'control-failed',requestId:message.requestId,target:requestedTarget});
        return;
      }
      switch (message.type) {
        case 'load':
          if(message.queue!==undefined&&!queueRef(message.queue)){reply({type:'load-failed',requestId:message.requestId});break;}
          startPlayback(String(message.itemId || ''), typeof message.startSeconds === 'number' ? message.startSeconds : undefined, displayMeta(message), message.subtitleTrackId, queueRef(message.queue),commandLease,function(evidence){
            requireLease(commandLease);
            if(typeof message.requestId==='string'&&/^[A-Za-z0-9._:-]{1,128}$/.test(message.requestId))reply({type:'load-pending',requestId:message.requestId,itemId:String(message.itemId||''),startId:evidence.startId,sequence:evidence.sequence,controller:controllerStamp(commandLease)});
          },message.versionId,message.audioTrackId).then(function (accepted) {
            requireLease(commandLease);
            if (typeof message.requestId === 'string' && /^[A-Za-z0-9._:-]{1,128}$/.test(message.requestId)) reply({type:'loaded',requestId:message.requestId,itemId:accepted ? accepted.itemId : ''});
            broadcast();
          }).catch(function (error) {
            if(!leaseCurrent(commandLease))return;
            reply({ type: 'load-failed', requestId: typeof message.requestId==='string'?message.requestId.slice(0,128):undefined, code: error.code || error.message || '' });
          });
          break;
        case 'play': if (playback) mediaCommand('PLAY'); break;
        case 'pause': if (playback) mediaCommand('PAUSE'); break;
        case 'seek': if(Number.isFinite(message.positionSeconds)) seekTo(message.positionSeconds,commandLease).catch(function(){if(leaseCurrent(commandLease))notice(copy('notice.seekFailed'), 5000);}); break;
        case 'audio': if(Number.isSafeInteger(message.trackId)) manager.getAudioTracksManager().setActiveById(message.trackId); break;
        case 'subtitles': if(Array.isArray(message.trackIds)&&message.trackIds.length<=1&&message.trackIds.every(Number.isSafeInteger)) manager.getTextTracksManager().setActiveByIds(message.trackIds); break;
        case 'stop': {
          var stopOwner=playback,stoppedTarget=remoteIdentity(commandLease,stopOwner);
          commandQueue(commandLease,function(){return stopPlayback(false,commandLease,stopOwner,message.requestId);}).then(function(stopClock){
            requireLease(commandLease);
            if(stoppedTarget&&typeof message.requestId==='string'&&/^[A-Za-z0-9._:-]{1,128}$/.test(message.requestId)){
              // Bounded acknowledgement history only; pending commands live in the durable coordinator.
              completedStops.set(message.requestId,{target:stoppedTarget,scope:stopMutationScope(),clock:stopClock,until:Date.now()+30*86400000});
              while(completedStops.size>32)completedStops.delete(completedStops.keys().next().value);
              reply(Object.assign({type:'stopped',requestId:message.requestId,target:stoppedTarget},stopClock));
            }
            broadcast();
          }).catch(function(){if(leaseCurrent(commandLease)){notice(copy('notice.stopFailed'),5000);if(stoppedTarget&&typeof message.requestId==='string')reply({type:'control-failed',requestId:message.requestId,target:stoppedTarget});}});
          break;
        }
        case 'status': controllerStatus(senderId,controllerAttempt,commandLease).catch(function(){if(leaseCurrent(commandLease))reply({type:'pair-required'});});return;
        default: return;
      }
      broadcast();
    });
    // Status goes only to the controller (SEC-14).
    broadcast = function () { if (controllerSender && controllerPresent) {var sender=controllerSender,lease=captureController(sender,false);controllerStatus(sender,controllerAttempt,lease).catch(function(){if(leaseCurrent(lease))send(sender,{type:'pair-required'});});} };
    var events=cast.framework.events.EventType;
    manager.addEventListener(events.ERROR,function(e){
      var code=Number(e&&e.detailedErrorCode),owner=playback;
      // CAF MEDIA_DECODE, MEDIA_SRC_NOT_SUPPORTED and SOURCE_BUFFER_FAILURE.
      if ([102,104,110].indexOf(code)>=0) recover('decode_error',owner);
      // MEDIA_NETWORK, SEGMENT_NETWORK and HLS master/media playlist failures.
      else if ([103,301,311,312].indexOf(code)>=0) recover('network_error',owner);
      else if (code>=300&&code<=499) recover('decode_error',owner);
      else if (code===100||code===905) terminalPlayback(owner,'playback_failed');
    });
    manager.addEventListener(events.BUFFERING,function(e){viewBuffering=e.isBuffering===true;playingView();clearTimeout(bufferingTimer);var owner=playback;if(e.isBuffering)bufferingTimer=setTimeout(function(){recover('stalled_without_data',owner);},20000);});
    // The title is over whether or not its last report gets through: the TV must not stay on a
    // finished title, still reporting, with nobody connected to stop it.
    manager.addEventListener(events.MEDIA_FINISHED,function(event){
      if(!event)return;
      if(event.endedReason==='ERROR'){
        var failed=playback;
        // A companion ERROR end does not race the recovery already chosen for
        // this deck. An old completion cannot terminate a newly playing deck.
        if(failed&&manager.getPlayerState()==='IDLE'&&!(recovering&&recovering.owner===failed))terminalPlayback(failed,'playback_failed');
        return;
      }
      if(event.endedReason!==cast.framework.events.EndedReason.END_OF_STREAM)return;
      var owner=playback,occurrence=cafOccurrence;
      // MEDIA_FINISHED has no item/session field. Require the real single deck's
      // ended state and its loaded content identity, then consume this event once.
      if(!finishedOccurrence(owner,occurrence)||occurrence.finished)return;
      occurrence.finished=true;
      serial(async function(){
        if(playback!==owner||!finishedOccurrence(owner,occurrence))return;
        try {
          if(await advanceQueue(owner))return;
          // The title is over whether or not its last report gets through: a lost
          // report must not leave an error card over the idle screen. The session
          // is still closed below with the ordinary Stop.
          try{await report('ended');}catch(_){/* lost last report, not a playback failure */}
          finally{notice('');await stopPlayback(true);}
        } catch(error) {
          // A terminal queue fetch/admission/load failure is actionable. Do not
          // leave it as a swallowed completion or advance an unrelated occurrence.
          if(playback===owner)await stopPlayback(true).catch(function(){});
          showError(error&&(error.code||error.message));
          if(controllerSender)send(controllerSender,{type:'load-failed',code:'playback_failed'});
          broadcast();
        }
      }).catch(function(error){showError(error&&(error.code||error.message));});
    });
    if(events.PLAYER_LOADING)manager.addEventListener(events.PLAYER_LOADING,function(){
      pictureReady=false;viewBuffering=false;activeTrackKey=null;
      if(!viewLoading){if(meta&&meta.title)showLoading(meta);else viewCall('splash');}
    });
    // LOADED_DATA is the SDK's first-frame-loaded event; PLAYING confirms playback start.
    [events.LOADED_DATA,events.PLAYING].filter(Boolean).forEach(function(name){manager.addEventListener(name,function(){restoreTracks(platformOccurrence);pictureReady=true;viewLoading=false;stopPreparing();playingView(name===events.PLAYING);scheduleReport(0);broadcast();});});
    if(events.PLAYER_LOAD_COMPLETE)manager.addEventListener(events.PLAYER_LOAD_COMPLETE,function(){restoreTracks(platformOccurrence);});
    // A state change or a completed seek is reported at once.
    [events.PAUSE,events.SEEKED].filter(Boolean).forEach(function(name){manager.addEventListener(name,function(){if(name===events.SEEKED)observedSeek();playingView(true);scheduleReport(0);broadcast();});});
    [events.TIME_UPDATE,events.DURATION_CHANGE,events.MEDIA_STATUS].filter(Boolean).forEach(function(name){manager.addEventListener(name,function(){if(name===events.TIME_UPDATE&&networkRetries&&position()>networkRetryPosition)networkRetries=0;playingView();});});
    context.addEventListener(cast.framework.system.EventType.SENDER_CONNECTED,function(e){if(e&&e.senderId){bumpConnection(e.senderId);disconnectedSenders.delete(e.senderId);if(e.senderId===controllerSender)controllerPresent=false;}});
    context.addEventListener(cast.framework.system.EventType.SENDER_DISCONNECTED,function(e){
      if(e&&e.senderId){bumpConnection(e.senderId);disconnectedSenders.add(e.senderId);}
      if (e && e.senderId && e.senderId === controllerSender) {
        controllerPresent = false;
        // Someone was waiting for the controller's answer: with the controller gone, the TV says who is taking over, then switches.
        if (pendingPair) { pendingPair.present=false;clearTimeout(pendingPair.timer); askTakeover(pendingPair.requester, false); pendingPair.timer = setTimeout(allowPending, TAKEOVER_ABSENT_MS); }
      }
      if (pendingPair && e && e.senderId === pendingPair.senderId) { clearTimeout(pendingPair.timer); pendingPair.code='';pendingPair = null; hideTakeover(); }
      // Nobody is connected: the TV has no controller, but it goes on playing. A phone that sleeps or
      // leaves Wi-Fi drops its connection; only Stop, the end of the title or the server ends playback.
      if(context.getSenders().length===0){controllerPresent=false;}
    });
    var statusInterval=setInterval(function(){if(playback){broadcast();playingView();}},2000);
    if(cast.framework.system.EventType.SHUTDOWN)context.addEventListener(cast.framework.system.EventType.SHUTDOWN,function(){
      shuttingDown=true;if(stopMutations)stopMutations.dispose();activeRequests.forEach(function(active){clearTimeout(active.timer);active.abort.abort();});activeRequests.clear();
      resetViewPlayback();clearInterval(statusInterval);clearTimeout(reportTimer);clearTimeout(bufferingTimer);clearTimeout(errorTimer);clearTimeout(noticeTimer);if(pendingPair)clearTimeout(pendingPair.timer);
    });
    // The standard idle timeout closes the app when nothing is playing and nobody is connected (CAST-01 §5).
    context.start({ customNamespaces: (function () { var n = {}; n[NAMESPACE] = cast.framework.system.MessageType.JSON; return n; })() });
  }

  showIdle();
  attachCastFramework();

  // Exposed for the server's own smoke test page and for manual diagnosis.
  window.porticoCastReceiver = {
    buildProfile: buildProfile,
    probeAudioOutput: probeAudioOutput,
    redeem: redeem,
    reconnect: reconnect,
    startPlayback: startPlayback,
    state: function () { return { paired: !!session, scope: scope, playbackId: playback && playback.id, controller: controllerSender, pending: pendingPair ? pendingPair.senderId : null }; }
  };
})();
