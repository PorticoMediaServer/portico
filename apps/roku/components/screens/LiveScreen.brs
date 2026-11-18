' The guide is one focus region (tv-focus 24 to 26): it starts on the programme on now on the first
' channel; Up and Down change channel keeping the time position; Left and Right walk the
' programmes; Left from the earliest programme reaches the channel cell, and Left again opens the
' rail. The time grid runs from the half hour before now; its lines are frames().guide. Up from the
' first channel reaches the filter chips (All and the channel groups), and Up again the toolbar:
' the channel source when there is more than one, the day shown (today or tomorrow, as the server
' reads a day of the guide at a time) and Now, back to what is on now. A chip or a choice reads the
' guide again with the server's own filter; focus stays where it was.
sub init()
    c = colours()
    m.g = frames().guide
    m.g.channelX = frames().gutter
    title = m.top.findNode("pageTitle")
    title.translation = [frames().gutter, frames().browse.titleY]
    setType(title, "title", c.text)
    state = m.top.findNode("state")
    state.translation = [frames().gutter, m.g.rowsY]
    state.width = 1200
    setType(state, "body", c.textSecondary)
    m.rows = m.top.findNode("rows")
    m.ring = m.top.findNode("ring")
    m.top.findNode("window").clippingRect = [frames().gutter - 40, m.g.rowsY - 30, 1920, 1080 - m.g.rowsY + 30]
    setType(m.top.findNode("nowText"), "keycap", c.actionInk)
    m.top.findNode("nowLine").color = c.accentStrong
    m.top.findNode("nowPill").blendColor = c.accent
    m.top.findNode("minute").observeField("fire", "drawNow")
    m.guide = []        ' [{channel, programmes}] from the server
    m.next = ""         ' the cursor of the guide's next page of channels, "" at the end
    m.paging = false    ' that next page has been asked for
    m.seq = 0           ' which read of the guide is current: a late answer to an older one is dropped
    m.r = 0             ' the channel in focus
    m.c = 0             ' the cell in focus in that row (0 is the channel itself)
    m.first = 0         ' the channel at the top of the screen
    m.day = 0           ' 0 today, 1 tomorrow
    m.region = "grid"   ' "bar" (the toolbar), "chips" or "grid"
    m.sources = []      ' the channel sources, with their groups
    m.filter = {source: "", group: ""}
    m.toolbar = m.top.findNode("toolbar")
    m.toolbar.translation = [m.g.toolbarX, m.g.toolbarY]
    m.toolbar.observeField("pressed", "onToolbar")
    m.chips = m.top.findNode("chips")
    m.chips.translation = [frames().gutter, m.g.chipsY]
    m.chips.observeField("pressed", "onChip")
    m.chips.observeField("focusedIndex", "slideChips")
    ' a long row of chips slides under this window, its focus ring kept whole
    m.top.findNode("chipWindow").clippingRect = [frames().gutter - 40, m.g.chipsY - 40, 1920 - frames().gutter + 40, 72 + 80]
    drawToolbar()
    m.top.findNode("minute").control = "start"
end sub

sub drawToolbar()
    day = "Today"
    if m.day = 1 then day = "Tomorrow"
    buttons = []
    for each id in liveToolbar(m.sources.count())
        if id = "source" then buttons.push({id: "source", text: sourceName(), icon: "", menu: true})
        if id = "day" then buttons.push({id: "day", text: day, icon: "", menu: true})
        if id = "now" then buttons.push({id: "now", text: "Now", icon: ""})
    end for
    m.toolbar.buttons = buttons
end sub

function sourceName() as string
    for each source in m.sources
        if source.id = m.filter.source then return source.name
    end for
    return "All channels"
end function

