' Focus in the player: with the controls hidden the picture holds it, and any key raises the
' controls with focus on Play/Pause. One row of buttons; Up goes to the seek bar; Back hides the
' controls, and Back again leaves the player.
sub init()
    c = colours()
    m.video = m.top.findNode("video")
    m.controls = m.top.findNode("controls")
    m.notice = m.top.findNode("notice")
    m.buttons = m.top.findNode("buttons")
    m.ring = m.top.findNode("ring")
    m.knob = m.top.findNode("knob")
    m.knobRing = m.top.findNode("knobRing")
    m.played = m.top.findNode("played")
    m.hide = m.top.findNode("hide")
    m.report = m.top.findNode("report")
    m.tick = m.top.findNode("tick")
    m.settle = m.top.findNode("settle")
    m.again = m.top.findNode("again")
    setType(m.top.findNode("title"), "title", c.text)
    setType(m.top.findNode("sub"), "meta", c.textSecondary)
    setType(m.top.findNode("clock"), "heading", c.text)
    setType(m.top.findNode("ends"), "caption", c.textSecondary)
    setType(m.top.findNode("at"), "metaStrong", c.textSecondary)
    setType(m.top.findNode("left"), "metaStrong", c.textSecondary)
    m.video.observeField("state", "onVideoState")
    m.video.observeField("position", "onPosition")
    m.hide.observeField("fire", "lower")
    m.report.observeField("fire", "sendProgress")
    m.tick.observeField("fire", "paint")
    m.settle.observeField("fire", "applySeek")
    m.again.observeField("fire", "start")
    m.retry = m.top.findNode("retry")
    m.retry.observeField("fire", "reload")
    m.wait = m.top.findNode("wait")
    m.top.findNode("spinner").scaleRotateCenter = [32, 32]
    setType(m.top.findNode("waitTitle"), "heading", c.text)
    setType(m.top.findNode("waitText"), "body", c.textSecondary)
    m.tries = 0          ' reloads since the picture last ran
    m.last = 0           ' the last place the picture was seen at, in seconds
    m.notice.observeField("pressed", "onNotice")
    m.session = invalid
    m.focus = 1          ' 0 back ten, 1 play/pause, 2 forward thirty, 3 Subtitles, 4 Audio, 5 Quality; 6 is the seek bar
    m.options = invalid  ' the title's versions, tracks and qualities (playback-options)
    m.pending = -1       ' where a scrub will land, in seconds; -1 when none
    m.duration = 0
    m.ended = false
    makeButtons()
end sub

sub makeButtons()
    specs = [{icon: "skip-back-44", size: 72, x: 830, seconds: "10"}, {icon: "pause-40", size: 100, x: 910}, {icon: "skip-forward-44", size: 72, x: 1018, seconds: "30"}]
    m.spots = []
    for each spec in specs
        plate = m.buttons.createChild("Poster")
        plate.uri = "pkg:/images/disc.png"
        plate.width = spec.size
        plate.height = spec.size
        plate.translation = [spec.x, (100 - spec.size) / 2]
        icon = m.buttons.createChild("Poster")
        icon.uri = "pkg:/images/i-" + spec.icon + ".png"
        parts = spec.icon.split("-")
        iconSize = parts[parts.count() - 1].toInt()
        icon.width = iconSize
        icon.height = iconSize
        icon.translation = [spec.x + (spec.size - iconSize) / 2, (100 - iconSize) / 2]
        number = invalid
        if spec.seconds <> invalid
            ' how far the arrow skips, drawn inside it
            number = m.buttons.createChild("Label")
            number.text = spec.seconds
            number.width = spec.size
            number.height = spec.size
            number.horizAlign = "center"
            number.vertAlign = "center"
            number.translation = [spec.x, (100 - spec.size) / 2 + 2]
            setType(number, "skipSeconds")
        end if
        m.spots.push({plate: plate, icon: icon, number: number, x: spec.x, size: spec.size})
    end for
    ' Subtitles, Audio and Quality: each says its current choice and opens the side sheet
    c = colours()
    m.pills = []
    x = 1262
    for each name in ["Subtitles", "Audio", "Quality"]
        plate = m.buttons.createChild("Poster")
        plate.uri = "pkg:/images/round-14.9.png"
        plate.height = 72
        plate.width = 180
        plate.translation = [x, 14]
        head = m.buttons.createChild("Label")
        head.text = name
        head.translation = [x + 18, 22]
        setType(head, "tag", c.textSecondary)
        value = m.buttons.createChild("Label")
        value.translation = [x + 18, 49]
        value.width = 144
        setType(value, "caption", c.textTertiary)
        m.pills.push({plate: plate, head: head, value: value, x: x, name: name})
        x = x + 192
    end for
