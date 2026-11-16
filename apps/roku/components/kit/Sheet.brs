' Rows are 64 px tall on a 74 px pitch; a row marked gapBefore starts a new group 24 px further down.
sub init()
    c = colours()
    m.rows = m.top.findNode("rows")
    m.window = m.top.findNode("window")
    m.plate = m.top.findNode("plate")
    m.ring = m.top.findNode("ring")
    setType(m.top.findNode("eyebrow"), "caption", c.textTertiary)
    setType(m.top.findNode("title"), "title", c.text)
    setType(m.top.findNode("status"), "bodyStrong", c.textSecondary)
    m.focus = 0
    m.ys = []
    m.scroll = 0
end sub

sub onModel()
    c = colours()
    model = m.top.model
    eyebrow = ""
    if model.eyebrow <> invalid then eyebrow = model.eyebrow
    m.top.findNode("eyebrow").text = eyebrow
    title = m.top.findNode("title")
    title.text = model.title
    y = 102 + title.boundingRect().height + 8
    status = m.top.findNode("status")
    status.text = ""
    if model.status <> invalid then status.text = model.status
    status.translation = [1336, y]
    if status.text <> "" then y = y + 44
    m.listTop = y + 20
    m.window.translation = [1336, m.listTop]
    m.window.clippingRect = [-60, -40, 660, 1080 - m.listTop + 40]
    m.rows.removeChildrenIndex(m.rows.getChildCount(), 0)
    m.ys = []
    m.focus = 0
    y = 0
    for i = 0 to model.rows.count() - 1
        spec = model.rows[i]
        if spec.gapBefore = true and i > 0 then y = y + 24
        row = m.rows.createChild("Group")
        row.translation = [0, y]
        back = row.createChild("Poster")
        back.uri = "pkg:/images/round-14.9.png"
        back.blendColor = "0x1D3848FF"
        back.width = 528
        back.height = 64
        x = 30
        if spec.icon <> invalid and spec.icon <> ""
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
        label.width = 528 - x - 64
        label.height = 64
        label.vertAlign = "center"
        setType(label, "button", c.textSecondary)
        tick = row.createChild("Poster")
        tick.uri = "pkg:/images/i-check-28.png"
        tick.width = 28
        tick.height = 28
        tick.translation = [478, 18]
        tick.visible = (spec.current = true)
        if spec.current = true then m.focus = i
        m.ys.push(y)
        y = y + 74
    end for
    m.total = y
    paint()
end sub

sub onActive()
    if m.top.active then m.top.setFocus(true)
    paint()
end sub

sub paint()
    c = colours()
    count = m.ys.count()
    if count = 0 then return
    ' keep the row in focus on the screen
    room = 1080 - m.listTop - 60
    y = m.ys[m.focus]
    if y - m.scroll > room - 64 then m.scroll = y - (room - 64)
    if y - m.scroll < 0 then m.scroll = y
    m.rows.translation = [0, -m.scroll]
    m.plate.translation = [0, y - m.scroll]
    m.ring.translation = [1336 - 36, m.listTop + y - m.scroll - 36]
    for i = 0 to count - 1
        row = m.rows.getChild(i)
        on = (i = m.focus)
        row.getChild(0).visible = not on
        tint = c.textSecondary
        if on then tint = c.actionInk
        row.getChild(2).color = tint
        row.getChild(3).blendColor = tint
        if row.getChild(1).subtype() = "Poster" then row.getChild(1).blendColor = tint
    end for
end sub

function pressKey(key as string) as boolean
    if key = "down" and m.focus < m.ys.count() - 1
        m.focus = m.focus + 1
    else if key = "up" and m.focus > 0
        m.focus = m.focus - 1
    else
        return false
    end if
    paint()
    return true
end function

function onKeyEvent(key as string, press as boolean) as boolean
    holdKey(key, press and (key = "up" or key = "down"))
    if not press then return true
    if key = "OK"
        if m.ys.count() > 0 then m.top.chosen = m.top.model.rows[m.focus].id
    else if key = "back" or key = "options"
        m.top.dismissed = true
    else
        pressKey(key)
    end if
    return true     ' the sheet holds focus until it closes
end function
