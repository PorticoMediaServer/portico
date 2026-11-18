' The guide is one focus region (tv-focus 24 to 26): it starts on the programme on now on the first
' channel; Up and Down change channel keeping the time position; Left and Right walk the
' programmes; Left from the earliest programme reaches the channel cell, and Left again opens the
' rail. The time grid runs from the half hour before now; its lines are frames().guide. Up from the
' first channel reaches the filter chips (All, Favorites and the channel groups), and Up again the toolbar:
' the channel source when there is more than one, the day shown (today or tomorrow, as the server
' reads a day of the guide at a time), Now, back to what is on now, and Recordings. A chip or a
' choice reads the guide again with the server's own filter; focus stays where it was. A programme
' booked to record carries the red dot; its sheet records it, records the series, or cancels.
sub init()
    c = colours()
    m.g = frames().guide
    m.g.channelX = frames().gutter
    title = m.top.findNode("pageTitle")
    title.translation = [frames().gutter, frames().browse.titleY]
    setType(title, "title", c.text)
    m.page = m.top.findNode("page")
    m.page.observeField("pressed", "onPagePressed")
    m.empty = m.top.findNode("empty")
    m.empty.translation = [frames().gutter, m.g.rowsY]
    m.empty.width = frames().rightEdge - frames().gutter
    m.empty.observeField("pressed", "showAll")
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
    m.bookingSeq = 0
    m.seq = 0           ' which read of the guide is current: a late answer to an older one is dropped
    m.r = 0             ' the channel in focus
    m.c = 0             ' the cell in focus in that row (0 is the channel itself)
    m.first = 0         ' the channel at the top of the screen
    m.day = 0           ' 0 today, 1 tomorrow
    m.region = "grid"   ' "bar" (the toolbar), "chips", "grid", "empty" (a filter's empty row) or "page" (the page's state)
    m.sources = []      ' the channel sources, with their groups
    m.filter = {source: "", group: "", favorites: false}
    m.booked = {}       ' the profile's bookings by bookingKey (source/Live.brs)
    m.reminded = {}     ' the programmes this profile asked to be reminded of, by id
    m.sheetLevel = ""   ' which sheet is open: "programme", "series", "notice", or "" for none
    m.canRecord = false ' this profile may record (the server's DVR overview)
    m.hasRecordings = false
    m.confirm = m.top.findNode("confirm")
    m.confirm.observeField("chosen", "onConfirm")
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
    for each id in liveToolbar(m.sources.count(), m.canRecord or m.hasRecordings)
        if id = "source" then buttons.push({id: "source", text: sourceName(), icon: "", menu: true})
        if id = "day" then buttons.push({id: "day", text: day, icon: "", menu: true})
        if id = "now" then buttons.push({id: "now", text: "Now", icon: ""})
        if id = "recordings" then buttons.push({id: "recordings", text: "Recordings", icon: "dvr"})
    end for
    m.toolbar.buttons = buttons
end sub

function sourceName() as string
    for each source in m.sources
        if source.id = m.filter.source then return source.name
    end for
    return "All channels"
end function

' All, Favorites, then the groups of the channels shown; a group past the first few is behind More groups.
sub drawChips()
    groups = liveGroups(m.sources, m.filter.source, 8)
    m.moreGroups = groups.more
    chips = []
    if m.sources.count() > 0
        chips.push({id: "all", text: "All", icon: "", switchedOn: m.filter.group = "" and not m.filter.favorites})
        chips.push({id: "fav", text: "Favorites", icon: "", switchedOn: m.filter.favorites})
    end if
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
    if m.readAt = invalid or nowSeconds() - m.readAt > 60
        load()
    else
        loadBookings()      ' Recordings may have cancelled one
    end if
end sub

