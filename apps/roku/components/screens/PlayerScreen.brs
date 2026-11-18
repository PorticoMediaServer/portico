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
    m.ahead = m.top.findNode("ahead")
    m.was = m.top.findNode("was")
    m.hide = m.top.findNode("hide")
    m.report = m.top.findNode("report")
    m.tick = m.top.findNode("tick")
    m.settle = m.top.findNode("settle")
    m.again = m.top.findNode("again")
    setType(m.top.findNode("title"), "title", c.text)
    setType(m.top.findNode("code"), "meta", c.textSecondary)
    setType(m.top.findNode("sub"), "metaStrong", c.text)
    setType(m.top.findNode("clock"), "heading", c.text)
    setType(m.top.findNode("ends"), "meta", c.textSecondary)
    setType(m.top.findNode("at"), "metaStrong", c.text)
    setType(m.top.findNode("left"), "metaStrong", c.textSecondary)
    setType(m.top.findNode("flabText"), "nav", c.text)
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
    m.loadedAt = 0       ' where the stream now playing was started from
    m.notice.observeField("pressed", "onNotice")
    m.session = invalid
    m.options = invalid  ' the title's versions, tracks and qualities (playback-options)
    m.pending = -1       ' where a scrub will land, in seconds; -1 when none
    m.scrub = false      ' true while a scrub waits for OK (held, or moved while paused)
    m.duration = 0
    m.ended = false
    m.failed = false     ' the Playback failed page is up
    m.clock = createObject("roTimespan")
    m.playedMs = 0       ' time this session has spent playing, before the stretch running now
    m.clockRunning = false
    m.prompt = m.top.findNode("prompt")
    m.prompt.observeField("pressed", "onPrompt")
    m.dismissed = {}     ' segments skipped or put away, by marker id
    m.promptKey = ""
    m.upNext = m.top.findNode("upNext")
    m.unRow = m.top.findNode("unRow")
    m.unRow.observeField("pressed", "onPrompt")
    m.finished = m.top.findNode("finished")
    m.finRow = m.top.findNode("finRow")
    m.finRow.observeField("pressed", "onFinished")
    setType(m.top.findNode("unLabel"), "groupLabel", c.accentStrong)
    setType(m.top.findNode("unTitle"), "upNextTitle", c.text)
    setType(m.top.findNode("unSub"), "factLabel", c.textSecondary)
    setType(m.top.findNode("unWhen"), "tag", c.textTertiary)
    setType(m.top.findNode("unWhenTime"), "groupLabel", c.text)
    m.top.findNode("unTrack").color = c.barTrack
    m.top.findNode("unBar").color = c.white
    setType(m.top.findNode("finLabel"), "eyebrow", c.accentStrong)
    setType(m.top.findNode("finTitle"), "finishedTitle", c.text)
    m.top.findNode("finTitle").lineSpacing = -40     ' two lines set close, as the frame draws them, ending above the year line
    setType(m.top.findNode("finMeta"), "metaStrong", c.textSecondary)
    m.queue = invalid    ' this play's queue (source/Queue.brs), once the server has made it
    m.queueAsked = false
    m.next = invalid     ' the entry after this one, when there is one
    m.undo = m.top.findNode("undo")
    m.undoRow = m.top.findNode("undoRow")
    m.undoRow.observeField("pressed", "onUndo")
    m.top.findNode("undoRun").observeField("state", "onUndoRun")
    setType(m.top.findNode("undoText"), "bodyStrong", c.text)
    m.top.findNode("count").observeField("fire", "onCount")
    m.top.findNode("sleep").observeField("fire", "onSleep")
    m.sleepMinutes = 0
    m.prefs = invalid    ' the profile's playback settings, once read
    m.cardMode = ""      ' what the Up next card is showing, or "" when it is away
    m.live = false       ' a channel is playing (Live TV)
    m.liveWindow = false ' its stream is a playlist with a window to go back in
    m.counting = false   ' the card is counting down to the next title
    m.backSeconds = 10
    m.forwardSeconds = 30
    ' every fixed line of the overlay and of what sits over the picture comes from the player's frame
    f = frames().player
    m.f = f
    m.top.findNode("title").translation = [f.left, f.top]
    m.top.findNode("title").width = 1300
    m.top.findNode("code").translation = [f.left, f.top + 54]
    m.top.findNode("sub").translation = [f.left, f.top + 54]
    m.top.findNode("times").translation = [f.left, f.top + 90]
    m.top.findNode("times").width = 1300
    m.top.findNode("clock").translation = [f.right - 500, f.top]
    m.top.findNode("ends").translation = [f.right - 500, f.top + 44]
    m.top.findNode("track").width = f.right - f.left
    m.top.findNode("track").color = c.barTrack
    m.ahead.color = c.barAhead
    m.played.color = c.accentStrong
    m.was.color = c.barTick
    m.top.findNode("edge").color = c.danger
    m.top.findNode("liveDot").blendColor = c.danger
    m.top.findNode("at").translation = [f.left, f.timeY]
    m.top.findNode("left").translation = [f.right - 500, f.timeY]
    m.buttons.translation = [0, f.buttonsY]
    m.top.findNode("hints").translation = [f.left, f.buttonsY + 26]
    m.top.findNode("flabPlate").blendColor = c.labelPlate
    m.top.findNode("spinner").translation = [960 - 32, f.waitY]
    m.top.findNode("spinner").blendColor = c.focus
    m.top.findNode("waitTitle").translation = [260, f.waitY + 96]
    m.top.findNode("waitText").translation = [260, f.waitY + 142]
    ' Playback failed sits under the title row, at the player's left line
    m.notice.translation = [f.left - frames().gutter, f.noticeDrop]
    m.top.findNode("undoPlate").blendColor = c.promptPlate
    m.top.findNode("undoTick").blendColor = c.accentStrong
    m.top.findNode("undoTrack").color = c.glass
    m.top.findNode("undoLeft").color = c.accentStrong
    m.top.findNode("unPlate").blendColor = c.promptPlate
    m.top.findNode("unBack").color = c.raised
    m.top.findNode("unStill").blendColor = c.stillDim
    m.top.findNode("finBack").color = c.ink
    m.top.findNode("finLabel").translation = [f.left, f.finished.top]
    m.top.findNode("finTitle").translation = [f.left, f.finished.titleY]
    m.top.findNode("finMeta").translation = [f.left, f.finished.metaY]
    m.finRow.translation = [f.left, f.finished.buttonsY]
    m.finStars = m.top.findNode("finStars")
    m.finStars.translation = [f.left, f.finished.rateY]
    m.finStars.observeField("pressed", "onRate")
    m.finRelated = m.top.findNode("finRelated")
    m.finRelated.slotX = f.left
    m.finRelated.translation = [0, f.finished.rowY]
    m.finRelated.observeField("selectedIndex", "onFinishedRelated")
    makeButtons(false, false)
    makeHints()
end sub

