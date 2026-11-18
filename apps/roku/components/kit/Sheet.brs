' Rows are 64 px tall on a 74 px pitch; a row marked gapBefore starts a new group 24 px further down.
' A heading's open choices sit on a faint plate together with it.
sub init()
    c = colours()
    f = frames().sheet
    m.x = f.rowsX
    m.width = f.rowsWidth
    m.top.findNode("scrim").color = c.ink
    m.top.findNode("panel").color = c.overlay
    m.top.findNode("plate").blendColor = c.plateOn
    m.top.findNode("panel").translation = [f.x, 0]
    m.top.findNode("panel").width = f.width
    m.rows = m.top.findNode("rows")
    m.groups = m.top.findNode("groups")
    m.window = m.top.findNode("window")
    m.plate = m.top.findNode("plate")
    m.ring = m.top.findNode("ring")
    eyebrow = m.top.findNode("eyebrow")
    eyebrow.translation = [m.x, f.top]
    eyebrow.width = m.width
    setType(eyebrow, "caption", c.textTertiary)
    title = m.top.findNode("title")
    title.width = m.width
    setType(title, "title", c.text)
    title.lineSpacing = 0     ' a two-line title (a setting's name) sits as tight as drawn
    status = m.top.findNode("status")
    status.width = m.width
    setType(status, "bodyStrong", c.textSecondary)
    body = m.top.findNode("body")
    body.width = m.width
    setType(body, "body", c.textSecondary)
    m.plate.width = m.width
    m.plate.height = 64
    m.ring.width = m.width + 72
    m.ring.height = 136
    m.focus = 0
    m.shown = []
    m.scroll = 0
end sub

sub onModel()
    model = m.top.model
    eyebrow = ""
    if model.eyebrow <> invalid then eyebrow = model.eyebrow
    m.top.findNode("eyebrow").text = eyebrow
    title = m.top.findNode("title")
    title.text = model.title
    y = frames().sheet.top
    if eyebrow <> "" then y = y + 38
    title.translation = [m.x, y]
    y = y + title.boundingRect().height + 8
    status = m.top.findNode("status")
    status.text = ""
    if model.status <> invalid then status.text = model.status
    status.translation = [m.x, y]
    if status.text <> "" then y = y + status.boundingRect().height + 8
    body = m.top.findNode("body")
    body.text = ""
    if model.body <> invalid then body.text = model.body
    body.translation = [m.x, y + 8]
    if body.text <> "" then y = y + 8 + body.boundingRect().height + 8
    m.listTop = y + 20
    m.window.translation = [m.x, m.listTop]
    ' the rows end above the remote's keys at the foot, when the sheet has them
    hints = m.top.findNode("hints")
    m.bottom = 1080 - 20
    if model.hints <> invalid then hints.hints = model.hints else hints.hints = []
    if model.hints <> invalid then m.bottom = frames().sheet.hintsY - 46
    hints.translation = [m.x, frames().sheet.hintsY]
    m.window.clippingRect = [-60, -40, m.width + 120, m.bottom - m.listTop + 40]
    m.open = ""
    if model.open <> invalid then m.open = model.open
    m.scroll = 0
    build(currentId())
end sub

' The row to start on: one marked `focus`, else the current choice under the open heading, else
' the first current row.
function currentId() as string
    for each spec in m.top.model.rows
        if spec.focus = true then return spec.id
    end for
    for each spec in m.top.model.rows
        if spec.current = true and (m.open = "" or spec.under = m.open) then return spec.id
    end for
    for each spec in m.top.model.rows
        if spec.heading = true and spec.id = m.open then return spec.id
    end for
    return ""
end function