' Whether this profile may record, and what it has booked: the red dots and the sheet's rows.
sub loadBookings()
    m.bookingSeq = m.bookingSeq + 1
    m.bookings = []
    api("GET", "/v1/dvr", invalid, "onDvr")
    api("GET", "/v1/dvr/recordings", invalid, "onBookings", {seq: m.bookingSeq})
    api("GET", "/v1/live/reminders", invalid, "onReminders", {seq: m.bookingSeq, ids: {}})
end sub

' The programmes this profile asked to be reminded of, by programme id (every page).
sub onReminders(event as object)
    result = event.getData()
    ctx = event.getRoSGNode().ctx
    if ctx.seq <> m.bookingSeq or not result.ok then return
    ids = ctx.ids
    for each r in result.data.items
        ids[r.programme] = true
    end for
    if result.data.nextCursor <> invalid and result.data.nextCursor <> ""
        api("GET", "/v1/live/reminders?cursor=" + result.data.nextCursor.encodeUriComponent(), invalid, "onReminders", {seq: ctx.seq, ids: ids})
        return
    end if
    m.reminded = ids
end sub

sub onDvr(event as object)
    result = event.getData()
    if not result.ok then return
    m.canRecord = (result.data.canRecord = true)
    m.hasRecordings = (result.data.upcoming + result.data.recorded > 0)
    drawToolbar()
end sub

' Every page of the bookings (a series rule can book many), then the dots are drawn once.
sub onBookings(event as object)
    result = event.getData()
    ctx = event.getRoSGNode().ctx
    if ctx.seq <> m.bookingSeq or not result.ok then return
    for each r in result.data.items
        channelId = ""
        if r.channelId <> invalid then channelId = r.channelId
        m.bookings.push({channelId: channelId, starts: secondsOf(r.startsAt), id: r.id, state: r.state, mayCancel: r.mayCancel = true, mayChange: r.mayChange = true})
    end for
    if result.data.nextCursor <> invalid and result.data.nextCursor <> ""
        api("GET", "/v1/dvr/recordings?cursor=" + result.data.nextCursor.encodeUriComponent(), invalid, "onBookings", ctx)
        return
    end if
    m.booked = liveBookings(m.bookings)
    if m.reopen <> invalid and m.reopen <> "" and m.sheetLevel <> ""
        showSheet(m.reopen)
        m.reopen = ""
    end if
    if m.cells = invalid then return
    draw()
    if m.region = "grid" then paint()
end sub

' The guide's query: the day's three hours and the filter, with the cursor of a next page.
function guidePath(cursor as string) as string
    from = createObject("roDateTime")
    from.fromSeconds(guideStart(nowSeconds(), m.day))
    upTo = createObject("roDateTime")
    upTo.fromSeconds(guideStart(nowSeconds(), m.day) + 3 * 3600)
    path = "/v1/live/guide?limit=50&from=" + from.toISOString() + "&to=" + upTo.toISOString()
    if m.filter.source <> "" then path = path + "&source=" + m.filter.source.encodeUriComponent()
    if m.filter.group <> "" then path = path + "&group=" + m.filter.group.encodeUriComponent()
    if m.filter.favorites then path = path + "&favorites=true"
    if cursor <> "" then path = path + "&cursor=" + cursor.encodeUriComponent()
    return path
end function

sub load()
    m.seq = m.seq + 1
    m.next = ""
    m.paging = false
    loadBookings()
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
    if not result.ok
        if ctx.more then return     ' the channels already here stay; the next press asks again
        m.guide = []
        m.start = guideStart(nowSeconds(), m.day)
        m.from = m.start
        draw()
        m.page.icon = "warning"
        m.page.title = "Couldn't load the guide"
        m.page.body = whyFailed(result)
        m.page.buttons = [{id: "retry", text: "Try again", icon: "refresh"}]
        m.page.visible = true
        focusRegion("page")
        return
    end if
    m.page.visible = false
    if m.region = "page" then m.region = "grid"
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
    showEmpty()
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