' All, then the groups of the channels shown; a group past the first few is behind More groups.
sub drawChips()
    groups = liveGroups(m.sources, m.filter.source, 8)
    m.moreGroups = groups.more
    chips = [{id: "all", text: "All", icon: "", switchedOn: m.filter.group = ""}]
    for each name in groups.shown
        chips.push({id: "g:" + name, text: name, icon: "", switchedOn: m.filter.group = name})
    end for
    if groups.more.count() > 0
        chosen = false
        for each name in groups.more
            if name = m.filter.group then chosen = true
        end for
        chips.push({id: "more", text: "More groups", icon: "", menu: true, switchedOn: chosen})
    end if
    if chips.count() = 1 then chips = []      ' All alone filters nothing
    m.chips.buttons = chips
    m.chips.visible = (chips.count() > 0)
    if not m.chips.visible and m.region = "chips" then focusRegion("bar")
end sub

' Keeps the chip in focus inside the window, sliding the row no further than it must.
sub slideChips()
    if m.chips.getChildCount() = 0 then return
    chip = m.chips.getChild(m.chips.focusedIndex)
    room = frames().rightEdge - frames().gutter
    x = m.chips.translation[0] - frames().gutter
    left = chip.translation[0] + x
    if left + chip.buttonWidth > room then x = room - chip.buttonWidth - chip.translation[0]
    if chip.translation[0] + x < 0 then x = -chip.translation[0]
    m.chips.translation = [frames().gutter + x, m.g.chipsY]
end sub

sub onActive()
    if not m.top.active then return
    focusRegion(m.region)
    ' read again when come back to after a while: what is on now has moved on
    if m.readAt = invalid or nowSeconds() - m.readAt > 60 then load()
end sub

' The guide's query: the day's three hours and the filter, with the cursor of a next page.
function guidePath(cursor as string) as string
    from = createObject("roDateTime")
    from.fromSeconds(guideStart(nowSeconds(), m.day))
    upTo = createObject("roDateTime")
    upTo.fromSeconds(guideStart(nowSeconds(), m.day) + 3 * 3600)
    path = "/v1/live/guide?limit=50&from=" + from.toISOString() + "&to=" + upTo.toISOString()
    escape = createObject("roUrlTransfer")
    if m.filter.source <> "" then path = path + "&source=" + escape.escape(m.filter.source)
    if m.filter.group <> "" then path = path + "&group=" + escape.escape(m.filter.group)
    if cursor <> "" then path = path + "&cursor=" + escape.escape(cursor)
    return path
end function

sub load()
    m.seq = m.seq + 1
    m.next = ""
    ' the sources and their groups come with the first page of channels
    api("GET", "/v1/live/channels?limit=1", invalid, "onSources")
    api("GET", guidePath(""), invalid, "onGuide", {seq: m.seq, more: false})
end sub

' The next page of channels, read as focus nears the end of those already here.
sub loadMore()
    if m.next = "" or m.paging = true then return
    m.paging = true
    api("GET", guidePath(m.next), invalid, "onGuide", {seq: m.seq, more: true})
end sub

sub onSources(event as object)
    result = event.getData()
    if not result.ok or result.data.sources = invalid then return
    m.sources = result.data.sources
    drawToolbar()
    drawChips()
end sub

sub onGuide(event as object)
    result = event.getData()
    ctx = event.getRoSGNode().ctx
    if ctx.seq <> m.seq then return
    m.paging = false
    state = m.top.findNode("state")
    if not result.ok
        if ctx.more then return     ' the channels already here stay; the next press asks again
        state.text = "The guide could not be loaded."
        state.visible = true
        return
    end if
    m.next = ""
    if result.data.nextCursor <> invalid then m.next = result.data.nextCursor
    if ctx.more
        m.guide.append(result.data.items)
        draw()
        if m.region = "grid" then paint()
        return
    end if
    keep = ""
    if m.guide.count() > m.r then keep = m.guide[m.r].channel.id
    m.guide = result.data.items
    m.readAt = nowSeconds()
    state.visible = (m.guide.count() = 0)
    state.text = "There are no channels to watch."
    m.r = 0
    for i = 0 to m.guide.count() - 1
        if m.guide[i].channel.id = keep then m.r = i
    end for
    ' the grid starts on the half hour before now; focus starts on what is on now
    m.start = guideStart(nowSeconds(), m.day)
    m.from = m.start
    m.time = nowSeconds()
    if m.day > 0 then m.time = m.start
    m.first = m.r
    draw()
    focusAt(m.time)