' The button row, 88 px tall: the transport centred on the screen (Play/Pause in the middle), and
' Subtitles, Audio and Quality at the right end, each the label over what is chosen now.
' m.row holds them left to right; m.focus is an index into it, or -1 for the seek bar.
sub makeButtons(hasPrevious as boolean, hasNext as boolean)
    m.buttons.removeChildrenIndex(m.buttons.getChildCount(), 0)
    m.row = []
    ' the skip arrows carry the profile's own amounts
    specs = [{kind: "back", icon: "skip-back-44", size: 64, seconds: m.backSeconds.toStr()}, {kind: "play", icon: "pause-40", size: 88}, {kind: "forward", icon: "skip-forward-44", size: 64, seconds: m.forwardSeconds.toStr()}]
    ' a channel sent as its own stream has no window to move in: no skip arrows
    if m.live = true and m.liveWindow <> true then specs = [{kind: "play", icon: "pause-40", size: 88}]
    ' Previous and Next flank the transport when the queue has something that way
    if hasPrevious then specs.unshift({kind: "previous", icon: "previous-32", size: 64})
    if hasNext then specs.push({kind: "next", icon: "next-32", size: 64})
    ' live: channel down and up flank the transport, each with the number it goes to
    if m.live = true
        down = neighbour(-1)
        up = neighbour(1)
        if down <> invalid then specs.unshift({kind: "chdown", icon: "chevronDown-20", size: 64, channel: down.channel.number})
        if up <> invalid then specs.push({kind: "chup", icon: "chevronUp-20", size: 64, channel: up.channel.number})
    end if
    ' an episode played from its show has Episodes at the left end of the row (tv-player frame 1)
    if m.top.item <> invalid and m.top.item.kind = "episode" and m.queue <> invalid and m.queue.current <> invalid
        plate = m.buttons.createChild("Poster")
        plate.uri = "pkg:/images/round-14.9.png"
        plate.height = 64
        plate.translation = [m.f.left, 12]
        icon = m.buttons.createChild("Poster")
        icon.uri = "pkg:/images/i-collection-28.png"
        icon.width = 28
        icon.height = 28
        icon.translation = [m.f.left + 22, 30]
        label = m.buttons.createChild("Label")
        label.text = "Episodes"
        label.height = 64
        label.vertAlign = "center"
        label.translation = [m.f.left + 64, 12]
        setType(label, "button")
        plate.width = 64 + label.boundingRect().width + 26
        m.row.push({kind: "episodes", plate: plate, icon: icon, label: label, x: m.f.left, width: plate.width})
    end if
    m.firstTransport = m.row.count()
    ' Play/Pause stays in the middle of the screen whatever flanks it
    gap = 22
    x = 960 - 44
    for each spec in specs
        if spec.kind = "play" then exit for
        x = x - spec.size - gap
    end for
    for each spec in specs
        plate = m.buttons.createChild("Poster")
        plate.uri = "pkg:/images/disc.png"
        plate.width = spec.size
        plate.height = spec.size
        plate.translation = [x, (88 - spec.size) / 2]
        iconSize = 36
        if spec.kind = "play" then iconSize = 40
        if spec.kind = "previous" or spec.kind = "next" then iconSize = 32
        if spec.channel <> invalid then iconSize = 20
        icon = m.buttons.createChild("Poster")
        icon.uri = "pkg:/images/i-" + spec.icon + ".png"
        icon.width = iconSize
        icon.height = iconSize
        icon.translation = [x + (spec.size - iconSize) / 2, (88 - iconSize) / 2]
        if spec.kind = "chdown" then icon.translation = [x + 22, 50]
        if spec.kind = "chup" then icon.translation = [x + 22, 18]
        number = invalid
        if spec.channel <> invalid
            number = m.buttons.createChild("Label")
            number.text = spec.channel
            number.width = spec.size
            number.horizAlign = "center"
            number.translation = [x, 26]
            if spec.kind = "chup" then number.translation = [x, 36]
            setType(number, "keycap")
        else if spec.seconds <> invalid
            ' how far the arrow skips, drawn inside it, a little below the middle
            number = m.buttons.createChild("Label")
            number.text = spec.seconds
            number.width = spec.size
            number.height = spec.size
            number.horizAlign = "center"
            number.vertAlign = "center"
            number.translation = [x, (88 - spec.size) / 2 + 3]
            setType(number, "skipSeconds")
        end if
        if spec.kind = "play" then m.playIndex = m.row.count()
        m.row.push({kind: spec.kind, plate: plate, icon: icon, number: number, x: x, size: spec.size})
        x = x + spec.size + gap
    end for
    m.firstPill = m.row.count()
    ' live has no subtitle, audio or quality choices the server can change: none are drawn
    names = ["Subtitles", "Audio", "Quality"]
    if m.live = true then names = []
    for each name in names
        plate = m.buttons.createChild("Poster")
        plate.uri = "pkg:/images/round-14.9.png"
        plate.height = 64
        head = m.buttons.createChild("Label")
        head.text = name
        setType(head, "groupLabel")
        value = m.buttons.createChild("Label")
        setType(value, "tag")
        m.row.push({kind: "pill", name: name, plate: plate, head: head, value: value, x: 0, width: 0})
    end for
    m.pillValues = ""
    m.focus = m.playIndex
end sub

' Lays the three labelled buttons out from the right edge, each as wide as its words (a long
' value ends in an ellipsis; Quality's never does).
sub placePills(values as object)
    if m.live = true then return
    key = values.join("|")
    if key = m.pillValues then return
    m.pillValues = key
    right = m.f.right
    for i = 2 to 0 step -1
        pill = m.row[m.firstPill + i]
        pill.value.text = values[i]
        pill.value.width = 0
        inner = pill.head.boundingRect().width
        valueWidth = pill.value.boundingRect().width
        most = 164
        if i = 2 then most = 400
        if valueWidth > most then valueWidth = most
        if valueWidth > inner then inner = valueWidth
        pill.value.width = valueWidth
        width = inner + 36
        if width < 92 then width = 92
        x = right - width
        pill.plate.width = width
        pill.plate.translation = [x, 12]
        pill.head.translation = [x + 18, 18]
        pill.value.translation = [x + 18, 43]
        pill.x = x
        pill.width = width
        right = x - 14
    end for
end sub

' The key hints that stand in the button row while a scrub waits for OK (tv-player frame 2).
sub makeHints()
    c = colours()
    hints = m.top.findNode("hints")
    m.hints = []
    for each key in ["< >", "Hold", "OK", "Back"]
        cap = hints.createChild("Poster")
        cap.uri = "pkg:/images/line-10.9.png"
        cap.blendColor = c.lineStrong
        cap.height = 36
        capText = hints.createChild("Label")
        capText.text = key
        capText.height = 36
        capText.vertAlign = "center"
        setType(capText, "keycap", c.text)
        words = hints.createChild("Label")
        words.height = 36
        words.vertAlign = "center"
        setType(words, "meta", c.textSecondary)
        m.hints.push({cap: cap, capText: capText, words: words})
    end for
    m.hints[0].words.text = "Move"
    m.hints[1].words.text = "Faster"
end sub

sub placeHints(target as float, from as float)
    m.hints[2].words.text = "Play from " + clockText(target)
    m.hints[3].words.text = "Return to " + clockText(from)
    x = 0
    for each hint in m.hints
        w = hint.capText.boundingRect().width + 20
        hint.cap.width = w
        hint.cap.translation = [x, 0]
        hint.capText.translation = [x + 10, 0]
        hint.words.translation = [x + w + 12, 0]
        x = x + w + 12 + hint.words.boundingRect().width + 40
    end for
end sub

sub onActive()
    if not m.top.active then return
    m.top.setFocus(true)
    if m.finished.visible then m.finRow.active = true
    if m.session = invalid and not m.ended then start()
end sub

' The options panel goes when the title ends under it: what comes next takes the screen.
sub closeOptions()
    m.top.menu = {close: true}
end sub

' --- the session with the server -----------------------------------------------------------

sub start()
    if m.top.item.kind = "channel"
        startLive()
        return
    end if
    item = m.top.item
    title = item.title
    sub_ = ""
    if item.kind = "episode"
        if item.rootTitle <> invalid then title = item.rootTitle
        sub_ = item.title
    end if
    m.top.findNode("title").text = title
    ' the episode line: the code never shortens, the episode's name ends in an ellipsis
    code = m.top.findNode("code")
    code.text = episodeCode(item)
    name = m.top.findNode("sub")
    name.text = sub_
    if code.text <> ""
        x = m.f.left + code.boundingRect().width + 16
        name.translation = [x, m.f.top + 54]
        name.width = m.f.left + 1300 - x
    else
        name.width = 1300
    end if
    m.top.findNode("waitTitle").text = title
    busy("Getting ready…")
    if not m.queueAsked
        ' every play is a queue on the server, so what comes next is known
        m.queueAsked = true
        api("POST", "/v1/queues", queueBody(item), "onQueue")
        return
    end if
    ' a queued play names its entry; the server knows the title from it
    body = {itemId: item.id}
    if m.queue <> invalid and m.queue.current <> invalid then body = {queue: {queueId: m.queue.id, entryId: m.queue.current.entryId}}
    if m.top.startOver then body.startPositionMs = 0
    if m.resumeAt <> invalid then body.startPositionMs = m.resumeAt
    ' a title page's Play a different version names the file to play, this once
    if item.versionId <> invalid then body.versionId = item.versionId
    ' one start is one request however often it is asked again while the stream gets ready (202)
    if m.startKey = invalid then m.startKey = createObject("roDeviceInfo").getRandomUUID()
    api("POST", "/v1/playback/sessions", body, "onSession", invalid, false, m.startKey)
    if m.options = invalid then api("GET", "/v1/items/" + item.id + "/playback-options", invalid, "onOptions")
    if m.prefs = invalid then api("GET", "/v1/preferences?deviceClass=television", invalid, "onPrefs")
end sub

sub onOptions(event as object)
    result = event.getData()
    if result.ok then m.options = result.data
    paint()
end sub

' --- Subtitles, Audio, Quality --------------------------------------------------------------

function languageName(code as dynamic) as string
    if code = invalid or code = "" or code = "und" or code = "mul" then return ""
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

' A track's name (VIDEO.md VS-3e): the language, or the track's own title for a commentary or a
' track with no language, then SDH or forced in brackets. The format goes on the line under it.
function trackName(track as object, number as integer) as string
    text = languageName(track.language)
    if track.title <> invalid and track.title <> "" and (text = "" or track.commentary = true) then text = track.title
    if text = "" then text = "Track " + number.toStr()
    if track.sdh = true then text = text + " (SDH)"
    if track.forced = true then text = text + " (forced)"
    return text
end function

function audioDetail(track as object) as string
    text = formatName(track.codec)
    channels = ""
    if track.channels <> invalid
        if track.channels = 1 then channels = "mono" else if track.channels = 2 then channels = "stereo" else if track.channels = 6 then channels = "5.1" else if track.channels = 8 then channels = "7.1" else channels = track.channels.toStr() + " channels"
    end if
    if text <> "" and channels <> "" then return text + ", " + channels
    return text + channels
end function

' "4 Mbps", "1.5 Mbps", "500 kbps"
function bitrateText(kbps as integer) as string
    if kbps < 1000 then return kbps.toStr() + " kbps"
    tenths = int(kbps / 100 + 0.5)
    if tenths mod 10 = 0 then return int(tenths / 10).toStr() + " Mbps"
    return int(tenths / 10).toStr() + "." + (tenths mod 10).toStr() + " Mbps"
