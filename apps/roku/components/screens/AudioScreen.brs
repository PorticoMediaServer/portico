' Focus (tv-focus 33): it starts on Pause. The transport is one row; Right from its end reaches Up
' next, a list that remembers its row; Left from the list returns to the transport. Down from the
' transport reaches a song's actions (Lyrics, Add to playlist, More), Up goes back. Back leaves.
'
' Songs join through Roku's Audio node playing a content list: about half a minute before a song
' ends the server is asked to get the next one ready (prepare-next) and it is added to the list, so
' Roku has it buffered when the song ends; when Roku moves on, the server is told (commit-next).
sub init()
    c = colours()
    f = frames().audio
    m.f = f
    m.audio = m.top.findNode("audio")
    m.top.findNode("back").color = c.ink
    m.top.findNode("fromIcon").translation = [f.artX, f.fromY + 6]
    m.top.findNode("fromIcon").blendColor = c.textSecondary
    from = m.top.findNode("from")
    from.translation = [f.artX + 40, f.fromY]
    from.width = f.art - 40
    setType(from, "meta", c.textSecondary)
    ' the cover, or the plate and mark in its place when there is none
    for each id in ["artBack", "art"]
        node = m.top.findNode(id)
        node.translation = [f.artX, f.artY]
        node.width = f.art
        node.height = f.art
    end for
    m.top.findNode("artBack").blendColor = c.raised
    mark = m.top.findNode("artMark")
    mark.translation = [f.artX + f.art / 2 - 48, f.artY + f.art / 2 - 48]
    mark.blendColor = c.textTertiary
    setType(m.top.findNode("title"), "display", c.text)
    m.top.findNode("title").lineSpacing = -22     ' the title's two lines set close, as the frame draws them
    m.top.findNode("title").width = f.titleWidth
    setType(m.top.findNode("artist"), "heading", c.textSecondary)
    m.top.findNode("artist").width = f.titleWidth
    setType(m.top.findNode("album"), "meta", c.textTertiary)
    m.top.findNode("album").width = f.titleWidth
    ' the scrubber and its times
    track = m.top.findNode("track")
    track.translation = [f.titleX, f.controlsY]
    track.width = f.titleWidth
    track.color = c.track
    m.played = m.top.findNode("played")
    m.played.translation = [f.titleX, f.controlsY]
    m.played.color = c.text
    at = m.top.findNode("at")
    at.translation = [f.titleX, f.controlsY + 20]
    setType(at, "metaStrong", c.textSecondary)
    length = m.top.findNode("length")
    length.translation = [f.titleX + f.titleWidth - 200, f.controlsY + 20]
    setType(length, "metaStrong", c.textSecondary)
    ' Up next
    heading = m.top.findNode("nextHeading")
    heading.translation = [f.nextX, f.nextY]
    setType(heading, "rowHeading", c.text)
    count = m.top.findNode("nextCount")
    count.translation = [f.nextX, f.nextY + 48]
    setType(count, "meta", c.textSecondary)
    m.rows = m.top.findNode("nextRows")
    m.top.findNode("nextWindow").translation = [f.nextX, f.nextY + 100]
    m.top.findNode("nextWindow").clippingRect = [-50, -40, f.nextWidth + 100, f.nextHeight - 100 + 40]
    m.ring = m.top.findNode("ring")
    chapterLine = m.top.findNode("chapterLine")
    chapterLine.translation = [f.titleX, f.controlsY + 236]
    chapterLine.width = f.titleWidth
    setType(chapterLine, "meta", c.textSecondary)
    makeTransport()
    m.actions = m.top.findNode("actions")
    m.actions.translation = [f.titleX, f.actionsY]
    m.actions.buttons = [{id: "lyrics", text: "Lyrics", icon: "lyrics"}, {id: "playlist", text: "Add to playlist", icon: "listPlus"}, {id: "more", text: "", icon: "more"}]
    m.actions.observeField("pressed", "onAction")
    m.showLyrics = false
    m.audio.observeField("state", "onState")
    m.audio.observeField("position", "onPosition")
    m.audio.observeField("contentIndex", "onIndex")
    m.top.findNode("report").observeField("fire", "sendProgress")
    m.top.findNode("again").observeField("fire", "startSession")
    m.clock = createObject("roTimespan")
    m.session = invalid
    m.queue = invalid
    m.upNext = []
    m.chapters = []
    m.focus = 2          ' 0 shuffle, 1 previous, 2 play/pause, 3 next, 4 repeat; -1 is Up next; -2 the actions
    m.row = 0            ' the Up next row in focus, remembered
    m.scroll = 0
end sub

' --- the controls --------------------------------------------------------------------------