end sub

function nowSeconds() as integer
    return createObject("roDateTime").asSeconds()
end function

function secondsOf(iso as string) as integer
    t = createObject("roDateTime")
    t.fromISO8601String(iso)
    return t.asSeconds()
end function

' "3:30", with " PM" when asked for.
function clockOf(seconds as integer, suffix as boolean) as string
    t = createObject("roDateTime")
    t.fromSeconds(seconds)
    t.toLocalTime()
    h = t.getHours()
    half = "AM"
    if h >= 12 then half = "PM"
    h = h mod 12
    if h = 0 then h = 12
    mins = t.getMinutes()
    text = h.toStr() + ":"
    if mins < 10 then text = text + "0"
    text = text + mins.toStr()
    if suffix then text = text + " " + half
    return text
end function

function badgeText(name as string) as string
    out = ""
    for each word in name.trim().split(" ")
        if word <> "" and len(out) < 3 then out = out + ucase(left(word, 1))
    end for
    return out
end function

' Lays out the time header and every row from m.from; the cells of each row are kept with their
' times so focus can find them again.
sub draw()
    c = colours()
    g = m.g
    header = m.top.findNode("header")
    header.removeChildrenIndex(header.getChildCount(), 0)
    for i = 0 to 5
        at = m.from + i * 1800
        label = header.createChild("Label")
        label.text = clockOf(at, i = 0)
        label.translation = [g.gridX + i * 30 * g.perMinute + 12, g.headerY]
        setType(label, "metaStrong", c.textSecondary)
        tick = header.createChild("Rectangle")
        tick.width = 2
        tick.height = 30
        tick.color = c.line
        tick.translation = [g.gridX + i * 30 * g.perMinute, g.headerY]
    end for
    m.rows.removeChildrenIndex(m.rows.getChildCount(), 0)
    m.cells = []
    palette = c.channelTints
    for r = 0 to m.guide.count() - 1
        row = m.guide[r]
        y = r * g.pitch
        cells = []
        ' the channel cell
        plate = cellPlate(g.channelX, y, g.channelWidth)
        badge = m.rows.createChild("Poster")
        badge.uri = "pkg:/images/round-14.9.png"
        badge.blendColor = palette[coverTint(row.channel.name)]
        badge.width = 56
        badge.height = 56
        badge.translation = [g.channelX + 16, y + 17]
        initials = m.rows.createChild("Label")
        initials.text = badgeText(row.channel.name)
        initials.width = 56
        initials.height = 56
        initials.horizAlign = "center"
        initials.vertAlign = "center"
        initials.translation = [g.channelX + 16, y + 17]
        setType(initials, "keycap", c.text)
        name = m.rows.createChild("Label")
        name.text = row.channel.name
        name.width = g.channelWidth - 104
        name.translation = [g.channelX + 88, y + 14]
        setType(name, "listLabel", c.text)
        number = m.rows.createChild("Label")
        number.text = row.channel.number
        number.translation = [g.channelX + 88, y + 48]
        setType(number, "help", c.textSecondary)
        cells.push({x: g.channelX, width: g.channelWidth, plate: plate, start: 0, ends: 0, programme: invalid})
        ' the programmes from the window's start
        for each p in row.programmes
            s = secondsOf(p.startsAt)
            e = secondsOf(p.endsAt)
            span = guideSpan(s, e, m.from, g)
            if span <> invalid then cells.push(programmeCell(p, s, e, span.x, y, span.width, span.earlier))
        end for
        m.cells.push(cells)
    end for
    drawNow()