' Draws the rows that show now (the open heading's choices among them) and puts focus on `focusId`.
sub build(focusId as string)
    c = colours()
    m.rows.removeChildrenIndex(m.rows.getChildCount(), 0)
    m.groups.removeChildrenIndex(m.groups.getChildCount(), 0)
    m.shown = []
    m.ys = []
    m.focus = 0
    groupTop = -1
    y = 0
    rows = []
    for each spec in m.top.model.rows
        if spec.under = invalid or spec.under = m.open then rows.push(spec)
    end for
    for i = 0 to rows.count() - 1
        spec = rows[i]
        if spec.gapBefore = true and m.shown.count() > 0 then y = y + 24
        if groupTop >= 0 and spec.under = invalid
            closeGroup(groupTop, y - 10)
            groupTop = -1
        end if
        if spec.heading = true and spec.id = m.open then groupTop = y
        row = m.rows.createChild("Group")
        row.translation = [0, y]
        back = row.createChild("Poster")
        back.uri = "pkg:/images/round-14.9.png"
        back.blendColor = c.raisedBright
        back.width = m.width
        back.height = 64
        back.visible = (spec.heading <> true and spec.under = invalid and spec.label <> true)
        x = 30
        if spec.under <> invalid then x = 70
        if spec.dot = true
            ' a recording's red dot in the icon's place (Record, Cancel recording)
            icon = row.createChild("Poster")
            icon.uri = "pkg:/images/disc.png"
            icon.width = 18
            icon.height = 18
            icon.translation = [33, 23]
            x = 70
        else if spec.icon <> invalid and spec.icon <> ""
            icon = row.createChild("Poster")
            icon.uri = "pkg:/images/i-" + spec.icon + "-28.png"
            icon.width = 28
            icon.height = 28
            icon.translation = [28, 18]
            x = 70
        else
            row.createChild("Group")
        end if
        label = row.createChild("Label")
        label.text = spec.text
        label.translation = [x, 0]
        label.width = m.width - x - 64
        label.height = 64
        label.vertAlign = "center"
        setType(label, "button", c.textSecondary)
        if spec.label = true
            ' a group's name or a line of help: words only, never a stop for focus
            setType(label, "groupLabel", c.textTertiary)
            label.height = 44
        end if
        side = row.createChild("Label")       ' a heading's value, or a choice's second line
        setType(side, "help", c.textTertiary)
        if spec.heading = true
            side.text = spec.value
            side.width = 300
            side.translation = [m.width - 330, 0]
            side.height = 64
            side.vertAlign = "center"
            side.horizAlign = "right"
            setType(side, "listLabel", c.accentStrong)
        else if spec.value <> invalid and spec.value <> ""
            side.text = spec.value
            side.width = 300
            side.translation = [m.width - 330, 0]
            side.height = 64
            side.vertAlign = "center"
            side.horizAlign = "right"
            setType(side, "metaStrong", c.textSecondary)
        else if spec.detail <> invalid and spec.detail <> ""
            label.height = 40
            label.translation = [x, 4]
            side.text = spec.detail
            side.translation = [x, 36]
            side.width = m.width - x - 30
        end if
        tick = row.createChild("Poster")
        tick.uri = "pkg:/images/i-check-28.png"
        tick.width = 28
        tick.height = 28
        tick.translation = [m.width - 50, 18]
        if spec.under <> invalid then tick.translation = [28, 18]
        tick.visible = (spec.current = true and spec.heading <> true)
        if spec.id = focusId then m.focus = m.shown.count()
        m.shown.push(spec)
        m.ys.push(y)
        if spec.label = true then y = y + 44 else y = y + 74
    end for
    if groupTop >= 0 then closeGroup(groupTop, y - 10)
    if m.shown.count() > 0 and m.shown[m.focus].label = true then pressKey("down")
    paint()
end sub

' The faint plate behind an open heading and its choices.
sub closeGroup(top as integer, bottom as integer)
    plate = m.groups.createChild("Poster")
    plate.uri = "pkg:/images/round-14.9.png"
    plate.blendColor = colours().openGroup
    plate.width = m.width
    plate.height = bottom - top
    plate.translation = [0, top]
end sub

sub onActive()
    if m.top.active then m.top.setFocus(true)
    paint()
end sub

sub paint()
    c = colours()
    count = m.ys.count()
    ' a sheet with no rows (an empty picker) has nothing to focus: no plate, no ring
    m.plate.visible = (count > 0)
    m.ring.visible = (count > 0)
    if count = 0 then return
    ' keep the row in focus on the screen
    room = m.bottom - m.listTop - 40
    y = m.ys[m.focus]
    if y - m.scroll > room - 64 then m.scroll = y - (room - 64)
    if y - m.scroll < 0 then m.scroll = y
    m.rows.translation = [0, -m.scroll]
    m.groups.translation = [0, -m.scroll]
    m.plate.translation = [0, y - m.scroll]
    m.ring.translation = [m.x - 36, m.listTop + y - m.scroll - 36]
    for i = 0 to count - 1
        row = m.rows.getChild(i)
        on = (i = m.focus)
        spec = m.shown[i]
        row.getChild(0).visible = not on and spec.heading <> true and spec.under = invalid and spec.label <> true
        tint = c.textSecondary
        if spec.label = true then tint = c.textTertiary
        if spec.current = true or spec.heading = true then tint = c.text
        if on then tint = c.actionInk
        row.getChild(2).color = tint
        row.getChild(4).blendColor = c.accentStrong
        if on then row.getChild(4).blendColor = tint
        side = c.textTertiary
        if spec.heading = true then side = c.accentStrong
        if on then side = c.actionInk
        row.getChild(3).color = side
        if row.getChild(1).subtype() = "Poster" then row.getChild(1).blendColor = tint
        if spec.dot = true then row.getChild(1).blendColor = c.danger
    end for
end sub

function pressKey(key as string) as boolean
    ' the next row that can take focus; a label row is passed over
    by = 0
    if key = "down" then by = 1
    if key = "up" then by = -1
    if by = 0 then return false
    at = m.focus + by
    while at >= 0 and at < m.ys.count() and m.shown[at].label = true
        at = at + by
    end while
    if at < 0 or at >= m.ys.count() then return false
    m.focus = at
    paint()
    return true
end function

function onKeyEvent(key as string, press as boolean) as boolean
    holdKey(key, press and (key = "up" or key = "down"))
    if not press then return true
    if key = "OK"
        if m.ys.count() = 0 then return true
        spec = m.shown[m.focus]
        if spec.heading = true
            ' a heading opens its choices in place (or folds them away), focus on the current one
            if m.open = spec.id then m.open = "" else m.open = spec.id
            id = spec.id
            if m.open <> "" then id = currentId()
            build(id)
        else
            m.top.chosen = spec.id
        end if
    else if key = "back" or key = "options"
        m.top.dismissed = true
    else
        pressKey(key)
    end if
    return true     ' the sheet holds focus until it closes
end function