' With no channels: a filter that leaves none says so in the grid's first row, with the way back
' to every channel; with none at all the page says so (the owner adds them).
sub showEmpty()
    empty = guideEmpty(m.guide.count(), m.filter)
    m.empty.visible = (empty = "filter")
    if m.empty.visible
        if m.filter.favorites
            m.empty.title = "No channels in Favorites"
            m.empty.reason = "Add a channel to Favorites from its sheet in the guide."
        else
            m.empty.title = "No channels to show here"
            m.empty.reason = "None of the channels you can watch is in " + filterName() + "."
        end if
        m.empty.action = "Show all channels"
        ' focus in the emptied guide goes to the row in its place; on the chips it stays there
        if m.region = "grid" then focusRegion("grid")
    else if m.region = "empty"
        focusRegion("grid")
    end if
    if empty = "none"
        m.page.icon = ""
        m.page.title = "No channels to watch"
        m.page.body = "When the server’s owner adds a TV source or a Library Channel, its channels show here."
        m.page.buttons = []
        m.page.visible = true
    end if
end sub

function filterName() as string
    if m.filter.group <> "" then return m.filter.group
    return sourceName()
end function

sub showAll()
    choose({source: "", group: "", favorites: false})
    focusRegion("chips")
end sub

sub onPagePressed()
    m.page.visible = false
    focusRegion("grid")
    load()
end sub

function nowSeconds() as integer
    return createObject("roDateTime").asSeconds()
end function

' Lays out the time header and every row from m.from; the cells of each row are kept with their
' times so focus can find them again.
sub draw()
    c = colours()
    g = m.g
    header = m.top.findNode("header")
    header.removeChildrenIndex(header.getChildCount(), 0)
    header.visible = (m.guide.count() > 0)
    for i = 0 to 5
        at = m.from + i * 1800
        label = header.createChild("Label")
        label.text = guideTime(at, i = 0)
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
    for r = 0 to m.guide.count() - 1
        row = m.guide[r]
        y = r * g.pitch
        cells = []
        ' the channel cell
        plate = cellPlate(g.channelX, y, g.channelWidth)
        badge = m.rows.createChild("ChannelMark")
        badge.channel = row.channel
        badge.translation = [g.channelX + 16, y + 17]
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
            if span <> invalid then cells.push(programmeCell(p, row.channel.id, s, e, span.x, y, span.width, span.earlier))
        end for
        if cells.count() = 1 then cells.push(noGuideCell(y))
        m.cells.push(cells)
    end for
    drawNow()
end sub

' A channel with nothing in the guide for these hours: one cell across the grid that says so; OK
' on it opens the channel's sheet, to watch it live.
function noGuideCell(y as float) as object
    width = 1920 - m.g.gridX
    plate = cellPlate(m.g.gridX, y, width)
    label = m.rows.createChild("Label")
    label.text = "No guide information for these hours"
    label.height = m.g.cell
    label.vertAlign = "center"
    label.translation = [m.g.gridX + 18, y]
    setType(label, "help", colours().textTertiary)
    return {x: m.g.gridX, width: width, plate: plate, start: m.from, ends: m.start + 3 * 3600, programme: invalid}
end function

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
function programmeCell(p as object, channelId as string, s as integer, e as integer, x as float, y as float, width as float, earlier as boolean) as object
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
    times = guideTime(s, false) + " to " + guideTime(e, false)
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
    if m.booked[bookingKey(channelId, s)] <> invalid
        ' booked to record: the red dot before its times
        booked = m.rows.createChild("Poster")
        booked.uri = "pkg:/images/disc.png"
        booked.blendColor = c.danger
        booked.width = 12
        booked.height = 12
        booked.translation = [tx, y + 58]
        tx = tx + 12 + 10
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
    showing = (x >= g.gridX and x < 1920 and m.guide.count() > 0)
    line.visible = showing
    pill.visible = showing
    text.visible = showing
    if not showing then return
    line.translation = [x - 1, g.headerY + 34]
    line.height = 1080 - g.headerY - 34
    text.text = guideTime(now, false)
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
    if m.cells = invalid or m.cells.count() = 0
        m.ring.visible = false
        return
    end if
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
            focusRegion(above())
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
    if region = "grid" and m.empty.visible then region = "empty"     ' the row in the emptied guide's place
    m.region = region
    ' covered by another page (the player over the guide), the guide keeps its place but takes no
    ' focus: a late answer (the guide failing, a filter left empty) never pulls the keys from what
    ' is in front. It takes focus again when it is shown and made active.
    if not m.top.visible then return
    m.toolbar.active = (region = "bar")
    m.chips.active = (region = "chips")
    m.empty.active = (region = "empty")
    m.page.active = (region = "page")
    if region = "grid"
        m.rows.setFocus(true)
        paint()
        return
    end if
    m.ring.visible = false
    if m.cells <> invalid and m.cells.count() > 0 then m.cells[m.r][m.c].plate.blendColor = colours().raised