end sub

function cellPlate(x as float, y as float, width as float) as object
    plate = m.rows.createChild("Poster")
    plate.uri = "pkg:/images/round-14.9.png"
    plate.blendColor = colours().raised
    plate.width = width
    plate.height = m.g.cell
    plate.translation = [x, y]
    return plate
end function

' One programme: its title on one line, then its times; what is on now says Live and shows how far in it is.
function programmeCell(p as object, s as integer, e as integer, x as float, y as float, width as float, earlier as boolean) as object
    c = colours()
    now = nowSeconds()
    plate = cellPlate(x, y, width)
    inset = 18
    if earlier
        ' it began before the grid's first half hour
        mark = m.rows.createChild("Label")
        mark.text = "<"
        mark.translation = [x + 10, y + 12]
        setType(mark, "help", c.textTertiary)
        inset = 30
    end if
    title = m.rows.createChild("Label")
    title.text = p.title
    title.width = width - inset - 12
    title.translation = [x + inset, y + 12]
    setType(title, "listLabel", c.text)
    times = clockOf(s, false) + " to " + clockOf(e, false)
    tx = x + inset
    if s <= now and now < e
        dot = m.rows.createChild("Poster")
        dot.uri = "pkg:/images/disc.png"
        dot.blendColor = c.danger
        dot.width = 10
        dot.height = 10
        dot.translation = [tx, y + 59]
        live = m.rows.createChild("Label")
        live.text = "Live"
        live.translation = [tx + 16, y + 50]
        setType(live, "help", c.danger)
        tx = tx + 16 + live.boundingRect().width + 10
        track = m.rows.createChild("Rectangle")
        track.color = c.accent
        track.height = 4
        track.width = width * (now - s) / (e - s)
        if x + track.width > 1920 then track.width = 1920 - x
        track.translation = [x, y + m.g.cell - 4]
    end if
    label = m.rows.createChild("Label")
    label.text = times
    label.width = x + width - tx - 12
    label.translation = [tx, y + 50]
    setType(label, "help", c.textSecondary)
    return {x: x, width: width, plate: plate, start: s, ends: e, programme: p}
end function

sub drawNow()
    g = m.g
    if m.from = invalid then return
    now = nowSeconds()
    x = g.gridX + (now - m.from) / 60 * g.perMinute
    line = m.top.findNode("nowLine")
    pill = m.top.findNode("nowPill")
    text = m.top.findNode("nowText")
    showing = (x >= g.gridX and x < 1920)
    line.visible = showing
    pill.visible = showing
    text.visible = showing
    if not showing then return
    line.translation = [x - 1, g.headerY + 34]
    line.height = 1080 - g.headerY - 34
    text.text = clockOf(now, false)
    w = text.boundingRect().width + 20
    pill.width = w
    pill.translation = [x - w / 2, g.headerY - 2]
    text.width = w
    text.translation = [x - w / 2, g.headerY - 2]
    ' a half hour's label under the now pill steps aside for it
    header = m.top.findNode("header")
    for i = 0 to header.getChildCount() - 1
        label = header.getChild(i)
        if label.subtype() = "Label"
            lx = label.translation[0]
            label.visible = (lx + label.boundingRect().width < x - w / 2 - 8 or lx > x + w / 2 + 8)
        end if
    end for
end sub

' Focus on the cell in this row that is on at `at` (the time position kept across channels).
sub focusAt(at as integer)
    if m.cells = invalid or m.cells.count() = 0 then return
    cells = m.cells[m.r]
    m.c = 0
    for i = 1 to cells.count() - 1
        if cells[i].start <= at and at < cells[i].ends then m.c = i
    end for
    if m.c = 0 and cells.count() > 1 and at >= m.from then m.c = 1
    if m.region = "grid" then paint()