end function

function qualityLabel(q as object) as string
    if q.mode = "original" then return "Original"
    if q.mode = "automatic" then return "Automatic"
    ' the server's own name for a step ("720p-high" reads "720p high"): two steps can share a height
    if q.id <> invalid then return q.id.replace("-", " ")
    return ""
end function

' What each of the three says now: [subtitles, audio, quality].
function currentChoices() as object
    out = ["Off", "", ""]
    p = m.presentation
    version = currentVersion()
    if p = invalid or version = invalid then return out
    for i = 0 to version.subtitles.count() - 1
        if p.subtitle <> invalid and version.subtitles[i].id = p.subtitle.trackId then out[0] = trackName(version.subtitles[i], i + 1)
    end for
    for i = 0 to version.audio.count() - 1
        if p.audioTrackId <> invalid and version.audio[i].id = p.audioTrackId then out[1] = trackName(version.audio[i], i + 1)
    end for
    if p.quality <> invalid then out[2] = qualityLabel(p.quality)
    return out
end function

' The one options panel (tv-player frame 4): Subtitles, Audio and Quality as headings, the one
' asked for open in place with focus on its current choice. Changes last for this playback only.
sub openOptions(which as integer)
    version = currentVersion()
    p = m.presentation
    if version = invalid or p = invalid then return
    now = currentChoices()
    rows = [{id: "h:sub", text: "Subtitles", heading: true, value: now[0]}]
    rows.push({id: "sub:", text: "Off", under: "h:sub", current: p.subtitle = invalid})
    for i = 0 to version.subtitles.count() - 1
        track = version.subtitles[i]
        rows.push({id: "sub:" + track.id, text: trackName(track, i + 1), detail: formatName(track.format), under: "h:sub", current: p.subtitle <> invalid and p.subtitle.trackId = track.id})
    end for
    rows.push({id: "h:aud", text: "Audio", heading: true, value: now[1]})
    for i = 0 to version.audio.count() - 1
        track = version.audio[i]
        rows.push({id: "aud:" + track.id, text: trackName(track, i + 1), detail: audioDetail(track), under: "h:aud", current: p.audioTrackId <> invalid and p.audioTrackId = track.id})
    end for
    rows.push({id: "h:qua", text: "Quality", heading: true, value: now[2]})
    for i = 0 to m.options.qualities.count() - 1
        q = m.options.qualities[i]
        same = (p.quality <> invalid and p.quality.mode = q.mode and (q.mode <> "fixed" or p.quality.id = q.id))
        detail = ""
        if q.mode = "original" then detail = "Plays the file as is"
        ' a fixed quality caps the height and the bitrate: a file above either is converted (server-next delivery_plan)
        ' (one line that fits the panel: "Converted when above 720p or 4 Mbps")
        if q.mode = "fixed" and q.maxHeight <> invalid
            detail = "Converted when above " + q.maxHeight.toStr() + "p"
            if q.maxVideoBitrateKbps <> invalid then detail = detail + " or " + bitrateText(q.maxVideoBitrateKbps)
        end if
        rows.push({id: "qua:" + i.toStr(), text: qualityLabel(q), detail: detail, under: "h:qua", current: same})
    end for
    ' then the sleep timer, the title's chapters and Playback info (tv-player frame 4), as the other apps offer them
    sleep = "Off"
    if m.sleepMinutes > 0 then sleep = m.sleepMinutes.toStr() + " minutes"
    rows.push({id: "h:slp", text: "Sleep timer", heading: true, value: sleep})
    for each minutes in [0, 15, 30, 45, 60, 90]
        text = "Off"
        if minutes > 0 then text = minutes.toStr() + " minutes"
        rows.push({id: "slp:" + minutes.toStr(), text: text, under: "h:slp", current: minutes = m.sleepMinutes})
    end for
    chapters = m.options.chapters
    if chapters <> invalid and chapters.count() > 0
        here = chapterAt(m.video.position * 1000)
        rows.push({id: "h:chp", text: "Chapters", heading: true, value: "Chapter " + (here + 1).toStr()})
        for i = 0 to chapters.count() - 1
            text = (i + 1).toStr() + "   " + chapters[i].title
            if chapters[i].title = invalid or chapters[i].title = "" then text = "Chapter " + (i + 1).toStr()
            rows.push({id: "chp:" + i.toStr(), text: text, detail: clockText(chapters[i].startMs / 1000), under: "h:chp", current: i = here})
        end for
    end if
    rows.push({id: "info", text: "Playback info", gapBefore: true})
    m.hide.control = "stop"
    m.top.menu = {title: "Playback options", status: "Changes here last until you stop watching. Your defaults stay as they are in Settings.", open: ["h:sub", "h:aud", "h:qua"][which], rows: rows}
end sub

' The chapter a place falls in: the last one starting at or before it.
function chapterAt(ms as float) as integer
    at = 0
    for i = 0 to m.options.chapters.count() - 1
        if m.options.chapters[i].startMs <= ms then at = i
    end for
    return at
end function

' The sleep timer: chosen here, for this playback only; when it runs out the title pauses.
sub setSleep(minutes as integer)
    m.sleepMinutes = minutes
    timer = m.top.findNode("sleep")
    timer.control = "stop"
    if minutes = 0 then return
    timer.duration = minutes * 60
    timer.control = "start"
end sub

sub onSleep()
    m.sleepMinutes = 0
    m.video.control = "pause"
    raise()
end sub

' Playback info (playback-info.html, the same words as the other apps): what plays and why in the
' panel's words, then a row for each fact. OK or Back closes it.
sub openInfo()
    p = m.presentation
    version = currentVersion()
    if p = invalid or version = invalid then return
    source = {container: version.container}
    if version.video <> invalid and version.video.count() > 0
        source.videoCodec = version.video[0].codec
        source.width = version.video[0].width
        source.height = version.video[0].height
    end if
    for each track in version.audio
        if track.id = p.audioTrackId then source.audioCodec = track.codec
    end for
    facts = {decision: p.decision, source: source, hdr: p.hdr}
    if p.quality <> invalid and p.quality.mode = "fixed" then facts.chosenQuality = qualityLabel(p.quality)
    if p.bitrateKbps <> invalid and p.bitrateKbps > 0 then facts.bitrate = bitrateText(p.bitrateKbps)
    status = ""
    body = ""
    rows = []
    for each row in playbackInfoRows(facts)
        if row.id = "delivery"
            status = row.value
        else if row.id = "why"
            body = row.value
        else
            rows.push({id: "inf:" + row.id, text: row.label, detail: row.value})
        end if
    end for
    m.hide.control = "stop"
    m.top.menu = {title: "Playback info", status: status, body: body, rows: rows}
end sub

' Episodes: the season playing, read from the queue (an episode's queue is its show), the one
' playing marked; OK on another plays it.
sub openEpisodes()
    if m.queue = invalid then return
    m.hide.control = "stop"
    api("GET", "/v1/queues/" + m.queue.id + "/entries?around=current&before=100&after=100", invalid, "onEpisodes", {entryId: m.queue.current.entryId})
end sub

sub onEpisodes(event as object)
    result = event.getData()
    if not result.ok or m.queue = invalid or m.queue.current = invalid then return
    if event.getRoSGNode().ctx.entryId <> m.queue.current.entryId then return
    playing = queueEntry(result.data.items, m.queue.current.entryId)
    if playing = invalid then return
    rows = []
    for each entry in result.data.items
        if entry.available = true and entry.parentIndex = playing.parentIndex
            row = {id: "epi:" + entry.entryId, text: entry.title, current: entry.entryId = playing.entryId}
            if entry.index <> invalid then row.text = entry.index.toStr() + "   " + entry.title
            if entry.durationMs <> invalid then row.detail = durationText(entry.durationMs)
            rows.push(row)
        end if
    end for
    model = {title: "Episodes", rows: rows}
    if playing.parentIndex <> invalid then model.title = "Season " + playing.parentIndex.toStr()
    if playing.rootTitle <> invalid then model.eyebrow = playing.rootTitle
    m.top.menu = model
end sub

sub onMenuChosen(event as object)
    if m.session = invalid then return
    id = event.getData()
    kind = left(id, 4)
    value = mid(id, 5)
    if kind = "slp:"
        setSleep(value.toInt())
        return
    else if kind = "chp:"
        m.pending = m.options.chapters[value.toInt()].startMs / 1000
        applySeek()
        return
    else if id = "info"
        openInfo()
        return
    else if kind = "inf:"
        return
    end if
    if kind = "epi:"
        if m.queue <> invalid and m.queue.current <> invalid and value <> m.queue.current.entryId then moveQueue("entry", value)
        return
    end if
    change = {}
    if kind = "sub:"
        if value = "" then change.subtitles = {trackId: invalid} else change.subtitles = {trackId: value}
    else if kind = "aud:"
        change.audio = {trackId: value}
    else if kind = "qua:"
        q = m.options.qualities[value.toInt()]
        change.quality = {mode: q.mode}
        if q.id <> invalid then change.quality.id = q.id
    else
        return
    end if
    change.positionMs = int(m.video.position * 1000)
    api("PATCH", "/v1/playback/sessions/" + m.session.id, change, "onChanged", {at: m.video.position, subtitles: kind = "sub:"})
