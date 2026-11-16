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
    m.notice.observeField("pressed", "onNotice")
    m.session = invalid
    m.focus = 1          ' 0 back ten, 1 play/pause, 2 forward thirty; 3 is the seek bar
    m.pending = -1       ' where a scrub will land, in seconds; -1 when none
    m.duration = 0
    m.ended = false
    makeButtons()
end sub

sub makeButtons()
    specs = [{icon: "back10-36", size: 72, x: 830}, {icon: "pause-40", size: 100, x: 910}, {icon: "forward10-36", size: 72, x: 1018}]
    m.spots = []
    for each spec in specs
        plate = m.buttons.createChild("Poster")
        plate.uri = "pkg:/images/disc.png"
        plate.width = spec.size
        plate.height = spec.size
        plate.translation = [spec.x, (100 - spec.size) / 2]
        icon = m.buttons.createChild("Poster")
        icon.uri = "pkg:/images/i-" + spec.icon + ".png"
        iconSize = spec.icon.split("-")[1].toInt()
        icon.width = iconSize
        icon.height = iconSize
        icon.translation = [spec.x + (spec.size - iconSize) / 2, (100 - iconSize) / 2]
        m.spots.push({plate: plate, icon: icon, x: spec.x, size: spec.size})
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
    body = {itemId: item.id}
    if m.top.startOver then body.startPositionMs = 0
    if m.resumeAt <> invalid then body.startPositionMs = m.resumeAt
    api("POST", "/v1/playback/sessions", body, "onSession")
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

sub load(p as object)
    content = createObject("roSGNode", "ContentNode")
    content.url = m.global.origin + p.url
    content.streamFormat = streamFormat(p)
    content.title = m.top.findNode("title").text
    if p.startPositionMs <> invalid and p.startPositionMs > 0 then content.playStart = p.startPositionMs / 1000
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
    for each timer in [m.report, m.tick, m.hide, m.settle, m.again]
        timer.control = "stop"
    end for
    at = int(m.video.position * 1000)
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
        leave(true)
    else if state = "error"
        if m.session <> invalid then api("PATCH", "/v1/playback/sessions/" + m.session.id, {failure: {code: "player_error", detail: m.video.errorMsg}}, "onStopped")
        fail("This title could not be played on this TV.")
    else if state = "playing"
        if m.video.duration > 0 then m.duration = m.video.duration
        paint()
    else
        paint()
    end if
end sub

sub fail(message as string)
    for each timer in [m.report, m.tick, m.hide]
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
    m.knobRing.visible = (m.focus = 3)
    m.top.findNode("at").text = clockText(position)
    m.top.findNode("left").text = "-" + clockText(duration - position)
    m.top.findNode("clock").text = timeOfDay(0)
    m.top.findNode("ends").text = "Ends " + timeOfDay(int(duration - position))
    paused = (m.video.state = "paused")
    if paused then m.spots[1].icon.uri = "pkg:/images/i-play-40.png" else m.spots[1].icon.uri = "pkg:/images/i-pause-40.png"
    for i = 0 to 2
        spot = m.spots[i]
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
        else
            applySeek()
        end if
    else if key = "left"
        if m.focus = 3 then nudge(-10) else if m.focus > 0 then m.focus = m.focus - 1
    else if key = "right"
        if m.focus = 3 then nudge(10) else if m.focus < 2 then m.focus = m.focus + 1
    else if key = "up"
        if m.focus < 3
            m.row = m.focus
            m.focus = 3
        end if
    else if key = "down"
        if m.focus = 3
            m.focus = 1
            if m.row <> invalid then m.focus = m.row
        end if
    end if
    raise()
    return true
end function