end sub

sub onActive()
    if not m.top.active then return
    m.top.setFocus(true)
    if m.session = invalid and not m.ended then start()
end sub

' --- the session with the server -----------------------------------------------------------

sub start()
    item = m.top.item
    title = item.title
    sub_ = ""
    if item.kind = "episode"
        if item.rootTitle <> invalid then title = item.rootTitle
        sub_ = (episodeCode(item) + "   " + item.title).trim()
    end if
    m.top.findNode("title").text = title
    m.top.findNode("sub").text = sub_
    m.top.findNode("waitTitle").text = title
    busy("Getting ready…")
    body = {itemId: item.id}
    if m.top.startOver then body.startPositionMs = 0
    if m.resumeAt <> invalid then body.startPositionMs = m.resumeAt
    ' one start is one request however often it is asked again while the stream gets ready (202)
    if m.startKey = invalid then m.startKey = createObject("roDeviceInfo").getRandomUUID()
    api("POST", "/v1/playback/sessions", body, "onSession", invalid, false, m.startKey)
    if m.options = invalid then api("GET", "/v1/items/" + item.id + "/playback-options", invalid, "onOptions")
end sub

sub onOptions(event as object)
    result = event.getData()
    if result.ok then m.options = result.data
    paint()
end sub

' --- Subtitles, Audio, Quality --------------------------------------------------------------

function languageName(code as dynamic) as string
    if code = invalid or code = "" or code = "und" then return "Unknown"
    names = {en: "English", eng: "English", fr: "French", fre: "French", fra: "French", es: "Spanish", spa: "Spanish", de: "German", ger: "German", deu: "German", it: "Italian", ita: "Italian", pt: "Portuguese", por: "Portuguese", ja: "Japanese", jpn: "Japanese", ko: "Korean", kor: "Korean", zh: "Chinese", chi: "Chinese", zho: "Chinese", nl: "Dutch", nld: "Dutch", dut: "Dutch", sv: "Swedish", swe: "Swedish", da: "Danish", dan: "Danish", no: "Norwegian", nor: "Norwegian", fi: "Finnish", fin: "Finnish", pl: "Polish", pol: "Polish", ru: "Russian", rus: "Russian", ar: "Arabic", ara: "Arabic", hi: "Hindi", hin: "Hindi", tr: "Turkish", tur: "Turkish"}
    if names[lcase(code)] <> invalid then return names[lcase(code)]
    return ucase(code)
end function

function currentVersion() as dynamic
    if m.options = invalid or m.session = invalid then return invalid
    for each version in m.options.versions
        if version.id = m.session.versionId then return version
    end for
    if m.options.versions.count() > 0 then return m.options.versions[0]
    return invalid
end function

function subtitleLabel(track as object) as string
    text = languageName(track.language)
    if track.sdh = true then text = text + " (SDH)"
    if track.forced = true then text = text + " (forced)"
    return text
end function

function audioLabel(track as object) as string
    text = languageName(track.language)
    if track.channels <> invalid
        if track.channels = 1 then text = text + ", mono" else if track.channels = 2 then text = text + ", stereo" else if track.channels = 6 then text = text + ", 5.1" else if track.channels = 8 then text = text + ", 7.1" else text = text + ", " + track.channels.toStr() + " channels"
    end if
    if track.commentary = true then text = text + " (commentary)"
    return text
end function

function qualityLabel(q as object) as string
    if q.mode = "original" then return "Original"
    if q.mode = "automatic" then return "Automatic"
    text = ""
    if q.id <> invalid then text = q.id
    if q.maxVideoBitrateKbps <> invalid
        mbps = q.maxVideoBitrateKbps / 1000
        if mbps >= 1 then text = text + ", " + str(int(mbps * 10) / 10).trim() + " Mbps" else text = text + ", " + q.maxVideoBitrateKbps.toStr() + " kbps"
    end if
    return text
end function