' The transport on its own line, centred under the title slot: shuffle and repeat at its ends.
sub makeTransport()
    f = m.f
    centre = f.titleX + f.titleWidth / 2
    middle = f.controlsY + 130
    specs = [{kind: "shuffle", icon: "shuffle-32", size: 64, dx: -214}, {kind: "previous", icon: "previous-32", size: 72, dx: -113}, {kind: "play", icon: "pause-40", size: 116, dx: 0}, {kind: "next", icon: "next-32", size: 72, dx: 113}, {kind: "repeat", icon: "repeat-32", size: 64, dx: 214}]
    transport = m.top.findNode("transport")
    m.spots = []
    for each spec in specs
        x = centre + spec.dx - spec.size / 2
        y = middle - spec.size / 2
        plate = transport.createChild("Poster")
        plate.uri = "pkg:/images/disc.png"
        plate.width = spec.size
        plate.height = spec.size
        plate.translation = [x, y]
        iconSize = 32
        if spec.kind = "play" then iconSize = 40
        icon = transport.createChild("Poster")
        icon.uri = "pkg:/images/i-" + spec.icon + ".png"
        icon.width = iconSize
        icon.height = iconSize
        icon.translation = [x + (spec.size - iconSize) / 2, y + (spec.size - iconSize) / 2]
        ' repeat one carries a small 1
        mark = transport.createChild("Label")
        mark.width = spec.size
        mark.horizAlign = "center"
        mark.translation = [x, y + spec.size - 20]
        setType(mark, "skipSeconds")
        m.spots.push({kind: spec.kind, plate: plate, icon: icon, mark: mark, x: x, y: y, size: spec.size})
    end for
end sub

' What each control can do now: Next needs something after this song.
function usable(i as integer) as boolean
    if m.spots[i].kind = "next" then return (m.queue <> invalid and m.queue.nextEntryId <> invalid) or chapterNow() < m.chapters.count() - 1
    return m.queue <> invalid or m.spots[i].kind = "play"
end function

sub paint()
    c = colours()
    if m.queue = invalid or (m.audio.state <> "paused" and m.parked <> true) then m.spots[2].icon.uri = "pkg:/images/i-pause-40.png" else m.spots[2].icon.uri = "pkg:/images/i-play-40.png"
    m.ring.visible = false
    for i = 0 to m.spots.count() - 1
        spot = m.spots[i]
        on = (i = m.focus)
        ' a control this song cannot use stays in its slot, dimmed
        if usable(i) then spot.plate.opacity = 1 else spot.plate.opacity = 0.4
        if usable(i) then spot.icon.opacity = 1 else spot.icon.opacity = 0.4
        if spot.kind = "play"
            spot.plate.blendColor = c.text
            spot.icon.blendColor = c.actionInk
        else
            if on then spot.plate.blendColor = c.glassOn else spot.plate.blendColor = c.glass
            spot.icon.blendColor = c.text
        end if
        if spot.kind = "shuffle" and m.queue <> invalid and m.queue.shuffle = true then spot.icon.blendColor = c.accentStrong
        if spot.kind = "repeat" and m.queue <> invalid and m.queue.repeat <> "off" then spot.icon.blendColor = c.accentStrong
        spot.mark.text = ""
        if spot.kind = "repeat" and m.queue <> invalid and m.queue.repeat = "one" then spot.mark.text = "1"
        spot.mark.color = c.accentStrong
        if on
            across = spot.size * 296 / 216
            m.ring.width = across
            m.ring.height = across
            m.ring.translation = [spot.x + spot.size / 2 - across / 2, spot.y + spot.size / 2 - across / 2]
            m.ring.visible = true
        end if
    end for
    paintTime()
end sub

' Where the music is: the place it was set aside at while a video has the screen, else the player's.
function here() as float
    if m.parked = true then return m.parkedAt
    return m.audio.position
end function

sub paintTime()
    duration = m.audio.duration
    if duration <= 0 and m.item <> invalid and m.item.durationMs <> invalid then duration = m.item.durationMs / 1000
    position = here()
    fraction = 0
    if duration > 0 then fraction = position / duration
    if fraction > 1 then fraction = 1
    m.played.width = m.f.titleWidth * fraction
    m.top.findNode("at").text = clockOf(position)
    m.top.findNode("length").text = clockOf(duration)
end sub

function clockOf(seconds as float) as string
    total = int(seconds)
    if total < 0 then total = 0
    mins = int(total / 60)
    secs = total mod 60
    text = secs.toStr()
    if secs < 10 then text = "0" + text
    return mins.toStr() + ":" + text
end function