end sub

' The server answers a change with how the title reaches this TV now: a new stream is loaded where
' the picture was; on the same stream the subtitle or the audio track is switched in place.
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
        if event.getRoSGNode().ctx.subtitles = true then drawSubtitle(p, true)
        if p.audioTrackId <> before.audioTrackId then switchAudio(p.audioTrackId)
    end if
    raise()
end sub

' Roku lists a file's audio tracks in the file's order, as the server does.
sub switchAudio(trackId as dynamic)
    version = currentVersion()
    tracks = m.video.availableAudioTracks
    if version = invalid or tracks = invalid then return
    for i = 0 to version.audio.count() - 1
        if version.audio[i].id = trackId and i < tracks.count() then m.video.audioTrack = tracks[i].Track
    end for
end sub

' The WebVTT copy of the chosen text subtitle (the server also lists an ASS track's original file,
' which Roku cannot draw). One burned into the picture needs nothing here.
function subtitleUrl(p as object) as string
    if p.subtitle = invalid or p.subtitles = invalid then return ""
    for each file in p.subtitles
        if file.trackId = p.subtitle.trackId and file.format = "vtt" then return m.global.origin + file.url
    end for
    return ""
end function

' Roku draws the chosen text subtitle. Its caption setting is the viewer's for the whole TV, so it
' is turned off only when they choose Off here, never just because a title starts without one.
sub drawSubtitle(p as object, chosenHere as boolean)
    url = subtitleUrl(p)
    if url <> ""
        m.video.subtitleTrack = url
        m.video.globalCaptionMode = "On"
    else if chosenHere
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
    m.playedMs = 0
    m.clock.mark()
    load(m.session.presentation)
    if m.session.lease <> invalid and m.session.lease.reportEveryMs <> invalid then m.report.duration = m.session.lease.reportEveryMs / 1000
    m.report.control = "start"
    m.tick.control = "start"
end sub

function streamFormat(p as object) as string
    ' a playlist is HLS; a channel the TV can take as it is comes as its own transport stream
    if instr(1, p.url, ".m3u8") > 0 then return "hls"
    if p.mode = "live" then return "ts"
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
' picture was, a few times, before it is called a failure. The session is read first: the server
' may already have moved this title to another stream, which is then played, and nothing is blamed.
sub again()
    if m.session = invalid or m.ended then return
    if m.tries >= 6
        fail("This title could not be played on this TV.")
        return
    end if
    m.tries = m.tries + 1
    m.video.control = "stop"
    busy("Getting ready…")
    api("GET", "/v1/playback/sessions/" + m.session.id, invalid, "onRecheck", {id: m.session.id, failed: true})
end sub

' The session as the server has it now. A newer stream than the one loaded is played from where
' the picture was. Otherwise, after a failure, the server is told the player could not play this;
' it may answer with another way for the title to reach this TV.
sub onRecheck(event as object)
    result = event.getData()
    ctx = event.getRoSGNode().ctx
    if m.session = invalid or m.ended or ctx.id <> m.session.id then return
    if result.ok and result.data <> invalid and result.data.presentation <> invalid and result.data.presentation.generation <> m.presentation.generation
        m.session = result.data
        p = m.session.presentation
        p.startPositionMs = int(m.last * 1000)
        load(p)
        return
    end if
    if ctx.failed <> true then return
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
    m.loadedAt = m.last
    ' every text subtitle the server offers beside this stream, as WebVTT
    tracks = []
    if p.subtitles <> invalid
        for each file in p.subtitles
            if file.format = "vtt" then tracks.push({TrackName: m.global.origin + file.url, Language: "", Description: file.trackId})
        end for
    end if
    content.subtitleTracks = tracks
    m.presentation = p
    if m.live = true and m.liveWindow <> (streamFormat(p) = "hls")
        m.liveWindow = (streamFormat(p) = "hls")
        makeButtons(false, false)
    end if
    m.loading = true        ' until the picture runs, a wait is "Getting ready", not "Catching up"
    m.video.content = content
    m.video.control = "play"
    drawSubtitle(p, false)
end sub

' Time spent actually playing (not paused, buffering or jumping), which the server counts as
' viewing time and needs before a play shows in history.
sub clockState()
    playing = (m.video.state = "playing")
    if playing and not m.clockRunning
        m.clock.mark()
    else if not playing and m.clockRunning
        m.playedMs = m.playedMs + m.clock.totalMilliseconds()
    end if
    m.clockRunning = playing
end sub

function playClock() as integer
    total = m.playedMs
    if m.clockRunning then total = total + m.clock.totalMilliseconds()
    return total
end function

' The stop carries the final place and the viewing time with the session's progress token, so the
' server saves both in one step, however the last progress report fared.
function finalReport(positionMs as integer) as object
    body = {positionMs: positionMs}
    if m.session.progressToken <> invalid
        body.progressToken = m.session.progressToken
        body.playClockMs = playClock()
    end if
    return body
end function

sub sendProgress()
    if m.session = invalid then return
    state = "playing"
    if m.video.state = "paused" then state = "paused"
    if m.video.state = "buffering" then state = "buffering"
    at = m.video.position
    if at = 0 and m.last > 0 then at = m.last      ' a stopped picture reports 0
    api("PUT", "/v1/playback/sessions/" + m.session.id + "/progress", {state: state, positionMs: int(at * 1000), playClockMs: playClock()}, "onProgress", {id: m.session.id})
end sub

sub onProgress(event as object)
    result = event.getData()
    if m.session = invalid or event.getRoSGNode().ctx.id <> m.session.id then return
    if result.ok and result.data <> invalid and result.data.generation <> invalid and result.data.generation <> m.presentation.generation
        ' the server moved this title to another stream by itself: read the session and play that
        api("GET", "/v1/playback/sessions/" + m.session.id, invalid, "onRecheck", {id: m.session.id, failed: false})
    else if result.status = 410
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
    for each timer in [m.report, m.tick, m.hide, m.settle, m.again, m.retry, m.top.findNode("count")]
        timer.control = "stop"
    end for
    busy(invalid)
    if tell then stopSession()
    m.video.control = "stop"
    m.session = invalid
    m.top.closed = true
end sub

'' Ends the session with where the picture got to and the viewing time.
sub stopSession()
    m.report.control = "stop"
    m.tick.control = "stop"
    if m.session = invalid then return
    at = int(m.video.position * 1000)
    if at = 0 and m.last > 0 then at = int(m.last * 1000)
    m.video.control = "stop"
    api("DELETE", "/v1/playback/sessions/" + m.session.id, finalReport(at), "onStopped")
    m.session = invalid
end sub

sub onStopped()
end sub

sub onVideoState()
    clockState()
    state = m.video.state
    if state = "finished"
        ' the end of the title, or a stream that stopped short of it
        if m.live <> true and (m.duration <= 0 or m.last >= m.duration - 5) then ended() else again()
    else if state = "error"
        again()
    else if state = "playing"
        m.loading = false
        if m.video.duration > 0 then m.duration = m.video.duration
        busy(invalid)
        if m.controls.visible
            m.hide.control = "stop"
            m.hide.control = "start"
        end if
        paint()
    else if state = "buffering"
        if m.loading = true then busy("Getting ready…") else busy("Catching up…")
        paint()
    else
        paint()
    end if
end sub

' Playback failed (states frame 11): the title and where it stopped stay on screen, focus starts on
' Try again, which picks up at the saved place; Back leaves the player.
sub fail(message as string)
    busy(invalid)
    for each timer in [m.report, m.tick, m.hide, m.retry, m.again, m.settle]
        timer.control = "stop"
    end for
    m.video.control = "stop"
    m.failed = true
    cancelScrub()
    m.controls.visible = true
    paint()
    if m.last > 0 then message = message + " Your place is saved at " + clockText(m.last) + ", so Try again picks up right there."
    m.notice.icon = "warning"
    m.notice.title = "Playback failed"
    m.notice.body = message
    m.notice.buttons = [{id: "retry", text: "Try again", icon: "refresh"}]
    m.notice.visible = true
    m.notice.active = true
end sub

sub onNotice(event as object)
    if event.getData() <> "retry" then return
    m.notice.visible = false
    m.failed = false
    m.controls.visible = false
    m.tries = 0
    if m.session <> invalid
        ' the failed session is ended with where it got to; a new one starts there
        api("DELETE", "/v1/playback/sessions/" + m.session.id, finalReport(int(m.last * 1000)), "onStopped")
        m.session = invalid
    end if
    if m.last > 0 then m.resumeAt = int(m.last * 1000)
    m.top.setFocus(true)
    start()
end sub

' --- Skip intro, recap and credits -----------------------------------------------------------

' The segment playing now that the viewer has not put away (the server's markers, from
' playback-options), or invalid. It stops half a second before its end: nothing is left to skip.
function activeMarker(at as float) as dynamic
    if m.options = invalid or m.options.markers = invalid then return invalid
    for each marker in m.options.markers
        if at >= marker.startMs / 1000 and at < marker.endMs / 1000 - 0.5 and m.dismissed[marker.id] <> true and skipMode(marker.type) <> "off" then return marker
    end for
    return invalid