' What each of the three says now: [subtitles, audio, quality].
function currentChoices() as object
    out = ["Off", "", ""]
    p = m.presentation
    version = currentVersion()
    if p = invalid or version = invalid then return out
    if p.subtitle <> invalid
        for each track in version.subtitles
            if track.id = p.subtitle.trackId then out[0] = subtitleLabel(track)
        end for
    end if
    for each track in version.audio
        if p.audioTrackId <> invalid and track.id = p.audioTrackId then out[1] = audioLabel(track)
    end for
    if p.quality <> invalid then out[2] = qualityLabel(p.quality)
    return out
end function

sub openOptions(which as integer)
    version = currentVersion()
    p = m.presentation
    if version = invalid or p = invalid then return
    rows = []
    if which = 0
        rows.push({id: "sub:", text: "Off", current: p.subtitle = invalid})
        for each track in version.subtitles
            rows.push({id: "sub:" + track.id, text: subtitleLabel(track), current: p.subtitle <> invalid and p.subtitle.trackId = track.id})
        end for
    else if which = 1
        for each track in version.audio
            rows.push({id: "aud:" + track.id, text: audioLabel(track), current: p.audioTrackId <> invalid and p.audioTrackId = track.id})
        end for
    else
        for i = 0 to m.options.qualities.count() - 1
            q = m.options.qualities[i]
            same = (p.quality <> invalid and p.quality.mode = q.mode and (q.mode <> "fixed" or p.quality.id = q.id))
            rows.push({id: "qua:" + i.toStr(), text: qualityLabel(q), current: same})
        end for
    end if
    if rows.count() = 0 then return
    m.hide.control = "stop"
    m.top.menu = {eyebrow: m.top.findNode("title").text, title: ["Subtitles", "Audio", "Quality"][which], status: "", rows: rows}
end sub

sub onMenuChosen(event as object)
    if m.session = invalid then return
    id = event.getData()
    kind = left(id, 4)
    value = mid(id, 5)
    change = {}
    if kind = "sub:"
        if value = "" then change.subtitles = {trackId: invalid} else change.subtitles = {trackId: value}
    else if kind = "aud:"
        change.audio = {trackId: value}
    else if kind = "qua:"
        q = m.options.qualities[value.toInt()]
        change.quality = {mode: q.mode}
        if q.id <> invalid then change.quality.id = q.id
    end if
    change.positionMs = int(m.video.position * 1000)
    api("PATCH", "/v1/playback/sessions/" + m.session.id, change, "onChanged", {at: m.video.position})
end sub

' The server answers a change with how the title reaches this TV now: a new stream is loaded where
' the picture was; the same stream only changes which subtitle is drawn.
sub onChanged(event as object)
    result = event.getData()
    if m.session = invalid then return
    if not result.ok
        raise()
        return
    end if
    before = m.presentation
    m.session = result.data
    p = m.session.presentation
    if p.url <> before.url
        if p.startPositionMs = invalid or p.startPositionMs = 0 then p.startPositionMs = int(event.getRoSGNode().ctx.at * 1000)
        load(p)
    else
        m.presentation = p
        drawSubtitle(p)
    end if
    raise()
end sub

' Roku draws a text subtitle from its file beside the stream; one burned into the picture needs nothing here.
sub drawSubtitle(p as object)
    chosen = ""
    if p.subtitle <> invalid and p.subtitles <> invalid
        for each file in p.subtitles
            if file.trackId = p.subtitle.trackId then chosen = m.global.origin + file.url
        end for
    end if
    if chosen <> ""
        m.video.subtitleTrack = chosen
        m.video.globalCaptionMode = "On"
    else
        m.video.globalCaptionMode = "Off"
    end if
end sub

sub onSession(event as object)
    result = event.getData()
    if m.ended then return
    if result.ok and result.status = 202
        wait = 1
        if result.data <> invalid and result.data.retryAfterMs <> invalid then wait = result.data.retryAfterMs / 1000
        if wait < 0.5 then wait = 0.5
        m.again.duration = wait
        m.again.control = "start"      ' the stream is being made ready
        return
    end if
    if not result.ok
        fail(result.error.message)
        return
    end if
    m.session = result.data
    m.startKey = invalid
    load(m.session.presentation)
    if m.session.lease <> invalid and m.session.lease.reportEveryMs <> invalid then m.report.duration = m.session.lease.reportEveryMs / 1000
    m.report.control = "start"
    m.tick.control = "start"