end sub

sub paint()
    if m.cells = invalid or m.cells.count() = 0 then return
    c = colours()
    g = m.g
    ' keep the row in focus on the screen
    if m.r < m.first then m.first = m.r
    if m.r > m.first + g.shown - 1 then m.first = m.r - g.shown + 1
    m.rows.translation = [0, g.rowsY - m.first * g.pitch]
    for r = 0 to m.cells.count() - 1
        for i = 0 to m.cells[r].count() - 1
            m.cells[r][i].plate.blendColor = c.raised
        end for
    end for
    cell = m.cells[m.r][m.c]
    cell.plate.blendColor = c.raisedBright
    out = metrics().focusSpread + 6
    m.ring.translation = [cell.x - out, g.rowsY + (m.r - m.first) * g.pitch - out]
    m.ring.width = cell.width + out * 2
    if cell.x + cell.width > 1920 then m.ring.width = 1920 - cell.x + out
    m.ring.height = g.cell + out * 2
    m.ring.visible = true
end sub

function pressKey(key as string) as boolean
    if m.cells = invalid or m.cells.count() = 0 then return false
    if key = "up" or key = "down"
        if key = "up" and m.r = 0
            if m.chips.visible then focusRegion("chips") else focusRegion("bar")
            return true
        end if
        if key = "down" and m.r = m.cells.count() - 1 then return false
        if key = "up" then m.r = m.r - 1 else m.r = m.r + 1
        ' the next page of channels is read before focus reaches the last one here
        if m.r >= m.cells.count() - 5 then loadMore()
        if m.c = 0 then paint() else focusAt(m.time)
        return true
    end if
    cells = m.cells[m.r]
    if key = "left"
        if m.c = 0 then return false              ' the channel cell: Left opens the rail
        if m.c = 1 and m.from > m.start
            shift(-1800)
            return true
        end if
        m.c = m.c - 1
    else if key = "right"
        if m.c = cells.count() - 1 then return false
        m.c = m.c + 1
        ' a programme starting past the right edge brings the next half hour in
        if cells[m.c].x + 300 > 1920 and m.from + 1800 < m.start + 3 * 3600
            m.time = cells[m.c].start
            shift(1800)
            return true
        end if
    else
        return false
    end if
    if m.c > 0
        m.time = cells[m.c].start
        if m.time < m.from then m.time = m.from
    end if
    paint()
    return true
end function

' Moves focus to the toolbar, the chips or the guide; the row that loses it draws its focus off.
sub focusRegion(region as string)
    m.region = region
    m.toolbar.active = (region = "bar")
    m.chips.active = (region = "chips")
    if region = "grid"
        m.rows.setFocus(true)
        paint()
        return
    end if
    m.ring.visible = false
    if m.cells <> invalid and m.cells.count() > 0 then m.cells[m.r][m.c].plate.blendColor = colours().raised
end sub

sub onToolbar(event as object)
    which = event.getData()
    if which = "now"
        focusRegion("grid")
        toStart()
    else if which = "source"
        rows = [{id: "source:", text: "All channels", current: m.filter.source = ""}]
        for each source in m.sources
            rows.push({id: "source:" + source.id, text: source.name, current: m.filter.source = source.id})
        end for
        m.top.menu = {title: "Channels", rows: rows}
    else
        m.top.menu = {title: "Day", rows: [{id: "day:0", text: "Today", current: m.day = 0}, {id: "day:1", text: "Tomorrow", current: m.day = 1}]}
    end if
end sub

sub onChip(event as object)
    which = event.getData()
    if which = "more"
        rows = []
        for each name in m.moreGroups
            rows.push({id: "group:" + name, text: name, current: m.filter.group = name})
        end for
        m.top.menu = {title: "Channel groups", rows: rows}
        return
    end if
    group = ""
    if left(which, 2) = "g:" then group = mid(which, 3)
    choose({source: m.filter.source, group: group})