end function

' Shows, changes or puts away the prompt for where the picture is.
sub updatePrompt()
    if m.live = true then return
    if m.failed or m.ended or m.session = invalid then return
    at = m.video.position
    if m.cardMode = "end" or m.cardMode = "count" or m.cardMode = "still" then return
    marker = activeMarker(at)
    if marker <> invalid and skipMode(marker.type) = "auto" and marker.automaticSafe = true
        autoSkip(marker)
        return
    end if
    key = ""
    if marker <> invalid then key = marker.id
    ' Up next: during closing credits, or an episode's last 30 seconds when it has none marked
    closing = marker <> invalid and (marker.type = "credits" or marker.type = "outro") and marker.startMs / 1000 >= m.duration / 2
    tail = m.top.item.kind = "episode" and m.duration >= 600 and at >= m.duration - 30 and at < m.duration - 1
    if m.next <> invalid and (closing or (tail and marker = invalid)) and m.dismissed["next"] <> true and not passoutCheckDue() then key = "next"
    if key = "next" and m.cardMode = "offer" and not m.counting then offerWhen(m.duration - at)
    if key = m.promptKey then return
    m.promptKey = key
    if key = "next"
        if not closing then marker = invalid
        showUpNext(marker, "offer")
        offerWhen(m.duration - at)
        ' "Start the next episode during the credits": only credits the server is sure of, and
        ' never when a scene follows them
        if marker <> invalid and pref("playback.autoplayNext") = true and pref("playback.startNextDuringCredits") = true and marker.automaticSafe = true and marker.endMs / 1000 >= m.duration - 3
            countDown(int(pref("playback.upNextCountdownMs") / 1000))
        end if
        focusPrompt()
        return
    end if
    stopCard()
    if marker = invalid
        m.prompt.visible = false
        if not m.top.hasFocus() and not m.controls.visible then m.top.setFocus(true)
        return
    end if
    words = {intro: "Skip intro", recap: "Skip recap", credits: "Skip credits", outro: "Skip outro", commercial: "Skip ad"}
    text = words[marker.type]
    if text = invalid then text = "Skip"
    m.prompt.buttons = [{id: "skip", text: text, icon: ""}]
    placePrompt()
    m.prompt.visible = true
    focusPrompt()
end sub

' Right-aligned at the frame's prompt place.
sub placePrompt()
    f = frames().player
    width = m.prompt.boundingRect().width
    m.prompt.translation = [m.f.right - width, m.f.promptBottom - 72]
end sub

' The prompt takes focus by itself only while no control has it (the overlay is down).
sub focusPrompt()
    if m.controls.visible or m.failed or not m.top.isInFocusChain() then return
    if m.undo.visible
        m.undoRow.active = true
    else if m.upNext.visible
        m.unRow.active = true
    else if m.prompt.visible
        m.prompt.active = true
    end if
end sub

' The card's last line while the credits run: when the next title will start by itself, if it will.
sub offerWhen(left as float)
    if pref("playback.autoplayNext") <> true
        setWhen("", "")
        return
    end if
    setWhen("Starts in", clockText(left + pref("playback.upNextCountdownMs") / 1000))
end sub

' The card's last line: its words in grey and the time after them in white.
sub setWhen(words as string, time as string)
    when = m.top.findNode("unWhen")
    if when.text = words and m.top.findNode("unWhenTime").text = time and not m.counting then return
    when.text = words
    m.top.findNode("unWhenTime").text = time
    layoutCard()
end sub

sub onPrompt(event as object)
    which = event.getData()
    if which = "next" or which = "still"
        advance("next")
        return
    else if which = "credits"
        ' Watch credits: the card goes away for this title
        m.dismissed["next"] = true
        stopCard()
        m.promptKey = ""
        m.top.setFocus(true)
        return
    end if
    marker = activeMarker(m.video.position)
    if marker = invalid then return
    m.top.setFocus(true)
    skipTo(marker)
    lower()
end sub

' --- the profile's playback settings --------------------------------------------------------

' What the server's settings are until the profile's own arrive (GET /v1/preferences, the same
' values the other apps and the Settings screen read).
function playbackDefaults() as object
    return {"playback.autoplayNext": true, "playback.upNextCountdownMs": 10000, "playback.startNextDuringCredits": false, "playback.introSkip": "ask", "playback.creditsSkip": "ask", "playback.recapSkip": "ask", "playback.skipBackMs": 10000, "playback.skipForwardMs": 30000}
end function

function pref(key as string) as dynamic
    if m.prefs <> invalid and m.prefs[key] <> invalid then return m.prefs[key]
    return playbackDefaults()[key]
end function

sub onPrefs(event as object)
    result = event.getData()
    if result.ok and result.data.effective <> invalid then m.prefs = result.data.effective
    m.backSeconds = int(pref("playback.skipBackMs") / 1000)
    m.forwardSeconds = int(pref("playback.skipForwardMs") / 1000)
    followQueue()
end sub

' Ask each time, skip automatically, or never, for a kind of segment.
function skipMode(kind as string) as string
    if kind = "intro" then return pref("playback.introSkip")
    if kind = "recap" then return pref("playback.recapSkip")
    if kind = "credits" or kind = "outro" then return pref("playback.creditsSkip")
    return "ask"
end function

' "Still watching?" is the server's decision, sent with the queue (postPlay.passoutCheckDue), as
' the other apps read it; the player keeps no rule of its own for it.
function passoutCheckDue() as boolean
    return m.queue <> invalid and m.queue.postPlay <> invalid and m.queue.postPlay.passoutCheckDue = true
end function

' --- an automatic skip, and its Undo -------------------------------------------------------

sub skipTo(marker as object)
    m.dismissed[marker.id] = true
    ' an intro or recap lands a second before it ends; closing credits land exactly where they end
    target = marker.endMs / 1000
    if marker.type <> "credits" and marker.type <> "outro" then target = target - 1
    if target < marker.startMs / 1000 then target = marker.startMs / 1000
    m.prompt.visible = false
    m.promptKey = ""
    m.pending = target
    applySeek()
end sub

' Skipped by itself (the viewer chose Skip automatically and the server is sure of the segment):
' says so for six seconds with Undo, which goes back to where the segment began.
sub autoSkip(marker as object)
    skipTo(marker)
    m.controls.visible = false
    m.undoMarker = marker
    words = {intro: "Skipped intro", recap: "Skipped recap", credits: "Skipped credits", outro: "Skipped outro"}
    label = m.top.findNode("undoText")
    label.text = words[marker.type]
    if label.text = "" then label.text = "Skipped"
    width = label.boundingRect().width
    m.top.findNode("undoTrack").width = width
    m.undoRow.buttons = [{id: "undo", text: "Undo", icon: "restart"}]
    m.undoRow.translation = [70 + width + 28, 16]
    plateWidth = 70 + width + 28 + m.undoRow.boundingRect().width + 16
    m.top.findNode("undoPlate").width = plateWidth
    m.top.findNode("undoShrink").keyValue = [width * 1.0, 0.0]
    m.undo.translation = [m.f.right - plateWidth, m.f.promptBottom - 104]
    m.undo.visible = true
    m.top.findNode("undoRun").control = "start"
    focusPrompt()
end sub

sub onUndoRun(event as object)
    if event.getData() <> "stopped" then return
    m.undo.visible = false
    if m.undoRow.isInFocusChain() then m.top.setFocus(true)
end sub

sub onUndo()
    m.top.findNode("undoRun").control = "stop"
    m.undo.visible = false
    m.top.setFocus(true)
    if m.undoMarker = invalid then return
    m.pending = m.undoMarker.startMs / 1000
    m.undoMarker = invalid
    applySeek()
    lower()
end sub

' --- the wait before the next title --------------------------------------------------------

sub onCount()
    m.countLeft = m.countLeft - 1
    if m.countLeft <= 0
        m.top.findNode("count").control = "stop"
        m.counting = false
        advance("completion")
        return
    end if
    setWhen("Playing in", m.countLeft.toStr() + "s")
end sub

' Puts the Up next card away, and any count it was running.
sub stopCard()
    m.top.findNode("count").control = "stop"
    m.counting = false
    m.cardMode = ""
    m.upNext.visible = false
end sub

sub countDown(seconds as integer)
    m.countLeft = seconds
    m.countFrom = seconds
    count = m.top.findNode("count")
    count.control = "stop"
    count.control = "start"
    m.counting = true
    setWhen("Playing in", seconds.toStr() + "s")
end sub

' --- Live TV: the normal player with a channel in it ----------------------------------------