end sub

function streamFormat(p as object) as string
    if p.mode <> "direct" then return "hls"
    container = ""
    if p.container <> invalid then container = lcase(p.container)
    if container = "mkv" or container = "matroska" or container = "webm" then return "mkv"
    if container = "ts" or container = "m2ts" or container = "mpegts" then return "ts"
    return "mp4"
end function

' The waiting state: shown from the moment Play is pressed until the picture runs, and again
' whenever the stream has to catch up.
sub busy(text as dynamic)
    showing = (text <> invalid)
    m.wait.visible = showing
    if showing
        m.top.findNode("waitText").text = text
        m.top.findNode("spin").control = "start"
    else
        m.top.findNode("spin").control = "stop"
    end if
end sub

' A stream the server is still making can answer "not there yet" for a moment, and Roku's player
' gives up at once. So a stop that is not the end of the title is tried again from where the
' picture was, a few times, before it is called a failure.
sub again()
    if m.session = invalid or m.ended then return
    if m.tries >= 6
        fail("This title could not be played on this TV.")
        return
    end if
    m.tries = m.tries + 1
    m.video.control = "stop"
    busy("Getting ready…")
    ' The server is told the player could not play this. It may answer with another way for the
    ' title to reach this TV, which is then what is played.
    detail = ""
    if m.video.errorMsg <> invalid then detail = m.video.errorMsg
    api("PATCH", "/v1/playback/sessions/" + m.session.id, {failure: {code: "player_error", detail: detail}, positionMs: int(m.last * 1000)}, "onFailureAnswer", {id: m.session.id})
end sub

sub onFailureAnswer(event as object)
    result = event.getData()
    if m.session = invalid or m.ended or event.getRoSGNode().ctx.id <> m.session.id then return
    if result.ok and result.data <> invalid and result.data.presentation <> invalid
        replaced = (result.data.presentation.url <> m.presentation.url)
        m.session = result.data
        m.presentation = result.data.presentation
        if replaced
            reload()          ' a replacement stream: play it now
            return
        end if
    else if result.status = 404 or result.status = 410
        fail("This title could not be played on this TV.")
        return
    end if
    m.retry.duration = 2 + m.tries
    m.retry.control = "start"
end sub

sub reload()
    if m.session = invalid or m.ended then return
    p = m.presentation
    p.startPositionMs = int(m.last * 1000)
    load(p)
end sub

sub load(p as object)
    content = createObject("roSGNode", "ContentNode")
    content.url = m.global.origin + p.url
    content.streamFormat = streamFormat(p)
    content.title = m.top.findNode("title").text
    if p.startPositionMs <> invalid and p.startPositionMs > 0
        content.playStart = p.startPositionMs / 1000
        m.last = p.startPositionMs / 1000
    end if
    tracks = []
    if p.subtitles <> invalid
        for each file in p.subtitles
            tracks.push({TrackName: m.global.origin + file.url, Language: "", Description: file.trackId})
        end for
    end if
    content.subtitleTracks = tracks
    m.presentation = p
    m.video.content = content
    m.video.control = "play"
    drawSubtitle(p)
end sub

sub sendProgress()
    if m.session = invalid then return
    state = "playing"
    if m.video.state = "paused" then state = "paused"
    if m.video.state = "buffering" then state = "buffering"
    api("PUT", "/v1/playback/sessions/" + m.session.id + "/progress", {state: state, positionMs: int(m.video.position * 1000)}, "onProgress", {id: m.session.id})
end sub

sub onProgress(event as object)
    result = event.getData()
    if m.session = invalid or event.getRoSGNode().ctx.id <> m.session.id then return
    if result.status = 410
        ' The server ended this session (stopped from another device, or a limit).
        leave(false)
    else if result.status = 404
        ' The server restarted: start again where the picture is.
        m.resumeAt = int(m.video.position * 1000)
        m.session = invalid
        m.video.control = "stop"
        start()
    end if
end sub

' Stop the session with where the picture got to, and hand the screen back.
sub leave(tell as boolean)
    if m.ended then return
    m.ended = true
    for each timer in [m.report, m.tick, m.hide, m.settle, m.again, m.retry]
        timer.control = "stop"
    end for
    busy(invalid)
    at = int(m.video.position * 1000)
    if at = 0 and m.last > 0 then at = int(m.last * 1000)
    m.video.control = "stop"
    if tell and m.session <> invalid then api("DELETE", "/v1/playback/sessions/" + m.session.id, {positionMs: at}, "onStopped")
    m.session = invalid
    m.top.closed = true
