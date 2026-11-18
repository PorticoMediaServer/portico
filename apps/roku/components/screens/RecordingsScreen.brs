' Two sections from the server's own lists: Scheduled (GET /v1/dvr/recordings, every page, soonest
' first) and Recorded (?state=recorded, read a page at a time as focus nears its end). Up and Down
' move through the rows of both; the section in view scrolls with focus.
sub init()
    c = colours()
    m.f = frames().recordings
    title = m.top.findNode("pageTitle")
    title.translation = [frames().gutter, frames().browse.titleY]
    setType(title, "title", c.text)
    m.list = m.top.findNode("list")
    m.top.findNode("box").clippingRect = [frames().gutter - 40, m.f.listY - m.f.headingGap - 10, m.f.width + 80, m.f.bottom - m.f.listY + m.f.headingGap + 10]
    m.page = m.top.findNode("page")
    m.page.observeField("pressed", "onPagePressed")
    m.confirm = m.top.findNode("confirm")
    m.confirm.observeField("chosen", "onConfirm")
    m.rows = []         ' {node, rec, y, first}: the rows in order, both sections
    m.shift = 0         ' how far the list is scrolled up
    m.focus = 0
    m.seq = 0
end sub

sub onActive()
    if not m.top.active then return
    if m.scheduled = invalid then load() else takeFocus()
end sub

sub takeFocus()
    if m.page.visible then m.page.active = true else m.top.setFocus(true)
    paint()
end sub

sub load()
    m.seq = m.seq + 1
    m.scheduled = []
    m.recorded = invalid
    m.recordedNext = ""
    api("GET", "/v1/dvr/recordings", invalid, "onScheduled", {seq: m.seq})
end sub

' A series rule can book many airings: every page is read before the list is drawn.
sub onScheduled(event as object)
    result = event.getData()
    ctx = event.getRoSGNode().ctx
    if ctx.seq <> m.seq then return
    if not result.ok
        failed()
        return
    end if
    m.scheduled.append(result.data.items)
    if result.data.nextCursor <> invalid and result.data.nextCursor <> ""
        api("GET", "/v1/dvr/recordings?cursor=" + createObject("roUrlTransfer").escape(result.data.nextCursor), invalid, "onScheduled", ctx)
        return
    end if
    api("GET", "/v1/dvr/recordings?state=recorded", invalid, "onRecorded", {seq: m.seq, more: false})
end sub

sub onRecorded(event as object)
    result = event.getData()
    ctx = event.getRoSGNode().ctx
    if ctx.seq <> m.seq then return
    m.asking = false
    if not result.ok
        if not ctx.more then failed()
        return
    end if
    if m.recorded = invalid then m.recorded = []
    m.recorded.append(result.data.items)
    m.recordedNext = ""
    if result.data.nextCursor <> invalid then m.recordedNext = result.data.nextCursor
    keep = ""
    if m.rows.count() > m.focus then keep = m.rows[m.focus].rec.id
    draw()
    for i = 0 to m.rows.count() - 1
        if m.rows[i].rec.id = keep then m.focus = i
    end for
    takeFocus()
end sub

sub failed()
    m.page.icon = "warning"
    m.page.title = "Recordings could not be loaded"
    m.page.body = "The server didn’t answer. Check that it’s running, then try again."
    m.page.buttons = [{id: "retry", text: "Try again", icon: "refresh"}]
    m.list.removeChildrenIndex(m.list.getChildCount(), 0)
    m.rows = []
    m.page.visible = true
    m.page.active = true
end sub

sub onPagePressed()
    m.page.visible = false
    load()
end sub

' Scheduled, then Recorded, each a heading over its rows on a raised card; a section with
' nothing in it is left out, and with nothing in either the page says so.
sub draw()
    m.list.removeChildrenIndex(m.list.getChildCount(), 0)
    m.rows = []
    m.page.visible = false
    y = m.f.listY
    y = section("Scheduled", m.scheduled, y)
    y = section("Recorded", m.recorded, y)
    if m.rows.count() = 0
        m.page.icon = ""
        m.page.title = "No recordings yet"
        m.page.body = "Programs you record from the guide show here, and so do the ones booked to record."
        m.page.buttons = []
        m.page.visible = true
    end if
    if m.focus >= m.rows.count() then m.focus = 0
end sub

function section(heading as string, recs as object, y as integer) as integer
    if recs.count() = 0 then return y
    c = colours()
    label = m.list.createChild("Label")
    label.text = heading
    label.translation = [frames().gutter, y - m.f.headingGap]
    setType(label, "rowHeading", c.text)
    card = m.list.createChild("Poster")
    card.uri = "pkg:/images/round-14.9.png"
    card.blendColor = c.raised
    card.translation = [frames().gutter, y]
    card.width = m.f.width
    top = y
    for each rec in recs
        row = m.list.createChild("ListRow")
        row.rowWidth = m.f.width
        row.model = {label: recordingTitle(rec), help: whenText(rec), scope: "", value: recordingState(rec), control: "", switchOn: false, icon: "", danger: false}
        row.translation = [frames().gutter, y]
        m.rows.push({node: row, rec: rec, y: y, first: y = top})
        y = y + row.rowHeight
    end for
    card.height = y - top
    return y + m.f.sectionGap + m.f.headingGap
end function

' "World Tonight", or a series' "Story Time Island   S1 E3 Homecoming".
function recordingTitle(rec as object) as string
    text = rec.title
    code = ""
    if rec.season <> invalid and rec.episode <> invalid then code = "S" + rec.season.toStr() + " E" + rec.episode.toStr()
    if rec.episodeTitle <> invalid and rec.episodeTitle <> "" then code = (code + " " + rec.episodeTitle).trim()
    if code <> "" then text = text + "   " + code
    return text
end function

' "Tomorrow   8:00 to 9:00 PM   Summit Sports", and what a recording takes on disk once made.
function whenText(rec as object) as string
    s = secondsOf(rec.startsAt)
    text = dayText(s) + "   " + guideTime(s, false) + " to " + guideTime(secondsOf(rec.endsAt), true)
    if rec.channel <> invalid and rec.channel <> "" then text = text + "   " + rec.channel
    if rec.state <> "scheduled" and rec.bytes > 0 then text = text + "   " + recordingSizeText(rec.bytes)
    return text
end function

sub paint()
    for i = 0 to m.rows.count() - 1
        m.rows[i].node.on = (i = m.focus and not m.page.visible)
        ' a card's first row has no line above it, and the line goes beside the row in focus
        m.rows[i].node.separator = not m.rows[i].first and i <> m.focus and i <> m.focus + 1
    end for
    if m.rows.count() = 0 then return
    ' the list scrolls only as far as keeps the row in focus between its top line and the foot
    row = m.rows[m.focus]
    if row.y + row.node.rowHeight + m.shift > m.f.bottom then m.shift = m.f.bottom - row.y - row.node.rowHeight
    if row.y + m.shift < m.f.listY then m.shift = m.f.listY - row.y
    m.list.translation = [0, m.shift]
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    holdKey(key, press and (key = "up" or key = "down"))
    if not press or m.page.visible then return false
    if key = "OK" and m.rows.count() > 0
        openSheet(m.rows[m.focus].rec)
        return true
    end if
    return pressKey(key)
end function

' Up and Down, pressed or held (kit/Hold.brs repeats them).
function pressKey(key as string) as boolean
    if key = "up" and m.focus > 0
        m.focus = m.focus - 1
    else if key = "down" and m.focus < m.rows.count() - 1
        m.focus = m.focus + 1
        ' the next page of recordings is read before focus reaches the last one here
        if m.focus >= m.rows.count() - 5 and m.recordedNext <> "" and m.asking <> true
            m.asking = true
            api("GET", "/v1/dvr/recordings?state=recorded&cursor=" + createObject("roUrlTransfer").escape(m.recordedNext), invalid, "onRecorded", {seq: m.seq, more: true})
        end if
    else
        return false
    end if
    paint()
    return true
end function

' A recording's side sheet: only what this profile may do with it.
sub openSheet(rec as object)
    m.sheetRec = rec
    rows = []
    if recordingPlayable(rec) then rows.push({id: "play", text: "Play", icon: "play"})
    if rec.state = "scheduled" and rec.mayCancel = true then rows.push({id: "cancel", text: "Cancel recording", dot: true})
    if rec.state = "recording" and rec.mayChange = true then rows.push({id: "stop", text: "Stop recording", icon: "stop"})
    if rec.state <> "scheduled" and rec.state <> "recording" and rec.mayChange = true then rows.push({id: "delete", text: "Delete recording", icon: "trash"})
    if rows.count() = 0 then return
    status = whenText(rec)
    if recordingState(rec) <> "" then status = recordingState(rec) + "   " + status
    m.top.menu = {title: recordingTitle(rec), status: status, rows: rows}
end sub

sub onMenuChosen(event as object)
    chosen = event.getData()
    rec = m.sheetRec
    m.asked = chosen
    if chosen = "play"
        ' the recording is a title in the Recorded TV library: read it, then play it as any title
        api("GET", "/v1/items/" + rec.itemId, invalid, "onTitle")
    else if chosen = "cancel"
        m.confirm.model = {title: "Cancel recording " + rec.title + "?", body: "It won’t be recorded. A series rule, if there is one, stays in place.", verb: "Cancel recording"}
    else if chosen = "stop"
        m.confirm.model = {title: "Stop recording " + rec.title + "?", body: "What has been recorded so far is kept.", verb: "Stop recording"}
    else if chosen = "delete"
        ' a recording that failed or was skipped takes nothing on disk, and its question names no size
        size = ""
        if rec.bytes > 0 then size = " (" + recordingSizeText(rec.bytes) + ")"
        m.confirm.model = {title: "Delete " + rec.title + "?", body: "The recording" + size + " is deleted from the server. This can’t be undone.", verb: "Delete"}
    end if
end sub

sub onTitle(event as object)
    result = event.getData()
    if result.ok then m.top.open = {play: true, item: result.data}
end sub

sub onConfirm()
    takeFocus()
    if m.confirm.chosen <> "confirm" then return
    path = "/v1/dvr/recordings/" + m.sheetRec.id
    if m.asked = "stop" then path = path + "?action=stop"
    api("DELETE", path, invalid, "onChanged")
end sub

' The lists as the server has them now; focus stays on the row's place.
sub onChanged(event as object)
    result = event.getData()
    if not result.ok
        m.top.menu = {title: "That didn’t work", body: result.error.message, rows: [{id: "ok", text: "OK"}]}
        return
    end if
    load()
end sub