end sub

' What is under the chips: a filter's empty row, the page's Try again, or the guide.
function belowChips() as string
    if m.empty.visible then return "empty"
    if m.page.visible and m.page.buttons.count() > 0 then return "page"
    return "grid"
end function

function above() as string
    if m.chips.visible then return "chips"
    return "bar"
end function

sub onToolbar(event as object)
    which = event.getData()
    if which = "now"
        focusRegion("grid")
        toStart()
    else if which = "recordings"
        m.top.open = {item: {kind: "recordings", title: "Recordings"}}
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
    choose({source: m.filter.source, group: group, favorites: which = "fav"})
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
        if m.region = "page" or m.region = "empty"
            ' Try again or Show all channels, in the guide's place: Up returns to the filters; Back
            ' and Left go on to the scene
            if key = "up" then focusRegion(above())
            return key = "up"
        end if
        if key = "back"
            focusRegion("grid")
            toStart()
            return true
        end if
        if key = "down"
            if m.region = "bar" and m.chips.visible then focusRegion("chips") else focusRegion(belowChips())
            return true
        end if
        if key = "up" and m.region = "chips"
            focusRegion("bar")
            return true
        end if
        return false
    end if
    if key = "up" and (m.cells = invalid or m.cells.count() = 0)
        focusRegion(above())
        return true
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

' The programme's side sheet. The channel cell opens what is on now.
sub openSheet()
    if m.cells = invalid or m.cells.count() = 0 then return
    row = m.guide[m.r]
    m.sheetRow = row       ' what the sheet's choices act on
    cell = m.cells[m.r][m.c]
    if cell.programme = invalid
        now = nowSeconds()
        for each other in m.cells[m.r]
            if other.programme <> invalid and other.start <= now and now < other.ends then cell = other
        end for
    end if
    m.sheetCell = cell
    showSheet("")
end sub