' The title slot: the song over its artist and album, two lines at most, set on the slot's foot.
sub drawItem(entry as object)
    f = m.f
    title = m.top.findNode("title")
    title.text = entry.title
    artist = m.top.findNode("artist")
    artist.text = ""
    if entry.rootTitle <> invalid then artist.text = entry.rootTitle else if m.author <> invalid then artist.text = m.author
    album = m.top.findNode("album")
    line = ""
    if entry.parentTitle <> invalid then line = entry.parentTitle
    if entry.year <> invalid then line = (line + "   " + entry.year.toStr()).trim()
    album.text = line
    foot = f.titleY + f.titleHeight
    album.translation = [f.titleX, foot - 34]
    artist.translation = [f.titleX, foot - 34 - 52]
    titleHeight = title.boundingRect().height
    title.translation = [f.titleX, foot - 34 - 52 - titleHeight - 8]
    cover = pick(entry.images, ["cover", "poster", "thumb"])
    m.top.findNode("art").uri = imageUrl(cover, f.art, f.art)
    m.top.findNode("art").visible = (cover <> "")
    m.top.findNode("artMark").visible = (cover = "")
    readLyrics(entry)
end sub

' --- a song's actions and its lyrics ---------------------------------------------------------

' The song now playing, by its item id (a queue entry names it itemId, the item a page asked for id).
function songId() as string
    if m.item = invalid then return ""
    if m.item.itemId <> invalid then return m.item.itemId
    if m.item.id <> invalid then return m.item.id
    return ""
end function

' A song's actions show under the transport; a book has none (no lyrics, and its chapter line sits there).
function isSong() as boolean
    return m.ask <> invalid and m.ask.item.kind <> "audiobook" and m.chapters.count() = 0
end function

' Each new song asks for its lyrics (GET /v1/items/{id}/lyrics, as the other apps do); Lyrics stays in
' its slot, dimmed, while the server has none.
sub readLyrics(entry as object)
    id = entry.itemId
    if id = invalid then id = entry.id
    if id = invalid or id = m.lyricsFor then return
    m.lyricsFor = id
    m.lyrics = invalid
    paintActions()
    if m.showLyrics then drawSide()
    if isSong() then api("GET", "/v1/items/" + id + "/lyrics", invalid, "onLyrics", {id: id})
end sub

sub onLyrics(event as object)
    result = event.getData()
    if event.getRoSGNode().ctx.id <> m.lyricsFor then return
    if result.ok and result.data.items.count() > 0 then m.lyrics = result.data.items[0]
    paintActions()
    if m.showLyrics then drawSide()
end sub

sub paintActions()
    m.actions.visible = isSong()
    lyricsButton = m.actions.getChild(0)
    if lyricsButton <> invalid
        if m.lyrics <> invalid then lyricsButton.opacity = 1 else lyricsButton.opacity = 0.4
    end if
end sub

sub onAction(event as object)
    which = event.getData()
    if which = "lyrics"
        if m.lyrics = invalid then return      ' dimmed: the server has none for this song
        m.showLyrics = not m.showLyrics
        drawSide()
    else if which = "playlist"
        if songId() <> "" then askPlaylists([songId()], m.item.title)
    else if which = "more"
        ' the song's state (a favorite or not) is read before its menu opens
        if songId() <> "" then api("GET", "/v1/items/" + songId(), invalid, "onSongRead", {id: songId()})
    end if
end sub

' More (tv-07-f7): Add to or Remove from Favorites, Add to playlist, Go to album, Go to artist.
sub onSongRead(event as object)
    result = event.getData()
    if not result.ok or event.getRoSGNode().ctx.id <> songId() then return
    m.song = result.data
    rows = []
    if m.song.state <> invalid and m.song.state.favorite = true then rows.push({id: "fav", text: "Remove from Favorites", icon: ""}) else rows.push({id: "fav", text: "Add to Favorites", icon: ""})
    rows.push({id: "pl", text: "Add to playlist…", icon: ""})
    if m.song.parentId <> invalid then rows.push({id: "album", text: "Go to album", icon: "", gapBefore: true})
    if m.song.rootId <> invalid then rows.push({id: "artist", text: "Go to artist", icon: ""})
    m.top.menu = {eyebrow: "", title: m.song.title, status: "", rows: rows}
end sub

sub onMenuChosen(event as object)
    which = event.getData()
    if addToPlaylist(which) or m.song = invalid then return
    song = m.song
    if which = "fav"
        on = not (song.state <> invalid and song.state.favorite = true)
        api("PUT", "/v1/items/" + song.id + "/state", {favorite: on, madeAgoMs: 0}, "onFavoriteSaved")
    else if which = "pl"
        askPlaylists([song.id], song.title)
    else if which = "album"
        m.top.open = {item: {id: song.parentId, kind: "album", title: song.parentTitle, libraryId: song.libraryId}, play: false}
    else if which = "artist"
        m.top.open = {item: {id: song.rootId, kind: "artist", title: song.rootTitle, libraryId: song.libraryId}, play: false}
    end if
end sub

sub onFavoriteSaved(event as object)
    result = event.getData()
    if result.ok and m.song <> invalid then m.song.state = result.data
end sub

