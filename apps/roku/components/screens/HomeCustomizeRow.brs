' Every line of the row comes from frames().customize (its height, the grip's slot, the gaps).
sub init()
    c = colours()
    f = frames().customize
    m.f = f
    setType(m.top.findNode("name"), "listLabel", c.text)
    setType(m.top.findNode("note"), "help", c.textTertiary)
    setType(m.top.findNode("always"), "tag", c.textTertiary)
    m.top.findNode("grip").blendColor = c.textTertiary
    m.top.findNode("lock").blendColor = c.textTertiary
    m.top.findNode("tile").blendColor = c.glass
    m.top.findNode("tileIcon").blendColor = c.textSecondary
end sub

sub layout()
    model = m.top.model
    if model = invalid then return
    f = m.f
    w = m.top.rowWidth
    h = f.rowHeight
    mid = h / 2
    m.top.findNode("grip").translation = [f.gripX + (f.gripWidth - 26) / 2, mid - 13]
    x = f.gripX + f.gripWidth + f.gap
    tile = m.top.findNode("tile")
    tile.visible = model.view = true
    m.top.findNode("tileIcon").visible = tile.visible
    if tile.visible
        tile.translation = [x, mid - 17]
        m.top.findNode("tileIcon").translation = [x + 7, mid - 10]
        x = x + 34 + f.gap
    end if
    ' the right side: the switch, or the lock and its words
    right = w - f.endPad
    sw = m.top.findNode("switch")
    always = m.top.findNode("always")
    lock = m.top.findNode("lock")
    sw.visible = (model.right = "switch")
    always.visible = (model.right = "lock")
    lock.visible = always.visible
    nameRight = right
    if sw.visible
        sw.translation = [right - 64, mid - 18]
        sw.uri = "pkg:/images/switch-" + onOff(model.hidden <> true) + ".png"
        nameRight = right - 64 - f.gap
    else if always.visible
        width = always.boundingRect().width
        always.translation = [right - width, mid - 14]
        lock.translation = [right - width - 9 - 18, mid - 9]
        nameRight = right - width - 9 - 18 - f.gap
    end if
    name = m.top.findNode("name")
    note = m.top.findNode("note")
    name.text = model.title
    name.width = nameRight - x
    note.width = nameRight - x
    note.text = ""
    if model.note <> invalid then note.text = model.note
    if note.text = ""
        name.height = h
        name.vertAlign = "center"
        name.translation = [x, 0]
    else
        name.height = 0
        name.vertAlign = "top"
        name.translation = [x, f.nameTop]
        note.translation = [x, f.noteTop]
    end if
    if model.hidden = true then name.color = colours().textTertiary else name.color = colours().text
    paint()
end sub

function onOff(on as boolean) as string
    if on then return "on"
    return "off"
end function

' At rest nothing; with focus the bright plate and the ring; held the same, lifted over the list; the
' slot is the outline of where a held row came from, with nothing in it.
sub paint()
    c = colours()
    f = m.f
    look = m.top.look
    w = m.top.rowWidth
    h = f.rowHeight
    plate = m.top.findNode("plate")
    plate.width = w
    plate.height = h
    plate.blendColor = c.raisedBright
    plate.visible = (look = "focus" or look = "held")
    ring = m.top.findNode("ring")
    out = metrics().focusSpread + 6
    ring.translation = [-out, -out]
    ring.width = w + out * 2
    ring.height = h + out * 2
    ring.visible = plate.visible
    slot = m.top.findNode("slot")
    slot.width = w
    slot.height = h
    slot.blendColor = c.lineStrong
    slot.visible = (look = "slot")
    m.top.findNode("parts").visible = (look <> "slot")
    if look = "held" then m.top.findNode("grip").blendColor = c.text else m.top.findNode("grip").blendColor = c.textTertiary
    sw = m.top.findNode("switch")
    sring = m.top.findNode("switchRing")
    sring.visible = (look = "switch" and sw.visible)
    if sring.visible
        sring.translation = [sw.translation[0] - out, sw.translation[1] - out]
        sring.width = 64 + out * 2
        sring.height = 36 + out * 2
    end if
end sub
