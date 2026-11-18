' At rest the card is one line of the header row's empty end; open, it grows down and left from the
' same corner, with focus on Pause. Every line comes from frames().nowPlaying.
sub init()
    c = colours()
    m.f = frames().nowPlaying
    m.card = m.top.findNode("card")
    m.ring = m.top.findNode("ring")
    m.top.findNode("dim").color = c.ink
    m.top.findNode("plate").blendColor = c.promptPlate
    m.top.findNode("artBack").blendColor = c.raisedBright
    setType(m.top.findNode("playingLabel"), "groupLabel", c.accentStrong)
    setType(m.top.findNode("at"), "help", c.textSecondary)
    setType(m.top.findNode("length"), "help", c.textSecondary)
    setType(m.top.findNode("hint"), "help", c.textSecondary)
    m.top.findNode("lineTrack").color = c.glass
    m.top.findNode("line").color = c.accentStrong
    m.top.findNode("pausedMark").blendColor = c.textSecondary
    m.spots = []
    controls = m.top.findNode("controls")
    for each spec in [{kind: "previous", icon: "previous-32", size: 72}, {kind: "play", icon: "pause-40", size: 100}, {kind: "next", icon: "next-32", size: 72}, {kind: "stop", icon: "stop-28", size: 72}]
        plate = controls.createChild("Poster")
        plate.uri = "pkg:/images/disc.png"
        plate.width = spec.size
        plate.height = spec.size
        icon = controls.createChild("Poster")
        icon.uri = "pkg:/images/i-" + spec.icon + ".png"
        m.spots.push({kind: spec.kind, plate: plate, icon: icon, size: spec.size, x: 0, y: 0})
    end for
    m.isOpen = false
    m.focus = 1
    m.top.observeField("focusedChild", "onFocusMoved")
    layout()
end sub

' One thing has focus at a time: when focus goes elsewhere while the card is open (a page that takes
' it back, the music ending), the card folds to its small state. Focus has already moved, so it stays
' where it went.
sub onFocusMoved()
    if not m.isOpen or m.top.isInFocusChain() then return
    m.isOpen = false
    layout()
    paint()
end sub

sub onActive()
    m.isOpen = m.top.active
    if m.isOpen
        m.focus = 1
        m.top.setFocus(true)
    end if
    layout()
end sub

' Places every part for the card's state: one line at rest, the controls when open.
sub layout()
    c = colours()
    f = m.f
    open = m.isOpen
    width = f.width
    height = f.height
    if open
        width = f.openWidth
        height = f.openHeight
    end if
    m.top.findNode("dim").visible = open
    m.card.translation = [f.right - width, f.top]
    m.top.findNode("plate").width = width
    m.top.findNode("plate").height = height
    art = 64
    pad = 10
    if open
        art = 170
        pad = 30
    end if
    for each id in ["artBack", "art"]
        node = m.top.findNode(id)
        node.translation = [pad, pad]
        node.width = art
        node.height = art
    end for
    x = pad + art + 24
    title = m.top.findNode("title")
    artist = m.top.findNode("artist")
    album = m.top.findNode("album")
    m.top.findNode("playingLabel").visible = open
    album.visible = open
    if open
        m.top.findNode("playingLabel").translation = [x + 34, pad + 4]
        m.top.findNode("meter").translation = [x, pad + 4]
        setType(title, "title", c.text)
        title.translation = [x, pad + 32]
        setType(artist, "heading", c.text)
        artist.translation = [x, pad + 92]
        setType(album, "meta", c.textSecondary)
        album.translation = [x, pad + 136]
    else
        m.top.findNode("meter").translation = [width - 48, 30]
        setType(title, "listLabel", c.text)
        title.translation = [x, 12]
        setType(artist, "help", c.textSecondary)
        artist.translation = [x, 46]
    end if
    title.width = width - x - pad - 40
    artist.width = width - x - pad - 40
    album.width = width - x - pad
    ' the progress line: along the foot at rest, a bar over the times when open
    lineY = height - 4
    lineX = 0
    lineWidth = width
    if open
        lineX = pad
        lineY = pad + art + 28
        lineWidth = width - pad * 2
    end if
    m.top.findNode("lineTrack").translation = [lineX, lineY]
    m.top.findNode("lineTrack").width = lineWidth
    m.lineX = lineX
    m.lineWidth = lineWidth
    m.top.findNode("line").translation = [lineX, lineY]
    at = m.top.findNode("at")
    length = m.top.findNode("length")
    at.visible = open
    length.visible = open
    at.translation = [pad, lineY + 14]
    length.translation = [width - pad - 200, lineY + 14]
    controls = m.top.findNode("controls")
    controls.visible = open
    openButton = m.top.findNode("open")
    openButton.visible = open
    m.top.findNode("hint").visible = open
    if open
        middle = lineY + 110
        x = pad
        for each spot in m.spots
            spot.x = x
            spot.y = middle - spot.size / 2
            spot.plate.translation = [spot.x, spot.y]
            iconSize = 32
            if spot.kind = "play" then iconSize = 40
            if spot.kind = "stop" then iconSize = 28
            spot.icon.width = iconSize
            spot.icon.height = iconSize
            spot.icon.translation = [spot.x + (spot.size - iconSize) / 2, spot.y + (spot.size - iconSize) / 2]
            x = x + spot.size + 18
        end for
        openButton.translation = [width - pad - openButton.buttonWidth, middle - 36]
        m.top.findNode("hint").translation = [pad, middle + 66]
    end if
    paint()