' The right-hand column: Up next, or the lyrics while Lyrics is on.
sub drawSide()
    showing = m.showLyrics and m.lyrics <> invalid
    m.rows.visible = not showing
    lines = m.top.findNode("lyricsLines")
    lines.visible = showing
    lines.removeChildrenIndex(lines.getChildCount(), 0)
    if not showing
        m.top.findNode("nextHeading").text = "Up next"
        drawUpNext()
        return
    end if
    c = colours()
    m.top.findNode("nextHeading").text = "Lyrics"
    m.top.findNode("nextHeading").visible = true
    count = m.top.findNode("nextCount")
    count.visible = true
    count.text = ""
    if m.lyrics.credit <> invalid then count.text = m.lyrics.credit
    m.lyricYs = []
    y = 0
    for each line in m.lyrics.lines
        label = lines.createChild("Label")
        label.text = line.text
        label.width = m.f.nextWidth
        label.wrap = true
        setType(label, "listLabel", c.textSecondary)
        label.translation = [0, y]
        m.lyricYs.push(y)
        y = y + label.boundingRect().height + 14
    end for
    m.lyricAt = -1
    m.lyricScroll = 0
    followLyrics()
end sub

' Timed lyrics follow the song: the line being sung is lit and kept a little below the top.
sub followLyrics()
    if not (m.showLyrics and m.lyrics <> invalid) or m.lyrics.format <> "lrc" then return
    at = here() * 1000 + m.lyrics.offsetMs
    now = -1
    for i = 0 to m.lyrics.lines.count() - 1
        line = m.lyrics.lines[i]
        if line.atMs <> invalid and line.atMs <= at then now = i
    end for
    if now = m.lyricAt then return
    m.lyricAt = now
    c = colours()
    lines = m.top.findNode("lyricsLines")
    for i = 0 to lines.getChildCount() - 1
        if i = now then lines.getChild(i).color = c.text else lines.getChild(i).color = c.textSecondary
    end for
    if now >= 0 then scrollLyrics(m.lyricYs[now] - 120)
end sub

sub scrollLyrics(y as float)
    if y < 0 then y = 0
    m.lyricScroll = y
    m.top.findNode("lyricsLines").translation = [0, -y]
end sub


' --- the queue and the sessions ----------------------------------------------------------

sub onPlay()
    ask = m.top.play
    stopSession()
    m.item = ask.item
    m.ask = ask
    m.author = ask.item.rootTitle     ' an entry of the queue does not carry a book's author
    m.chapters = []
    m.showLyrics = false
    m.lyricsFor = invalid
    m.top.findNode("lyricsLines").visible = false
    m.rows.visible = true
    m.top.findNode("chapterLine").text = ""
    m.top.findNode("nextHeading").text = "Up next"
    if ask.item.kind = "audiobook" then api("GET", "/v1/items/" + ask.item.id, invalid, "onBookRead")
    m.queue = invalid
    m.prepared = invalid
    m.parked = false
    m.focus = 2
    m.row = 0
    m.top.findNode("from").text = ""
    m.top.findNode("fromIcon").visible = false
    drawItem(queueItemEntry(ask.item))
    body = ask.queue
    if body = invalid or body.source = invalid then body = queueBody(ask.item)
    api("POST", "/v1/queues", body, "onQueue")
    paint()
end sub

' An item as the queue's entries carry it, for drawing before the queue answers.
function queueItemEntry(item as object) as object
    return {title: item.title, rootTitle: item.rootTitle, parentTitle: item.parentTitle, year: item.year, images: item.images}
end function

sub onQueue(event as object)
    result = event.getData()
    if not result.ok
        leave()
        return
    end if
    m.queue = result.data.queue
    from = m.top.findNode("from")
    entry = queueEntry(result.data.window, m.queue.current.entryId)
    if entry <> invalid and entry.parentTitle <> invalid then from.text = "Playing from " + entry.parentTitle
    m.top.findNode("fromIcon").visible = (from.text <> "")
    startSession()
end sub

sub startSession()
    if m.queue = invalid or m.queue.current = invalid then return
    if m.startKey = invalid then m.startKey = createObject("roDeviceInfo").getRandomUUID()
    body = {queue: {queueId: m.queue.id, entryId: m.queue.current.entryId}}
    ' a book from its start; otherwise the server resumes where it stopped
    if m.ask <> invalid and m.ask.startOver = true then body.startPositionMs = 0
    ' back from being set aside for a video: from where it was, in a fresh session
    if m.resumeAtMs <> invalid
        body.startPositionMs = m.resumeAtMs
        m.resumeAtMs = invalid
    end if
    api("POST", "/v1/playback/sessions", body, "onSession", invalid, false, m.startKey)
end sub