' The sheet as things stand, focus on the row `focusId` ("" for its first) (tv-focus 37 to 47):
' Watch live first, except for a programme still to come, which starts on Record; then Record
' series…, Remind me, View recordings, the title's page and Favorites, each where it applies.
' Record, Remind me and Favorites keep the sheet open: it is shown again with their new state.
sub showSheet(focusId as string)
    row = m.sheetRow
    cell = m.sheetCell
    p = cell.programme
    now = nowSeconds()
    model = programmeSheet(row, p, now)
    m.sheetLevel = "programme"
    if p = invalid or cell.start <= now
        watch = "Watch channel"
        if model.live or p = invalid then watch = "Watch live"
        model.rows.push({id: "watch", text: watch, icon: "play"})
    end if
    booking = invalid
    ' Record and Record series: a source's channel (a Library Channel plays the library), a
    ' programme the profile may see that has not ended, and a profile allowed to record
    if m.canRecord and row.channel.kind = "source" and p <> invalid and p.notAvailable <> true and cell.ends > now
        booking = m.booked[bookingKey(row.channel.id, cell.start)]
        if booking = invalid
            model.rows.push({id: "record", text: "Record", dot: true, stays: true})
        else if booking.state = "scheduled"
            ' booked: the row reads Recording (F-GUIDE-45); OK cancels it, after asking
            if booking.mayCancel then model.rows.push({id: "record", text: "Recording", detail: "Scheduled. OK cancels it.", dot: true}) else model.rows.push({id: "recordings", text: "Recording", detail: "Scheduled. OK opens Recordings.", dot: true})
        else
            if booking.mayChange then model.rows.push({id: "record", text: "Recording", detail: "Recording now. OK stops it.", dot: true}) else model.rows.push({id: "recordings", text: "Recording", detail: "Recording now. OK opens Recordings.", dot: true})
        end if
        model.rows.push({id: "series", text: "Record series…", icon: "repeat"})
    end if
    ' Remind me: a programme still to come; the note shows two minutes before, while the app is open
    if p <> invalid and p.notAvailable <> true and cell.start > now
        if m.reminded[p.id] = true
            model.rows.push({id: "remind", text: "Stop reminding", detail: "A note shows 2 minutes before it starts.", icon: "bell", stays: true})
        else
            model.rows.push({id: "remind", text: "Remind me", icon: "bell", stays: true})
        end if
    end if
    if booking <> invalid then model.rows.push({id: "recordings", text: "View recordings", icon: "dvr"})
    ' a Library Channel's programme is a title of the library: its page is a press away
    if p <> invalid and p.itemId <> invalid and p.itemId <> ""
        if p.episode <> invalid then model.rows.push({id: "title", text: "Go to show", icon: "info"}) else model.rows.push({id: "title", text: "Go to title", icon: "info"})
    end if
    favorite = favoriteRow(row.channel)
    favorite.stays = true
    model.rows.push(favorite)
    for each r in model.rows
        if r.id = focusId then r.focus = true
    end for
    m.sheetFocus = focusId
    m.top.menu = model
end sub

' Back from Record series, or from the server's refusal, returns to the row that opened it
' (F-GUIDE-46); Back from the programme's sheet returns to the guide.
sub onMenuClosed()
    if m.sheetLevel = "series" or m.sheetLevel = "notice" then showSheet(m.sheetFocus) else m.sheetLevel = ""
end sub

' A sheet that says why the server would not do something; OK or Back returns to the programme's.
sub refused(message as string)
    m.sheetLevel = "notice"
    m.top.menu = notRecordedSheet(message)
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
        choose({source: mid(chosen, 8), group: "", favorites: m.filter.favorites})
        return
    end if
    if left(chosen, 6) = "group:"
        choose({source: m.filter.source, group: mid(chosen, 7), favorites: false})
        return
    end if
    if chosen = "ok"
        showSheet(m.sheetFocus)
        return
    end if
    if chosen = "favorite"
        ' the profile's own mark: the channel's hidden mark is sent back as it is
        channel = m.sheetRow.channel
        api("PUT", "/v1/live/channels/" + channel.id + "/state", {favorite: not channel.favorite, hidden: channel.hidden}, "onFavorite", {id: channel.id, seq: m.seq})
        return
    end if
    if chosen = "remind"
        p = m.sheetCell.programme
        method = "PUT"
        if m.reminded[p.id] = true then method = "DELETE"
        api(method, "/v1/live/reminders/" + p.id.encodeUriComponent(), invalid, "onRemind", {id: p.id, on: method = "PUT", seq: m.seq})
        return
    end if
    if chosen = "record"
        booking = m.booked[bookingKey(m.sheetRow.channel.id, m.sheetCell.start)]
        p = m.sheetCell.programme
        if booking = invalid
            api("POST", "/v1/dvr/recordings", {programme: p.id}, "onRecordAnswer", {seq: m.seq, focus: "record"})
        else
            m.confirm.model = recordingConfirm(p.title, booking)
        end if
        return
    end if
    if chosen = "series"
        ' what a rule for this programme would record, before it is made
        m.sheetFocus = "series"
        api("POST", "/v1/dvr/rules?preview=true", {programme: m.sheetCell.programme.id}, "onSeriesPreview", {seq: m.seq})
        return
    end if
    if chosen = "rule:this" or chosen = "rule:any"
        api("POST", "/v1/dvr/rules", {programme: m.sheetCell.programme.id, anyChannel: chosen = "rule:any"}, "onRecordAnswer", {seq: m.seq, focus: "series"})
        return
    end if
    m.sheetLevel = ""
    if chosen = "recordings"
        m.top.open = {item: {kind: "recordings", title: "Recordings"}}
        return
    end if
    if chosen = "title"
        api("GET", "/v1/items/" + m.sheetCell.programme.itemId, invalid, "onTitle")
        return
    end if
    if chosen <> "watch" then return
    row = m.sheetRow
    ' the player gets the channel and the whole lineup, for channel up and down
    m.top.open = {play: true, item: {kind: "channel", id: row.channel.id, title: row.channel.name, channel: row.channel, programmes: row.programmes, lineup: m.guide}}