' A channel from the guide: the programme on now, the channel's name and number with Live, and
' the times with what follows. There is no queue, no versions and no tracks to choose on live.
sub startLive()
    c = colours()
    item = m.top.item
    m.live = true
    m.queueAsked = true
    now = createObject("roDateTime").asSeconds()
    m.liveProgramme = invalid
    following = invalid
    if item.programmes <> invalid
        for each p in item.programmes
            if m.liveProgramme <> invalid and following = invalid then following = p
            if isoSeconds(p.startsAt) <= now and now < isoSeconds(p.endsAt) then m.liveProgramme = p
        end for
    end if
    title = item.channel.name
    times = ""
    if m.liveProgramme <> invalid
        title = m.liveProgramme.title
        times = timeOfDay(isoSeconds(m.liveProgramme.startsAt) - now) + " to " + timeOfDay(isoSeconds(m.liveProgramme.endsAt) - now)
        if following <> invalid then times = times + "   Then " + following.title
    end if
    m.top.findNode("title").text = title
    code = m.top.findNode("code")
    code.text = item.channel.name + "   " + item.channel.number
    setType(code, "metaStrong", c.text)
    live = m.top.findNode("sub")
    live.text = "Live"
    live.color = c.danger
    live.translation = [m.f.left + code.boundingRect().width + 16, m.f.top + 54]
    setType(m.top.findNode("times"), "meta", c.textSecondary)
    m.top.findNode("times").text = times
    m.top.findNode("waitTitle").text = item.channel.name
    setType(m.top.findNode("goLiveText"), "nav", c.text)
    m.top.findNode("goLivePlate").width = m.top.findNode("goLiveText").boundingRect().width + 60
    m.top.findNode("goLive").translation = [m.f.right - m.top.findNode("goLivePlate").width, m.f.timeY - 10]
    makeButtons(false, false)
    busy("Getting ready…")
    if m.startKey = invalid then m.startKey = createObject("roDeviceInfo").getRandomUUID()
    api("POST", "/v1/playback/sessions", {channel: item.channel.id}, "onSession", invalid, false, m.startKey)
end sub

function isoSeconds(iso as string) as integer
    t = createObject("roDateTime")
    t.fromISO8601String(iso)
    return t.asSeconds()
end function

' The channel beside this one in the guide's lineup: +1 up (the higher number), -1 down.
function neighbour(stepBy as integer) as dynamic
    lineup = m.top.item.lineup
    if lineup = invalid or lineup.count() < 2 then return invalid
    for i = 0 to lineup.count() - 1
        if lineup[i].channel.id = m.top.item.channel.id then return lineup[(i + stepBy + lineup.count()) mod lineup.count()]
    end for
    return invalid
end function

sub changeChannel(stepBy as integer)
    row = neighbour(stepBy)
    if row = invalid then return
    stopSession()
    lineup = m.top.item.lineup
    m.top.item = {kind: "channel", id: row.channel.id, title: row.channel.name, channel: row.channel, programmes: row.programmes, lineup: lineup}
    fresh()
    m.focus = m.playIndex
    startLive()
end sub

' How far behind live the picture is: Roku counts a live stream's duration up to its live edge.
function liveBehind() as float
    behind = 0
    if m.video.duration > 0 then behind = m.video.duration - m.video.position
    if behind < 0 then behind = 0
    return behind
end function

' Go to live: the stream is started again at its live edge.
sub goLive()
    if m.presentation = invalid then return
    p = m.presentation
    p.startPositionMs = 0
    load(p)
    m.focus = m.playIndex
    raise()
end sub

' The live bar spans the programme: played up to where the picture is, a red mark at live, and the
' time row says the picture's time and how far behind live it is.
sub paintLive()
    if m.liveProgramme = invalid then return
    c = colours()
    now = createObject("roDateTime").asSeconds()
    s = isoSeconds(m.liveProgramme.startsAt)
    e = isoSeconds(m.liveProgramme.endsAt)
    behind = liveBehind()
    at = now - behind
    x = barX(at - s, e - s)
    m.played.width = x - m.f.left
    m.knob.translation = [x - m.knob.width / 2, m.f.barY + 4 - m.knob.height / 2]
    m.knobRing.visible = false
    edge = m.top.findNode("edge")
    edge.translation = [barX(now - s, e - s) - 2, m.f.barY - 7]
    edge.visible = true
    text = timeOfDay(-int(behind))
    if behind >= 60 then text = text + "   " + int(behind / 60).toStr() + " min behind live"
    m.top.findNode("at").text = text
    m.top.findNode("left").text = ""
    m.top.findNode("ends").text = "Ends " + timeOfDay(e - now)
    goLiveNode = m.top.findNode("goLive")
    goLiveNode.visible = behind > 30
    if not goLiveNode.visible and m.focus < 0 then m.focus = m.playIndex
    on = (m.focus < 0)
    if on then m.top.findNode("goLivePlate").blendColor = c.glassOn else m.top.findNode("goLivePlate").blendColor = c.glass
    pillRing = m.top.findNode("pillRing")
    if on
        out = metrics().focusSpread + 6
        node = m.top.findNode("goLivePlate")
        pillRing.translation = [goLiveNode.translation[0] - out, m.f.timeY - 10 - out]
        pillRing.width = node.width + out * 2
        pillRing.height = 48 + out * 2
        pillRing.visible = true
    end if
end sub

' --- the queue: Previous, Next, Up next and the end of a title ------------------------------

sub onQueue(event as object)
    result = event.getData()
    if m.ended then return
    if result.ok
        m.queue = result.data.queue
        m.window = result.data.window
        followQueue()
    end if
    start()     ' a play the server could not queue still plays; nothing comes after it
end sub

' What comes after (and before) the title now playing, and the transport that goes there.
sub followQueue()
    m.next = invalid
    hasPrevious = false
    if m.queue <> invalid and m.queue.current <> invalid
        m.next = queueEntry(m.window, m.queue.nextEntryId)
        hasPrevious = m.queue.current.position > 0
    end if
    makeButtons(hasPrevious, m.next <> invalid)
    m.pillValues = ""
end sub

' Next, Previous, or "this title ended": the queue moves (naming the entry it moves from, so a
' request sent twice moves once), and the title it lands on starts.
sub advance(reason as string)
    moveQueue(reason, invalid)
end sub

sub moveQueue(reason as string, entryId as dynamic)
    if m.queue = invalid or m.queue.current = invalid then return
    body = {reason: reason, fromEntryId: m.queue.current.entryId}
    if entryId <> invalid then body.entryId = entryId
    if reason = "completion" and m.session <> invalid then body.sessionId = m.session.id
    stopSession()
    hideAll()
    busy("Getting ready…")
    api("POST", "/v1/queues/" + m.queue.id + "/advance", body, "onAdvanced")
end sub

sub onAdvanced(event as object)
    result = event.getData()
    if m.ended then return
    if not result.ok or result.data.current = invalid
        leave(false)
        return
    end if
    m.queue = result.data
    api("GET", "/v1/queues/" + m.queue.id + "/entries?around=current&before=1&after=1", invalid, "onWindow")
end sub

sub onWindow(event as object)
    result = event.getData()
    if m.ended then return
    entry = invalid
    if result.ok then entry = queueEntry(result.data.items, m.queue.current.entryId)
    if entry = invalid
        leave(false)
        return
    end if
    m.window = result.data.items
    m.top.item = queueItem(entry)
    m.top.startOver = false
    fresh()
    followQueue()
    start()
end sub

' Everything that belonged to the title before.
sub fresh()
    m.options = invalid
    m.presentation = invalid
    m.resumeAt = invalid
    m.duration = 0
    m.last = 0
    m.loadedAt = 0
    m.tries = 0
    m.dismissed = {}
    m.promptKey = ""
    m.startKey = invalid
end sub

sub hideAll()
    closeOptions()
    m.top.findNode("count").control = "stop"
    m.counting = false
    m.cardMode = ""
    m.undo.visible = false
    m.controls.visible = false
    m.prompt.visible = false
    m.upNext.visible = false
    m.promptKey = ""
    m.top.setFocus(true)
end sub

' The Up next card for the entry after this one, with Play now first.
' mode: "offer" during the credits; at the end, "count" (playing after the chosen wait), "end"
' (auto-play off: waits for Play now) or "still" (Still watching?, after the chosen number of episodes).
sub showUpNext(marker as dynamic, mode as string)
    m.cardMode = mode
    entry = m.next
    labels = {offer: "Up next", count: "Up next", end: "Up next", still: "Still watching?"}
    m.top.findNode("unLabel").text = labels[mode]
    whens = {offer: "", count: "", end: "Ready when you are", still: "Playback paused after several episodes. Confirm to keep going."}
    m.top.findNode("unWhen").text = whens[mode]
    m.top.findNode("unWhenTime").text = ""
    still = pick(entry.images, ["thumb", "still", "backdrop"])
    m.top.findNode("unStill").uri = imageUrl(still, 256, 144)
    title = m.top.findNode("unTitle")
    title.text = entry.title
    sub_ = episodeCode(entry)
    if entry.durationMs <> invalid then sub_ = (sub_ + "   " + durationText(entry.durationMs)).trim()
    m.top.findNode("unSub").text = sub_
    buttons = [{id: "next", text: "Play now", icon: "play"}]
    if mode = "still" then buttons = [{id: "still", text: "I’m still watching", icon: "play"}]
    if marker <> invalid and marker.endMs / 1000 < m.duration - 3 then buttons.push({id: "skip", text: "Skip credits", icon: ""})
    if mode = "offer" then buttons.push({id: "credits", text: "Watch credits", icon: ""})
    m.unRow.buttons = buttons
    layoutCard()
    m.upNext.visible = true
    m.prompt.visible = false