sub onSession(event as object)
    result = event.getData()
    if result.ok and result.status = 202
        again = m.top.findNode("again")
        again.duration = 1
        again.control = "start"      ' the stream is being made ready
        return
    end if
    m.startKey = invalid
    if not result.ok
        leave()
        return
    end if
    m.session = result.data
    m.playedMs = 0
    m.clock.mark()
    content = createObject("roSGNode", "ContentNode")
    song = songNode(m.session.presentation)
    p = m.session.presentation
    if p.startPositionMs <> invalid and p.startPositionMs > 0 then song.playStart = p.startPositionMs / 1000
    content.appendChild(song)
    m.index = 0
    m.audio.contentIsPlaylist = true
    m.audio.content = content
    m.audio.control = "play"
    report = m.top.findNode("report")
    if m.session.lease <> invalid and m.session.lease.reportEveryMs <> invalid then report.duration = m.session.lease.reportEveryMs / 1000
    report.control = "start"
    readQueue()
end sub

' The queue as it stands once a song starts: a queue built from a list fills in what follows
' after it is made, so what comes next is read again here.
sub readQueue()
    if m.queue = invalid then return
    api("GET", "/v1/queues/" + m.queue.id, invalid, "onQueueRead", {queueId: m.queue.id})
end sub

sub onQueueRead(event as object)
    result = event.getData()
    if not result.ok or m.queue = invalid or event.getRoSGNode().ctx.queueId <> m.queue.id then return
    m.queue = result.data
    readUpNext()
    paint()
end sub

function songNode(p as object) as object
    node = createObject("roSGNode", "ContentNode")
    node.url = m.global.origin + p.url
    format = "mp3"
    container = ""
    if p.container <> invalid then container = lcase(p.container)
    if instr(1, p.url, ".m3u8") > 0 then format = "hls"
    if container = "flac" then format = "flac"
    if container = "mp4" or container = "m4a" or container = "mov" or container = "aac" then format = "mp4"
    if container = "wav" then format = "wav"
    node.streamFormat = format
    return node
end function

' Half a minute before the end the next song is got ready and added to the list.
sub onPosition()
    paintTime()
    publish()
    followLyrics()
    if m.chapters.count() > 0 then drawChapterLine()
    if m.chapters.count() > 0 then markChapters()
    ' just after Roku moves on, the position still reads the last song's end: wait for the new start
    if m.awaitStart = true
        if m.audio.position < 5 then m.awaitStart = false else return
    end if
    if m.session = invalid or m.prepared <> invalid or m.queue = invalid or m.queue.nextEntryId = invalid then return
    duration = m.audio.duration
    if duration > 0 and m.audio.position >= duration - 30
        m.prepared = {asked: true}
        api("POST", "/v1/queues/" + m.queue.id + "/prepare-next", {sessionId: m.session.id}, "onPrepared", {queueId: m.queue.id})
    end if
end sub

sub onPrepared(event as object)
    result = event.getData()
    if m.queue = invalid or event.getRoSGNode().ctx.queueId <> m.queue.id then return
    if not result.ok
        m.prepared = invalid     ' the song ends and the queue is moved on the usual way
        return
    end if
    m.prepared = result.data
    m.audio.content.appendChild(songNode(result.data.presentation))
end sub

' Roku moved on to the song got ready: it becomes the session, and the queue moves with it.
sub onIndex()
    if m.audio.contentIndex <= m.index or m.prepared = invalid or m.prepared.token = invalid then return
    m.index = m.audio.contentIndex
    m.awaitStart = true
    api("POST", "/v1/queues/" + m.queue.id + "/commit-next", {token: m.prepared.token}, "onCommitted")
    m.prepared = invalid
end sub

sub onCommitted(event as object)
    result = event.getData()
    if not result.ok then return
    m.session = result.data.session
    m.queue = result.data.queue
    m.playedMs = 0
    m.clock.mark()
    readQueue()
end sub

sub onState()
    state = m.audio.state
    playing = (state = "playing")
    if playing and not m.clockRunning
        m.clock.mark()
    else if not playing and m.clockRunning = true
        m.playedMs = m.playedMs + m.clock.totalMilliseconds()
    end if
    m.clockRunning = playing
    publish()
    if state = "finished"
        ' the end of the list: the queue moves on, or it is over
        advance("completion")
    else if state = "error"
        if m.queue <> invalid and m.queue.nextEntryId <> invalid then advance("next") else leave()
    end if
    paint()
end sub

' Next, Previous, a row of Up next, or the end of the songs Roku had: the queue moves and the song
' it lands on starts.
sub advance(reason as string, entryId = "" as string)
    if m.queue = invalid or m.queue.current = invalid then return
    body = {reason: reason, fromEntryId: m.queue.current.entryId}
    if entryId <> "" then body.entryId = entryId
    if reason = "completion" and m.session <> invalid then body.sessionId = m.session.id
    stopSession()
    m.prepared = invalid
    api("POST", "/v1/queues/" + m.queue.id + "/advance", body, "onAdvanced")
end sub

sub onAdvanced(event as object)
    result = event.getData()
    if not result.ok or result.data.current = invalid
        leave()
        return
    end if
    m.queue = result.data
    startSession()
end sub