end sub

' The mark is the server's now, and the sheet shows it. In the Favorites view a channel taken out
' leaves it: the guide is read again.
sub onFavorite(event as object)
    result = event.getData()
    ctx = event.getRoSGNode().ctx
    if not result.ok or ctx.seq <> m.seq then return
    for each row in m.guide
        if row.channel.id = ctx.id then row.channel.favorite = result.data.favorite
    end for
    m.sheetRow.channel.favorite = result.data.favorite
    if m.sheetLevel = "programme" then showSheet("favorite")
    if m.filter.favorites and not result.data.favorite then load()
end sub

sub onRemind(event as object)
    result = event.getData()
    ctx = event.getRoSGNode().ctx
    if ctx.seq <> m.seq then return
    if not result.ok
        if m.sheetLevel = "programme" then refused(result.error.message)
        return
    end if
    if ctx.on then m.reminded[ctx.id] = true else m.reminded.delete(ctx.id)
    ' the scene's note for it is set (or taken away): it counts this and reads the reminders again
    if m.global.hasField("remindersChanged") then m.global.remindersChanged = m.global.remindersChanged + 1
    if m.sheetLevel = "programme" then showSheet("remind")
end sub

' A booking, a series rule or a cancel done: the red dots are read again, and the sheet comes back
' on the row it was chosen from once they are. One the server refused says why.
sub onRecordAnswer(event as object)
    result = event.getData()
    ctx = event.getRoSGNode().ctx
    if ctx.seq <> m.seq then return
    if not result.ok
        refused(result.error.message)
        return
    end if
    m.reopen = ctx.focus
    loadBookings()
end sub

' Record series: the rule's channel choice, once the server has said what it would record.
sub onSeriesPreview(event as object)
    result = event.getData()
    if event.getRoSGNode().ctx.seq <> m.seq then return
    if not result.ok
        refused(result.error.message)
        return
    end if
    m.sheetLevel = "series"
    m.top.menu = seriesSheet(result.data, m.sheetRow.channel.name, m.sheetCell.programme.title)
end sub

' Cancel or stop, asked over the sheet: either way the sheet comes back on Recording.
sub onConfirm()
    focusRegion(m.region)
    booking = m.booked[bookingKey(m.sheetRow.channel.id, m.sheetCell.start)]
    if m.confirm.chosen <> "confirm" or booking = invalid
        showSheet("record")
        return
    end if
    api("DELETE", recordingCancelPath(booking), invalid, "onRecordAnswer", {seq: m.seq, focus: "record"})
end sub

' An episode opens its show's page; anything else, its own.
sub onTitle(event as object)
    result = event.getData()
    if not result.ok then return
    item = result.data
    if item.kind = "episode" and item.rootId <> invalid then item = {id: item.rootId, kind: "show", title: item.rootTitle, libraryId: item.libraryId}
    m.top.open = {item: item}
end sub