end sub

sub onStopped()
end sub

sub onVideoState()
    state = m.video.state
    if state = "finished"
        ' the end of the title, or a stream that stopped short of it
        if m.duration <= 0 or m.last >= m.duration - 5 then leave(true) else again()
    else if state = "error"
        again()
    else if state = "playing"
        if m.video.duration > 0 then m.duration = m.video.duration
        busy(invalid)
        paint()
    else if state = "buffering"
        busy("Catching up…")
        paint()
    else
        paint()
    end if
end sub

sub fail(message as string)
    busy(invalid)
    for each timer in [m.report, m.tick, m.hide, m.retry]
        timer.control = "stop"
    end for
    m.video.control = "stop"
    m.controls.visible = false
    m.notice.icon = "warning"
    m.notice.title = "Playback failed"
    m.notice.body = message
    m.notice.buttons = [{id: "retry", text: "Try again", icon: "refresh"}, {id: "leave", text: "Go back", icon: ""}]
    m.notice.visible = true
    m.notice.active = true
end sub

sub onNotice(event as object)
    if event.getData() = "retry"
        m.notice.visible = false
        m.session = invalid
        m.top.setFocus(true)
        start()
    else
        leave(m.session <> invalid)
    end if
end sub

' --- the controls ---------------------------------------------------------------------------

sub raise()
    m.controls.visible = true
    m.hide.control = "stop"
    if m.video.state <> "paused" then m.hide.control = "start"
    paint()
end sub

sub lower()
    if m.video.state = "paused" then return     ' paused: the controls stay
    m.controls.visible = false
    m.focus = 1
    m.pending = -1
end sub

function clockText(seconds as float) as string
    total = int(seconds)
    if total < 0 then total = 0
    h = int(total / 3600)
    mins = int((total mod 3600) / 60)
    secs = total mod 60
    two = function(n as integer) as string
        if n < 10 then return "0" + n.toStr()
        return n.toStr()
    end function
    if h > 0 then return h.toStr() + ":" + two(mins) + ":" + two(secs)
    return mins.toStr() + ":" + two(secs)
end function

function timeOfDay(offsetSeconds as integer) as string
    now = createObject("roDateTime")
    now.toLocalTime()
    stamp = createObject("roDateTime")
    stamp.fromSeconds(now.asSeconds() + offsetSeconds)
    h = stamp.getHours()
    suffix = " AM"
    if h >= 12 then suffix = " PM"
    h = h mod 12
    if h = 0 then h = 12
    mins = stamp.getMinutes()
    text = mins.toStr()
    if mins < 10 then text = "0" + text
    return h.toStr() + ":" + text + suffix
end function

sub onPosition()
    at = m.video.position
    if at > 0
        ' the picture has run on from where it was last started: it is healthy again
        if at > m.last + 8 then m.tries = 0
        if m.pending < 0 then m.last = at
    end if
    if m.controls.visible then paint()
end sub