end sub

' A new filter: the chips show it at once, and the guide is read again from its first channel.
sub choose(filter as object)
    m.filter = filter
    drawToolbar()
    drawChips()
    m.r = 0
    m.first = 0
    load()
end sub

function atStart() as boolean
    if m.day <> 0 then return false
    if m.cells = invalid or m.cells.count() = 0 then return true
    now = nowSeconds()
    cell = m.cells[0][m.c]
    return m.r = 0 and m.from = m.start and m.c > 0 and cell.start <= now and now < cell.ends
end function

sub toStart()
    if m.day <> 0
        m.day = 0
        drawToolbar()
        m.r = 0
        load()
        return
    end if
    m.r = 0
    m.first = 0
    if m.from <> m.start
        m.from = m.start
        draw()
    end if
    m.time = nowSeconds()
    focusAt(m.time)
end sub

' Moves the grid's window by half an hour and keeps focus on the same time.
sub shift(by as integer)
    m.from = m.from + by
    if m.from < m.start then m.from = m.start
    if m.c > 0 and m.time < m.from then m.time = m.from
    draw()
    focusAt(m.time)
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    holdKey(key, press)
    if not press then return false
    if m.region <> "grid"
        ' the toolbar or the chips gave the key up: Down goes on towards the guide, Up towards the
        ' toolbar, Back returns to the guide's start; Left at their start opens the rail
        if key = "back"
            focusRegion("grid")
            toStart()
            return true
        end if
        if key = "down"
            if m.region = "bar" and m.chips.visible then focusRegion("chips") else focusRegion("grid")
            return true
        end if
        if key = "up" and m.region = "chips"
            focusRegion("bar")
            return true
        end if
        return false
    end if
    if key = "back"
        ' Back, step by step: first to the guide's start (what is on now on the first channel)
        if atStart() then return false
        toStart()
        return true
    end if
    if key = "OK"
        openSheet()
        return true
    end if
    return pressKey(key)
end function

' The programme's side sheet: Watch live first. The channel cell opens what is on now.
sub openSheet()
    if m.cells = invalid or m.cells.count() = 0 then return
    row = m.guide[m.r]
    cell = m.cells[m.r][m.c]
    if cell.programme = invalid
        now = nowSeconds()
        for each other in m.cells[m.r]
            if other.programme <> invalid and other.start <= now and now < other.ends then cell = other
        end for
    end if
    model = {eyebrow: row.channel.name + "   " + row.channel.number, title: row.channel.name, status: "", rows: [{id: "watch", text: "Watch live", icon: "play"}]}
    if cell.programme <> invalid
        p = cell.programme
        model.title = p.title
        status = clockOf(cell.start, false) + " to " + clockOf(cell.ends, true)
        minsLeft = int((cell.ends - nowSeconds()) / 60)
        if cell.start <= nowSeconds() and minsLeft > 0 then status = status + "   " + durationText(minsLeft * 60000) + " left"
        model.status = status
        if p.summary <> invalid then model.body = p.summary
    end if
    m.top.menu = model
end sub

sub onMenuChosen(event as object)
    chosen = event.getData()
    if left(chosen, 4) = "day:"
        m.day = mid(chosen, 5).toInt()
        drawToolbar()
        m.r = 0
        focusRegion("grid")
        load()
        return
    end if
    if left(chosen, 7) = "source:"
        choose({source: mid(chosen, 8), group: ""})
        return
    end if
    if left(chosen, 6) = "group:"
        choose({source: m.filter.source, group: mid(chosen, 7)})
        return
    end if
    if chosen <> "watch" then return
    row = m.guide[m.r]
    ' the player gets the channel and the whole lineup, for channel up and down
    m.top.open = {play: true, item: {kind: "channel", id: row.channel.id, title: row.channel.name, channel: row.channel, programmes: row.programmes, lineup: m.guide}}
end sub