end sub

' The frame's card: 26 in from its edges, the words 24 after the 256 wide still and set on its
' middle, then the count's line (18 below, while it counts) and the buttons 24 below. A title wraps
' rather than being cut: the lines under it, and the card, grow to fit.
sub layoutCard()
    label = m.top.findNode("unLabel")
    title = m.top.findNode("unTitle")
    sub_ = m.top.findNode("unSub")
    when = m.top.findNode("unWhen")
    whenTime = m.top.findNode("unWhenTime")
    titleHeight = title.boundingRect().height
    whenHeight = 0
    if when.text <> "" or whenTime.text <> "" then whenHeight = 10 + when.boundingRect().height
    if when.text = "" and whenTime.text <> "" then whenHeight = 10 + whenTime.boundingRect().height
    words = 24 + 6 + titleHeight + 4 + 27 + whenHeight
    top = 26
    if words < 144 then top = 26 + (144 - words) / 2
    label.translation = [306, top]
    title.translation = [306, top + 30]
    y = top + 30 + titleHeight + 4
    sub_.translation = [306, y]
    y = y + 27 + 10
    when.translation = [306, y]
    whenTime.translation = [306 + when.boundingRect().width + 6, y]
    bottom = top + words
    if bottom < 26 + 144 then bottom = 26 + 144
    track = m.top.findNode("unTrack")
    bar = m.top.findNode("unBar")
    track.visible = m.counting
    bar.visible = m.counting
    if m.counting
        track.translation = [26, bottom + 18]
        bar.translation = track.translation
        part = 0
        if m.countFrom > 0 then part = (m.countFrom - m.countLeft) / m.countFrom
        bar.width = 628 * part
        bottom = bottom + 18 + 4
    end if
    m.unRow.translation = [26, bottom + 24]
    height = bottom + 24 + 72 + 26
    m.top.findNode("unPlate").height = height
    m.upNext.translation = [m.f.right - 680, m.f.promptBottom - height]
end sub

' The end of the title: the next entry plays; a film with nothing after it gets Finished.
sub ended()
    closeOptions()
    if m.next <> invalid
        m.promptKey = ""
        m.controls.visible = false
        if passoutCheckDue()
            showUpNext(invalid, "still")
        else if pref("playback.autoplayNext") <> true
            showUpNext(invalid, "end")
        else if pref("playback.upNextCountdownMs") <= 0
            advance("completion")
            return
        else
            showUpNext(invalid, "count")
            countDown(int(pref("playback.upNextCountdownMs") / 1000))
        end if
        m.top.setFocus(true)
        focusPrompt()
    else if m.top.item.kind = "movie"
        showFinished()
    else
        leave(true)
    end if
end sub

sub showFinished()
    stopSession()
    hideAll()
    item = m.top.item
    art = pick(item.images, ["backdrop", "still"])
    m.top.findNode("finArt").uri = imageUrl(art, 1920, 1080)
    m.top.findNode("finTitle").text = item.title
    meta = []
    if item.year <> invalid then meta.push(item.year.toStr())
    if item.durationMs <> invalid then meta.push(durationText(item.durationMs))
    m.top.findNode("finMeta").text = meta.join("   ")
    m.finRow.buttons = [{id: "replay", text: "Replay", icon: "restart"}, {id: "close", text: "Close", icon: ""}]
    m.finished.visible = true
    m.finRow.active = true
    m.finRelated.visible = false
    m.finStars.visible = false
    api("GET", "/v1/items/" + item.id, invalid, "onFinishedItemRead", {id: item.id})
    api("GET", "/v1/items/" + item.id + "/related", invalid, "onFinishedRelatedRead", {id: item.id})
end sub

' Rate this shows your rating, which the server keeps out of ten; the stars are whole, as the other apps draw them.
sub onFinishedItemRead(event as object)
    result = event.getData()
    if not result.ok or not m.finished.visible or event.getRoSGNode().ctx.id <> m.top.item.id then return
    stars = 0
    if result.data.state <> invalid and result.data.state.rating <> invalid then stars = int(result.data.state.rating / 2 + 0.5)
    m.finStars.stars = stars
    m.finStars.visible = true
end sub

' OK on a star rates it that many; on the star it already has, the rating is cleared.
sub onRate(event as object)
    star = event.getData()
    before = m.finStars.stars
    stars = star
    if star = before then stars = 0
    m.finStars.stars = stars
    rating = invalid
    if stars > 0 then rating = stars * 2
    api("PUT", "/v1/items/" + m.top.item.id + "/state", {rating: rating, madeAgoMs: 0}, "onRated", {before: before})
end sub

sub onRated(event as object)
    if not event.getData().ok then m.finStars.stars = event.getRoSGNode().ctx.before      ' not saved: the stars go back
end sub

' More like this film: the related groups in one row, as the title page reads them.
sub onFinishedRelatedRead(event as object)
    result = event.getData()
    if not result.ok or not m.finished.visible or event.getRoSGNode().ctx.id <> m.top.item.id then return
    items = []
    seen = {}
    seen[m.top.item.id] = true
    for each group in result.data.groups
        for each item in withoutReading(group.items)
            if seen[item.id] <> true
                seen[item.id] = true
                items.push(item)
            end if
        end for
    end for
    m.relatedItems = items
    if items.count() = 0 then return
    cards = createObject("roSGNode", "ContentNode")
    for each item in items
        card = cardFor(item, "poster")
        card.art = imageUrl(card.art, 214, 320)
        cards.createChild("ContentNode").addFields(card)
    end for
    m.finRelated.heading = "More like " + m.top.item.title
    m.finRelated.cards = cards
    m.finRelated.visible = true
end sub

' A related title opens its page in place of the player.
sub onFinishedRelated(event as object)
    at = event.getData()
    if m.relatedItems = invalid or at < 0 or at >= m.relatedItems.count() then return
    item = m.relatedItems[at]
    leave(false)
    m.top.open = {item: item, play: false}
end sub

sub onFinished(event as object)
    if event.getData() = "replay"
        m.finished.visible = false
        m.top.setFocus(true)
        fresh()
        m.top.startOver = true
        start()
    else
        leave(false)
    end if
end sub

' --- the controls ---------------------------------------------------------------------------

sub raise()
    if not m.top.hasFocus() then m.top.setFocus(true)
    m.controls.visible = true
    m.hide.control = "stop"
    if m.video.state <> "paused" then m.hide.control = "start"
    paint()
end sub

sub lower()
    if m.video.state = "paused" or m.scrub or m.failed then return     ' paused, a scrub waiting, or failed: the controls stay
    ' still catching up after a jump: the controls stay, and their time runs again once the picture plays
    if m.video.state = "buffering" or m.pending >= 0 then return
    m.controls.visible = false
    m.focus = m.playIndex
    focusPrompt()
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
        if at > m.loadedAt + 8 then m.tries = 0
        if m.pending < 0 then m.last = at
    end if
    if m.controls.visible then paint()
    updatePrompt()
end sub

