' Rows `row` apart from under the heading; focus starts on the channel playing.
sub init()
    c = colours()
    g = frames().miniGuide
    m.g = g
    panel = m.top.findNode("panel")
    panel.blendColor = c.miniGuide
    panel.translation = [g.x, g.y]
    panel.width = g.width
    panel.height = g.rowsY + 5 * g.row + 64
    heading = m.top.findNode("heading")
    heading.translation = [g.x + 30, g.y + 22]
    setType(heading, "heading", c.text)
    hint = m.top.findNode("hint")
    hint.width = g.width - 60
    hint.translation = [g.x + 30, g.y + 34]
    setType(hint, "help", c.textTertiary)
    m.ring = m.top.findNode("ring")
    m.focus = 0
end sub

sub onOpen()
    m.focus = 0
    for i = 0 to m.top.lineup.count() - 1
        if m.top.lineup[i].channel.id = m.top.current then m.focus = i
    end for
    draw()
end sub

sub onActive()
    if m.top.active then m.top.setFocus(true)
end sub

' The five rows round the one in focus (fewer at the ends of the lineup), then Open the full guide.
sub draw()
    c = colours()
    g = m.g
    rows = m.top.findNode("rows")
    rows.removeChildrenIndex(rows.getChildCount(), 0)
    lineup = m.top.lineup
    count = lineup.count()
    first = m.focus - 2
    if first > count - 5 then first = count - 5
    if first < 0 then first = 0
    now = createObject("roDateTime").asSeconds()
    y = g.y + g.rowsY
    m.ringY = -1
    for i = first to first + 4
        if i < count
            drawRow(rows, lineup[i], y, now, i)
            y = y + g.row
        end if
    end for
    ' the last row leaves the player for the whole guide
    open = rows.createChild("Label")
    open.text = "Open the full guide"
    open.translation = [g.x + 34, y + 12]
    setType(open, "listLabel", c.text)
    arrow = rows.createChild("Poster")
    arrow.uri = "pkg:/images/i-forward-28.png"
    arrow.width = 28
    arrow.height = 28
    arrow.blendColor = c.text
    arrow.translation = [g.x + 34 + open.boundingRect().width + 12, y + 14]
    if m.focus = count then m.ringY = y - 4
    out = metrics().focusSpread + 6
    m.ring.translation = [g.x + 18 - out, m.ringY - out]
    m.ring.width = g.width - 36 + out * 2
    m.ring.height = g.row - 4 + out * 2
    if m.focus = count then m.ring.height = 64 + out * 2
end sub

' One channel: its badge, its name and number (Watching when it plays), what is on, how far in and what is next.
sub drawRow(rows as object, row as object, y as integer, now as integer, i as integer)
    c = colours()
    g = m.g
    playing = (row.channel.id = m.top.current)
    if playing or i = m.focus
        plate = rows.createChild("Poster")
        plate.uri = "pkg:/images/round-14.9.png"
        plate.blendColor = c.openGroup
        if i = m.focus then plate.blendColor = c.glass
        plate.translation = [g.x + 18, y]
        plate.width = g.width - 36
        plate.height = g.row - 4
    end if
    if i = m.focus then m.ringY = y
    mark = rows.createChild("ChannelMark")
    mark.size = 60
    mark.channel = row.channel
    mark.translation = [g.x + 34, y + 26]
    x = g.x + 114
    name = rows.createChild("Label")
    name.text = row.channel.name + "   " + row.channel.number
    name.width = g.width - 114 - 140
    name.translation = [x, y + 10]
    setType(name, "help", c.textTertiary)
    if playing
        watching = rows.createChild("Label")
        watching.text = "Watching"
        watching.width = 120
        watching.horizAlign = "right"
        watching.translation = [g.x + g.width - 34 - 120, y + 10]
        setType(watching, "groupLabel", c.accentStrong)
    end if
    onNow = invalid
    following = invalid
    for each p in row.programmes
        s = secondsOf(p.startsAt)
        if s <= now and now < secondsOf(p.endsAt) then onNow = p
        if s > now and following = invalid then following = p
    end for
    title = rows.createChild("Label")
    title.width = g.width - 114 - 34
    title.translation = [x, y + 36]
    setType(title, "listLabel", c.text)
    title.text = row.channel.name
    if onNow <> invalid and onNow.title <> invalid then title.text = onNow.title
    lineX = x
    if onNow <> invalid
        s = secondsOf(onNow.startsAt)
        e = secondsOf(onNow.endsAt)
        track = rows.createChild("Rectangle")
        track.color = c.track
        track.width = 150
        track.height = 5
        track.translation = [x, y + 82]
        bar = rows.createChild("Rectangle")
        bar.color = c.accentStrong
        bar.width = 150 * (now - s) / (e - s)
        bar.height = 5
        bar.translation = [x, y + 82]
        lineX = x + 150 + 14
    end if
    if following <> invalid and following.title <> invalid
        nextLine = rows.createChild("Label")
        nextLine.text = "Next " + guideTime(secondsOf(following.startsAt), false) + "   " + following.title
        nextLine.width = g.x + g.width - 34 - lineX
        nextLine.translation = [lineX, y + 72]
        setType(nextLine, "help", c.textTertiary)
    end if
end sub

function pressKey(key as string) as boolean
    if key = "up" and m.focus > 0
        m.focus = m.focus - 1
    else if key = "down" and m.focus < m.top.lineup.count()
        m.focus = m.focus + 1
    else
        return false
    end if
    draw()
    return true
end function

function onKeyEvent(key as string, press as boolean) as boolean
    holdKey(key, press and (key = "up" or key = "down"))
    if not press then return true
    if key = "OK"
        if m.focus = m.top.lineup.count() then m.top.chosen = "guide" else m.top.chosen = m.top.lineup[m.focus].channel.id
    else if key = "back"
        m.top.dismissed = true
    else
        pressKey(key)
    end if
    return true     ' the panel holds focus until it closes
end function