' --- a book's chapters ---------------------------------------------------------------------

sub onBookRead(event as object)
    result = event.getData()
    if not result.ok then return
    ' a book started from a card does not carry its author: the book names it
    if m.item.rootTitle = invalid and result.data.credits <> invalid
        for each credit in result.data.credits
            if credit.role = "author" and m.item.rootTitle = invalid then m.item.rootTitle = credit.name
        end for
        if m.item.rootTitle <> invalid
            m.author = m.item.rootTitle
            m.top.findNode("artist").text = m.author
        end if
    end if
    m.chapters = bookChapters(result.data)
    paintActions()
    if m.chapters.count() = 0 then return
    ' the Up next region lists the chapters
    m.upNext = []
    for each ch in m.chapters
        m.upNext.push({title: ch.title, durationMs: ch.endMs - ch.startMs})
    end for
    m.top.findNode("nextHeading").text = "Chapters"
    m.top.findNode("nextCount").text = countText(m.chapters.count(), "chapter", "chapters")
    drawUpNext()
    drawChapterLine()
end sub

function chapterNow() as integer
    if m.chapters = invalid then return -1
    at = here() * 1000
    for i = 0 to m.chapters.count() - 1
        if at >= m.chapters[i].startMs and at < m.chapters[i].endMs then return i
    end for
    return 0
end function

' The frame's chapter line: "Chapter 6 of 17   The Journey from Platform Nine and Three-Quarters".
sub drawChapterLine()
    ch = chapterNow()
    m.top.findNode("chapterLine").text = "Chapter " + (ch + 1).toStr() + " of " + m.chapters.count().toStr() + "   " + m.chapters[ch].title
end sub

' --- Up next -------------------------------------------------------------------------------

sub readUpNext()
    if m.queue = invalid then return
    if m.chapters.count() > 0 then return       ' a book lists its chapters instead
    api("GET", "/v1/queues/" + m.queue.id + "/entries?around=current&before=0&after=40", invalid, "onUpNext", {queueId: m.queue.id})
end sub

sub onUpNext(event as object)
    result = event.getData()
    if not result.ok or m.queue = invalid or event.getRoSGNode().ctx.queueId <> m.queue.id then return
    items = result.data.items
    m.upNext = []
    for each entry in items
        if entry.entryId = m.queue.current.entryId
            drawItem(entry)
            m.item = entry
        else if entry.available = true
            m.upNext.push(entry)
        end if
    end for
    count = m.top.findNode("nextCount")
    count.text = ""
    if m.queue.remainingCount <> invalid and m.queue.remainingCount > 0 then count.text = countText(m.queue.remainingCount, "song", "songs") + ", " + durationText(m.queue.remainingMs)
    drawUpNext()
end sub

' The rows, under a heading for where they come from: the album or list, then what was added.
sub drawUpNext()
    c = colours()
    m.rows.removeChildrenIndex(m.rows.getChildCount(), 0)
    m.rowYs = []
    y = 0
    group = ""
    for i = 0 to m.upNext.count() - 1
        entry = m.upNext[i]
        name = "Added by you"
        if entry.added <> true and entry.parentTitle <> invalid then name = "From " + entry.parentTitle
        if name <> group and m.chapters.count() = 0
            label = m.rows.createChild("Label")
            label.text = name
            label.translation = [0, y]
            setType(label, "groupLabel", c.textSecondary)
            y = y + 40
            group = name
        end if
        row = m.rows.createChild("TrackRow")
        row.rowWidth = m.f.nextWidth
        row.translation = [0, y]
        art = pick(entry.images, ["cover", "poster", "thumb"])
        sub_ = ""
        if entry.rootTitle <> invalid then sub_ = entry.rootTitle
        duration = ""
        if entry.durationMs <> invalid then duration = clockOf(entry.durationMs / 1000)
        model = {art: imageUrl(art, 72, 72), title: entry.title, sub: sub_, duration: duration}
        if m.chapters.count() > 0 then model = {number: (i + 1).toStr(), title: entry.title, duration: duration}
        row.model = model
        m.rowYs.push(y)
        y = y + 88
    end for
    ' nothing after this (the last song, a book in one file): no heading over an empty column
    if not (m.showLyrics and m.lyrics <> invalid)
        m.top.findNode("nextHeading").visible = (m.upNext.count() > 0)
        m.top.findNode("nextCount").visible = (m.upNext.count() > 0)
    end if
    ' the TrackRows are every other child after a heading: keep a list of them for focus
    m.trackRows = []
    for i = 0 to m.rows.getChildCount() - 1
        if m.rows.getChild(i).subtype() = "TrackRow" then m.trackRows.push(m.rows.getChild(i))
    end for
    if m.row >= m.trackRows.count() then m.row = m.trackRows.count() - 1
    if m.row < 0 then m.row = 0
    if m.focus < 0 and m.trackRows.count() = 0 then m.focus = 3
    m.markedChapter = -1
    if m.chapters.count() > 0 then markChapters()
    paintRows()