sub paint()
    if not m.controls.visible then return
    c = colours()
    duration = m.duration
    if duration <= 0 and m.top.item.durationMs <> invalid then duration = m.top.item.durationMs / 1000
    now = m.video.position
    position = now
    if m.pending >= 0 then position = m.pending
    seeking = (m.focus < 0)
    ' the seek bar: played, and while a scrub waits the stretch it would skip, lighter, with a tick where the picture is
    trackY = m.f.barY
    height = 8
    if seeking
        trackY = m.f.barY - 2
        height = 12
    end if
    for each bar in [m.top.findNode("track"), m.played, m.ahead]
        bar.translation = [m.f.left, trackY]
        bar.height = height
    end for
    m.played.width = barX(earlier(position, now), duration) - m.f.left
    m.ahead.visible = (m.pending >= 0)
    if m.ahead.visible
        m.ahead.translation = [barX(earlier(position, now), duration), trackY]
        m.ahead.width = abs(barX(position, duration) - barX(now, duration))
        m.was.translation = [barX(now, duration) - 2, m.f.barY - 9]
    end if
    m.was.visible = m.ahead.visible
    knob = 22
    if seeking then knob = 30
    x = barX(position, duration)
    m.knob.width = knob
    m.knob.height = knob
    m.knob.translation = [x - knob / 2, m.f.barY + 4 - knob / 2]
    m.knobRing.translation = [x - 40, m.f.barY - 36]
    m.knobRing.width = 80
    m.knobRing.height = 80
    m.knobRing.visible = seeking
    m.top.findNode("at").text = clockText(position)
    m.top.findNode("left").text = "-" + clockText(duration - position)
    m.top.findNode("clock").text = timeOfDay(0)
    m.top.findNode("ends").text = "Ends " + timeOfDay(int(duration - position))
    ' while a scrub waits for OK its key hints stand where the buttons are
    m.top.findNode("hints").visible = m.scrub
    m.buttons.visible = not m.scrub
    if m.scrub then placeHints(position, now)
    paused = (m.video.state = "paused")
    flab = m.top.findNode("flab")
    flab.visible = false
    m.ring.visible = false
    for i = m.firstTransport to m.firstPill - 1
        spot = m.row[i]
        on = (i = m.focus and not m.scrub)
        if spot.kind = "play"
            if paused then spot.icon.uri = "pkg:/images/i-play-40.png" else spot.icon.uri = "pkg:/images/i-pause-40.png"
            spot.plate.blendColor = c.text
            spot.icon.blendColor = c.actionInk
        else
            ' glass, lighter under focus
            if on then spot.plate.blendColor = c.glassOn else spot.plate.blendColor = c.glass
            spot.icon.blendColor = c.text
            if spot.number <> invalid then spot.number.color = c.text
        end if
        if on
            across = spot.size * 296 / 216
            m.ring.width = across
            m.ring.height = across
            m.ring.translation = [spot.x + spot.size / 2 - across / 2, m.f.buttonsY + 44 - across / 2]
            m.ring.visible = true
            names = {back: "Back " + m.backSeconds.toStr() + " seconds", forward: "Forward " + m.forwardSeconds.toStr() + " seconds", play: "Pause", previous: "Previous", next: "Next"}
            if spot.kind = "chup" then names.chup = "Channel up   " + neighbour(1).channel.number + " " + neighbour(1).channel.name
            if spot.kind = "chdown" then names.chdown = "Channel down   " + neighbour(-1).channel.number + " " + neighbour(-1).channel.name
            text = names[spot.kind]
            if spot.kind = "play" and paused then text = "Play"
            showLabel(flab, text, spot.x + spot.size / 2, m.f.buttonsY + (88 - spot.size) / 2 - 12)
        end if
    end for
    ' the three choices
    placePills(currentChoices())
    pillRing = m.top.findNode("pillRing")
    pillRing.visible = false
    for i = m.firstPill to m.row.count() - 1
        pill = m.row[i]
        if i = m.focus and not m.scrub
            pill.plate.blendColor = c.glassOn
            pill.value.color = c.text
            out = metrics().focusSpread + 6
            pillRing.translation = [pill.x - out, m.f.buttonsY + 12 - out]
            pillRing.width = pill.width + out * 2
            pillRing.height = 64 + out * 2
            pillRing.visible = true
        else
            pill.plate.blendColor = c.glass
            pill.value.color = c.textSecondary
        end if
        pill.head.color = c.text
    end for
    if m.firstTransport > 0
        episodes = m.row[0]
        if m.focus = 0 and not m.scrub
            episodes.plate.blendColor = c.glassOn
            out = metrics().focusSpread + 6
            pillRing.translation = [episodes.x - out, m.f.buttonsY + 12 - out]
            pillRing.width = episodes.width + out * 2
            pillRing.height = 64 + out * 2
            pillRing.visible = true
        else
            episodes.plate.blendColor = c.glass
        end if
        episodes.icon.blendColor = c.text
        episodes.label.color = c.text
    end if
    ' Playback failed keeps the title and where it stopped; its own buttons are the only ones
    if m.failed
        m.buttons.visible = false
        m.ring.visible = false
        pillRing.visible = false
        flab.visible = false
    end if
    if m.live = true then paintLive()
end sub

' Where a time falls on the seek bar.
function barX(seconds as float, duration as float) as float
    if duration <= 0 then return m.f.left
    fraction = seconds / duration
    if fraction < 0 then fraction = 0
    if fraction > 1 then fraction = 1
    return m.f.left + (m.f.right - m.f.left) * fraction
end function

function earlier(a as float, b as float) as float
    if a < b then return a
    return b
end function

' The focus label: a dark plate with the button's name, centred over it, its foot at `bottom`.
sub showLabel(flab as object, text as string, centre as float, bottom as float)
    label = m.top.findNode("flabText")
    label.text = text
    label.width = 0
    w = label.boundingRect().width + 32
    m.top.findNode("flabPlate").width = w
    label.translation = [16, 0]
    flab.translation = [centre - w / 2, bottom - 40]
    flab.visible = true
end sub

sub togglePause()
    if m.live = true and m.liveWindow <> true and m.video.state = "paused"
        goLive()        ' nothing was kept while paused: Play rejoins the channel live
        return
    end if
    if m.video.state = "paused"
        m.video.control = "resume"
    else if m.video.state = "playing" or m.video.state = "buffering"
        m.video.control = "pause"
    end if
    sendProgress()
    raise()
end sub

' Move the place the picture will go to. A tap while playing jumps once the presses rest; a held
' key, or any move while paused, is a scrub that waits for OK (Back puts it away).
sub nudge(seconds as float)
    duration = m.duration
    if duration <= 0 then return
    if m.pending < 0 then m.pending = m.video.position
    m.pending = m.pending + seconds
    if m.pending < 0 then m.pending = 0
    if m.pending > duration - 2 then m.pending = duration - 2
    if m.video.state = "paused" then m.scrub = true
    m.settle.control = "stop"
    if not m.scrub then m.settle.control = "start"
    raise()
end sub

sub cancelScrub()
    m.settle.control = "stop"
    m.pending = -1
    m.scrub = false
end sub

' A held Left or Right on the seek bar (from Hold.brs): each repeat is a scrub step.
function pressKey(key as string) as boolean
    if m.live = true then return false
    if m.focus >= 0 or not m.controls.visible then return false
    m.scrub = true
    if key = "left" then nudge(-10) else if key = "right" then nudge(10) else return false
    return true
end function

sub applySeek()
    if m.pending < 0 or m.session = invalid then return
    target = m.pending
    m.pending = -1
    m.scrub = false
    m.last = target
    if m.presentation.mode = "direct" or m.presentation.mode = "live"
        m.video.seek = target
    else
        ' A converted stream is the server's: it is told, and answers with where to play from.
        api("PATCH", "/v1/playback/sessions/" + m.session.id, {seek: {positionMs: int(target * 1000), id: createObject("roDeviceInfo").getRandomUUID()}}, "onSeekAnswer", {target: target})
    end if
    sendProgress()
    raise()
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
    holdKey(key, press and m.focus < 0 and m.controls.visible and (key = "left" or key = "right"))
    if not press then return false
    if m.finished.visible
        if key = "back" then leave(false)
        ' the buttons, then Rate this, then More like this
        if key = "down"
            if m.finRow.isInFocusChain() and m.finStars.visible
                m.finStars.active = true
            else if not m.finRelated.isInFocusChain() and m.finRelated.visible
                m.finRelated.active = true
            end if
        else if key = "up"
            if m.finRelated.isInFocusChain() and m.finStars.visible
                m.finStars.active = true
            else if m.finRelated.isInFocusChain() or m.finStars.isInFocusChain()
                m.finRow.active = true
            end if
        end if
        return true
    end if
    if m.notice.visible
        ' Back from Playback failed leaves the player, as everywhere else in it
        if key = "back" then leave(m.session <> invalid)
        return true
    end if
    if key = "back"
        if m.scrub
            cancelScrub()
            raise()
        else if m.controls.visible and m.video.state <> "paused"
            m.controls.visible = false
            m.focus = m.playIndex
            focusPrompt()
        else
            leave(true)
        end if
        return true
    end if
    ' the remote's own transport keys act at once, whatever has focus
    if key = "play"
        togglePause()
        return true
    else if key = "rewind" or key = "replay"
        nudge(-m.backSeconds)
        return true
    else if key = "fastforward"
        nudge(m.forwardSeconds)
        return true
    end if
    if key = "options"
        ' * is the remote's options key: it opens the options panel at Subtitles, as Roku's own does
        openOptions(0)
        return true
    end if
    if not m.controls.visible
        raise()          ' any other key raises the controls, on Play/Pause
        return true
    end if
    if key = "OK"
        if m.focus < 0 and m.live = true
            goLive()
            return true
        else if m.focus < 0
            applySeek()
            return true
        end if
        kind = m.row[m.focus].kind
        if kind = "back"
            nudge(-m.backSeconds)
        else if kind = "forward"
            nudge(m.forwardSeconds)
        else if kind = "play"
            togglePause()
        else if kind = "chup" or kind = "chdown"
            if kind = "chup" then changeChannel(1) else changeChannel(-1)
            return true
        else if kind = "previous" or kind = "next"
            advance(kind)
            return true
        else if kind = "episodes"
            openEpisodes()
            return true
        else
            openOptions(m.focus - m.firstPill)
            return true
        end if
    else if key = "left"
        if m.focus < 0 and m.live = true
            ' Go to live stands alone in its row
        else if m.focus < 0
            nudge(-m.backSeconds)
        else if m.focus > 0
            m.focus = m.focus - 1
        end if
    else if key = "right"
        if m.focus < 0 and m.live = true
            ' Go to live stands alone in its row
        else if m.focus < 0
            nudge(m.forwardSeconds)
        else if m.focus < m.row.count() - 1
            m.focus = m.focus + 1
        end if
    else if key = "up"
        ' on live, Up reaches Go to live when the picture is behind; there is no scrubbing
        if m.live <> true or m.top.findNode("goLive").visible then m.focus = -1
    else if key = "down"
        if m.focus < 0
            if m.scrub then cancelScrub()
            m.focus = m.playIndex
        end if
    end if
    raise()
    return true
end function