end sub

sub paint()
    c = colours()
    now = m.top.now
    if now = invalid or now.title = invalid then return
    m.top.findNode("title").text = now.title
    m.top.findNode("artist").text = ""
    if now.artist <> invalid then m.top.findNode("artist").text = now.artist
    m.top.findNode("album").text = ""
    if now.album <> invalid then m.top.findNode("album").text = now.album
    m.top.findNode("art").uri = ""
    if now.art <> invalid then m.top.findNode("art").uri = now.art
    paused = (now.paused = true)
    if paused then m.top.findNode("playingLabel").text = "Paused" else m.top.findNode("playingLabel").text = "Playing"
    m.top.findNode("meter").visible = not paused
    pausedMark = m.top.findNode("pausedMark")
    pausedMark.visible = paused and not m.isOpen
    pausedMark.translation = [m.f.width - 48, 30]
    fraction = 0
    if now.duration <> invalid and now.duration > 0 then fraction = now.position / now.duration
    if fraction > 1 then fraction = 1
    m.top.findNode("line").width = m.lineWidth * fraction
    m.top.findNode("at").text = clockOf(now.position)
    m.top.findNode("length").text = clockOf(now.duration)
    m.ring.visible = false
    for i = 0 to m.spots.count() - 1
        spot = m.spots[i]
        on = (m.isOpen and i = m.focus)
        if spot.kind = "play"
            spot.plate.blendColor = c.text
            spot.icon.blendColor = c.actionInk
            if paused then spot.icon.uri = "pkg:/images/i-play-40.png" else spot.icon.uri = "pkg:/images/i-pause-40.png"
        else
            if on then spot.plate.blendColor = c.glassOn else spot.plate.blendColor = c.glass
            spot.icon.blendColor = c.text
        end if
        ' Next waits for something after this song: it stays in its place, dimmed
        if spot.kind = "next" and now.hasNext <> true then spot.icon.opacity = 0.4 else spot.icon.opacity = 1
        if on
            ring = m.ring
            across = spot.size * 296 / 216
            ring.width = across
            ring.height = across
            ring.translation = [spot.x + spot.size / 2 - across / 2, spot.y + spot.size / 2 - across / 2]
            ring.visible = true
        end if
    end for
    m.top.findNode("open").on = (m.isOpen and m.focus = m.spots.count())
end sub

function clockOf(seconds as dynamic) as string
    if seconds = invalid then seconds = 0
    total = int(seconds)
    mins = int(total / 60)
    secs = total mod 60
    text = secs.toStr()
    if secs < 10 then text = "0" + text
    return mins.toStr() + ":" + text
end function

function onKeyEvent(key as string, press as boolean) as boolean
    if not press or not m.isOpen then return false
    if key = "back" or key = "down"
        m.isOpen = false
        layout()
        m.top.dismissed = true
    else if key = "left" and m.focus > 0
        m.focus = m.focus - 1
    else if key = "right" and m.focus < m.spots.count()
        m.focus = m.focus + 1
    else if key = "OK" or key = "play"
        if key = "play" or m.focus < m.spots.count()
            kind = "play"
            if key = "OK" then kind = m.spots[m.focus].kind
            if not (kind = "next" and m.top.now.hasNext <> true) then m.top.command = kind
        else
            m.top.command = "open"
        end if
    end if
    paint()
    return true
end function