end sub

' The chapters already heard are ticked and dimmed; the one playing is lit, with how far in it is.
sub markChapters()
    if m.trackRows = invalid or m.trackRows.count() <> m.chapters.count() then return
    ch = chapterNow()
    if ch <> m.markedChapter
        m.markedChapter = ch
        for i = 0 to m.trackRows.count() - 1
            model = m.trackRows[i].model
            model.done = (i < ch)
            model.playing = (i = ch)
            model.delete("progress")
            m.trackRows[i].model = model
        end for
    end if
    chapter = m.chapters[ch]
    part = 0
    if chapter.endMs > chapter.startMs then part = (here() * 1000 - chapter.startMs) / (chapter.endMs - chapter.startMs)
    if part < 0 then part = 0
    if part > 1 then part = 1
    model = m.trackRows[ch].model
    model.progress = part
    m.trackRows[ch].model = model
end sub

sub paintRows()
    if m.trackRows = invalid then return
    room = m.f.nextHeight - 100 - 88
    if m.rowYs.count() > 0
        y = m.rowYs[m.row]
        if y - m.scroll > room then m.scroll = y - room
        if y - m.scroll < 0 then m.scroll = y
    end if
    m.rows.translation = [0, -m.scroll]
    for i = 0 to m.trackRows.count() - 1
        m.trackRows[i].on = (m.focus < 0 and i = m.row)
    end for
end sub

' --- reports -------------------------------------------------------------------------------

function playClock() as integer
    total = m.playedMs
    if m.clockRunning = true then total = total + m.clock.totalMilliseconds()
    return total
end function

sub sendProgress()
    if m.session = invalid then return
    state = "playing"
    if m.audio.state = "paused" then state = "paused"
    api("PUT", "/v1/playback/sessions/" + m.session.id + "/progress", {state: state, positionMs: int(m.audio.position * 1000), playClockMs: playClock()}, "onProgress")
end sub

sub onProgress()
end sub

' The session ends with where the song got to and the time played, in one step (as the video player does).
sub stopSession()
    m.top.findNode("report").control = "stop"
    if m.session = invalid then return
    body = {positionMs: int(m.audio.position * 1000)}
    if m.session.progressToken <> invalid
        body.progressToken = m.session.progressToken
        body.playClockMs = playClock()
    end if
    api("DELETE", "/v1/playback/sessions/" + m.session.id, body, "onProgress")
    m.session = invalid
end sub

sub leave()
    stopSession()
    m.audio.control = "stop"
    m.queue = invalid
    m.parked = false
    m.top.now = {}
    m.top.closed = true
end sub

sub onEnqueue()
    ask = m.top.enqueue
    if m.queue = invalid then return
    ' the server asks every change to a queue's entries to name its revision; a song added goes in
    ' whatever else changed meanwhile, so it names any ("*")
    api("POST", "/v1/queues/" + m.queue.id + "/entries", {source: {items: [ask.item.id]}, placement: ask.placement}, "onQueueChanged", invalid, false, "", "", "*")
end sub

sub onCommand()
    kind = m.top.command
    if kind = "stop"
        leave()
    else if kind = "park"
        park()
    else
        pressControl(kind)
    end if
end sub

' A video is starting. Roku plays one stream at a time (a paused song kept the film from starting),
' so the music is set aside as the other apps park it: its session ends where it was, the card shows
' it paused, and Play brings it back from there in a fresh session.
sub park()
    if m.queue = invalid or m.parked = true then return
    m.parkedAt = m.audio.position
    stopSession()
    m.prepared = invalid
    m.audio.control = "stop"
    m.parked = true
    publish()
    paint()
end sub

' Play, Next or Previous on music set aside: it starts again, at `seconds` into what is current.
sub unpark(seconds as float)
    m.parked = false
    m.resumeAtMs = int(seconds * 1000)
    m.startKey = invalid
    startSession()
end sub

' What the card shows, kept current as the song plays. Next works there as here: the next song, or a book's next chapter.
sub publish()
    if m.queue = invalid or m.item = invalid then return
    album = ""
    if m.item.parentTitle <> invalid then album = m.item.parentTitle
    if m.item.year <> invalid then album = (album + "   " + m.item.year.toStr()).trim()
    duration = m.audio.duration
    if duration <= 0 and m.item.durationMs <> invalid then duration = m.item.durationMs / 1000
    ' a queue entry names its title itemId; the item a page asked to play names it id
    itemId = m.item.itemId
    if itemId = invalid then itemId = m.item.id
    m.top.now = {itemId: itemId, title: m.item.title, artist: m.item.rootTitle, album: album, art: m.top.findNode("art").uri, position: here(), duration: duration, paused: (m.audio.state = "paused" or m.parked = true), hasNext: (m.queue.nextEntryId <> invalid or chapterNow() < m.chapters.count() - 1)}