sub paint()
    if not m.controls.visible then return
    c = colours()
    duration = m.duration
    if duration <= 0 and m.top.item.durationMs <> invalid then duration = m.top.item.durationMs / 1000
    position = m.video.position
    if m.pending >= 0 then position = m.pending
    fraction = 0
    if duration > 0 then fraction = position / duration
    if fraction > 1 then fraction = 1
    m.played.width = 1728 * fraction
    m.knob.translation = [96 + 1728 * fraction - 11, 820]
    m.knobRing.translation = [96 + 1728 * fraction - 30, 801]
    m.knobRing.visible = (m.focus = 6)
    m.top.findNode("at").text = clockText(position)
    m.top.findNode("left").text = "-" + clockText(duration - position)
    m.top.findNode("clock").text = timeOfDay(0)
    m.top.findNode("ends").text = "Ends " + timeOfDay(int(duration - position))
    paused = (m.video.state = "paused")
    if paused then m.spots[1].icon.uri = "pkg:/images/i-play-40.png" else m.spots[1].icon.uri = "pkg:/images/i-pause-40.png"
    for i = 0 to 2
        spot = m.spots[i]
        if spot.number <> invalid
            if i = m.focus then spot.number.color = c.actionInk else spot.number.color = c.text
        end if
        if i = m.focus
            spot.plate.blendColor = "0xD8E6EEFF"
            spot.icon.blendColor = c.actionInk
            across = spot.size * 296 / 216
            m.ring.width = across
            m.ring.height = across
            m.ring.translation = [spot.x + spot.size / 2 - across / 2, 906 + 50 - across / 2]
        else
            spot.plate.blendColor = "0x1D3848E6"
            spot.icon.blendColor = c.text
        end if
    end for
    m.ring.visible = (m.focus < 3)
    ' the three choices
    values = currentChoices()
    pillRing = m.top.findNode("pillRing")
    pillRing.visible = (m.focus >= 3 and m.focus <= 5)
    for i = 0 to 2
        pill = m.pills[i]
        pill.value.text = values[i]
        on = (m.focus = i + 3)
        if on
            pill.plate.blendColor = "0xD8E6EEFF"
            pill.head.color = c.actionInk
            pill.value.color = c.actionInk
            out = metrics().focusSpread + 6
            pillRing.translation = [pill.x - out, 906 + 14 - out]
            pillRing.width = 180 + out * 2
            pillRing.height = 72 + out * 2
        else
            pill.plate.blendColor = "0x1D3848E6"
            pill.head.color = c.textSecondary
            pill.value.color = c.textTertiary
        end if
    end for
end sub

sub togglePause()
    if m.video.state = "paused"
        m.video.control = "resume"
    else if m.video.state = "playing" or m.video.state = "buffering"
        m.video.control = "pause"
    end if
    sendProgress()
    raise()
end sub

' Move the place a scrub will land; the picture follows once the thumb rests.
sub nudge(seconds as float)
    duration = m.duration
    if duration <= 0 then return
    if m.pending < 0 then m.pending = m.video.position
    m.pending = m.pending + seconds
    if m.pending < 0 then m.pending = 0
    if m.pending > duration - 2 then m.pending = duration - 2
    m.settle.control = "stop"
    m.settle.control = "start"
    raise()
end sub

sub applySeek()
    if m.pending < 0 or m.session = invalid then return
    target = m.pending
    m.pending = -1
    m.last = target
    if m.presentation.mode = "direct"
        m.video.seek = target
    else
        ' A converted stream is the server's: it is told, and answers with where to play from.
        api("PATCH", "/v1/playback/sessions/" + m.session.id, {seek: {positionMs: int(target * 1000), id: createObject("roDeviceInfo").getRandomUUID()}}, "onSeekAnswer", {target: target})
    end if
    sendProgress()
end sub

sub onSeekAnswer(event as object)
    result = event.getData()
    if m.session = invalid then return
    target = event.getRoSGNode().ctx.target
    if result.ok and result.data.presentation <> invalid and result.data.presentation.url <> m.presentation.url
        m.session = result.data
        load(m.session.presentation)
    else
        m.video.seek = target
    end if
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not press then return false
    if m.notice.visible then return key <> "back"
    if key = "back"
        if m.controls.visible and m.video.state <> "paused"
            m.controls.visible = false
            m.focus = 1
        else
            leave(true)
        end if
        return true
    end if
    ' the remote's own transport keys act at once, whatever has focus
    if key = "play"
        togglePause()
        return true
    else if key = "rewind"
        nudge(-10)
        return true
    else if key = "fastforward"
        nudge(30)
        return true
    else if key = "replay"
        nudge(-10)
        return true
    end if
    if not m.controls.visible
        raise()          ' any other key raises the controls, on Play/Pause
        return true
    end if
    if key = "OK"
        if m.focus = 0
            nudge(-10)
        else if m.focus = 1
            togglePause()
        else if m.focus = 2
            nudge(30)
        else if m.focus <= 5
            openOptions(m.focus - 3)
            return true
        else
            applySeek()
        end if
    else if key = "left"
        if m.focus = 6 then nudge(-10) else if m.focus > 0 then m.focus = m.focus - 1
    else if key = "right"
        if m.focus = 6 then nudge(10) else if m.focus < 5 then m.focus = m.focus + 1
    else if key = "up"
        if m.focus < 6
            m.row = m.focus
            m.focus = 6
        end if
    else if key = "down"
        if m.focus = 6
            m.focus = 1
            if m.row <> invalid then m.focus = m.row
        end if
    end if
    raise()
    return true
end function