end sub

' --- keys ------------------------------------------------------------------------------------

sub onActive()
    if m.top.active then focusHere()
    paint()
end sub

' Focus on the action row when it was there, else on the screen (held by the transport, so that
' focus leaving the row is a real move: setFocus on this screen would leave it in the row).
sub focusHere()
    if m.focus = -2 and m.actions.visible then m.actions.active = true else m.top.findNode("transport").setFocus(true)
end sub

sub pressControl(kind as string)
    if m.parked = true
        ch = chapterNow()
        if kind = "play"
            unpark(m.parkedAt)
            return
        else if kind = "next" and m.chapters.count() > 0 and ch < m.chapters.count() - 1
            unpark(m.chapters[ch + 1].startMs / 1000)
            return
        else if kind = "previous" and m.chapters.count() > 0
            unpark(m.chapters[ch].startMs / 1000)
            return
        else if kind = "previous" and m.parkedAt > 3
            unpark(0)
            return
        else if kind = "next" or kind = "previous"
            m.parked = false        ' the queue moves, and the song it lands on starts
        end if
    end if
    if kind = "play"
        if m.audio.state = "paused" then m.audio.control = "resume" else if m.audio.state = "playing" then m.audio.control = "pause"
        sendProgress()
    else if kind = "next"
        ch = chapterNow()
        if m.chapters.count() > 0 and ch < m.chapters.count() - 1
            m.audio.seek = m.chapters[ch + 1].startMs / 1000      ' a book steps by chapter
        else if m.queue <> invalid and m.queue.nextEntryId <> invalid
            advance("next")
        end if
    else if kind = "previous" and m.chapters.count() > 0
        ch = chapterNow()
        start = m.chapters[ch].startMs / 1000
        if m.audio.position - start < 3 and ch > 0 then start = m.chapters[ch - 1].startMs / 1000
        m.audio.seek = start
    else if kind = "previous"
        ' a song a few seconds in starts again; at its start, the one before
        if m.audio.position > 3 then m.audio.seek = 0 else advance("previous")
    else if kind = "shuffle" and m.queue <> invalid
        api("PATCH", "/v1/queues/" + m.queue.id, {shuffle: not (m.queue.shuffle = true)}, "onQueueChanged")
    else if kind = "repeat" and m.queue <> invalid
        modes = {off: "all", all: "one", one: "off"}
        api("PATCH", "/v1/queues/" + m.queue.id, {repeat: modes[m.queue.repeat]}, "onQueueChanged")
    end if
end sub

' Shuffle or repeat changed: what comes next may have changed, so a song got ready is dropped.
sub onQueueChanged(event as object)
    result = event.getData()
    if not result.ok then return
    m.queue = result.data
    if m.prepared <> invalid and m.audio.content.getChildCount() > m.index + 1 then m.audio.content.removeChildIndex(m.index + 1)
    m.prepared = invalid
    readUpNext()
    paint()
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not press then return false
    if key = "back"
        ' the screen goes; the music carries on, with its card on the pages
        m.top.closed = true
        return true
    end if
    if key = "play"
        pressControl("play")
    else if m.focus = -2
        ' the action row gave the key up: Up goes back to the transport, on Play/Pause
        if key = "up"
            m.focus = 2
            focusHere()
        end if
    else if m.focus = -1 and m.showLyrics and m.lyrics <> invalid
        ' lyrics in Up next's place: Up and Down read through them, Left returns
        if key = "up" then scrollLyrics(m.lyricScroll - 80)
        if key = "down" then scrollLyrics(m.lyricScroll + 80)
        if key = "left" then m.focus = 4
    else if m.focus < 0
        if key = "up" and m.row > 0
            m.row = m.row - 1
        else if key = "down" and m.row < m.trackRows.count() - 1
            m.row = m.row + 1
        else if key = "left"
            m.focus = 4
        else if key = "OK"
            if m.chapters.count() > 0 then m.audio.seek = m.chapters[m.row].startMs / 1000 else advance("entry", m.upNext[m.row].entryId)
        end if
    else
        if key = "left"
            i = m.focus - 1
            while i >= 0 and not usable(i)
                i = i - 1
            end while
            if i >= 0 then m.focus = i
        else if key = "right"
            i = m.focus + 1
            while i < m.spots.count() and not usable(i)
                i = i + 1
            end while
            if i < m.spots.count()
                m.focus = i
            else if (m.trackRows <> invalid and m.trackRows.count() > 0) or (m.showLyrics and m.lyrics <> invalid)
                m.focus = -1          ' Up next, on the row it was left on (or the lyrics)
            end if
        else if key = "OK"
            pressControl(m.spots[m.focus].kind)
        else if key = "down" and m.actions.visible
            m.focus = -2
            focusHere()
        end if
    end if
    paint()
    paintRows()
    return true
end function
