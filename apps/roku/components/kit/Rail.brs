' Rows are 72 px apart. The middle list shows nine and scrolls when there are more;
' Settings and the profile stay at the foot.
sub init()
    m.dim = m.top.findNode("dim")
    m.panel = m.top.findNode("panel")
    m.mark = m.top.findNode("mark")
    m.wordmark = m.top.findNode("wordmark")
    m.plate = m.top.findNode("plate")
    m.ring = m.top.findNode("ring")
    m.window = m.top.findNode("window")
    m.list = m.top.findNode("list")
    m.foot = m.top.findNode("foot")
    m.window.clippingRect = [0, 0, 440, 648]
    m.rows = []       ' every focus stop in order: the entries, then Settings, then the profile
    m.count = 0       ' how many of them are in the middle list
    m.focus = 0
    m.open = false
    m.scroll = 0
    onEntries()
end sub

function makeRow(parent as object, y as integer, entry as object) as object
    row = parent.createChild("Group")
    row.translation = [0, y]
    bar = row.createChild("Rectangle")
    bar.width = 3
    bar.height = 28
    bar.translation = [56, 22]
    bar.color = colours().accent
    if entry.icon = "profile"
        face = row.createChild("Poster")
        face.uri = "pkg:/images/round-20.9.png"
        face.width = 40
        face.height = 40
        face.translation = [84, 16]
        initial = row.createChild("Label")
        initial.width = 40
        initial.height = 40
        initial.translation = [84, 16]
        initial.horizAlign = "center"
        initial.vertAlign = "center"
        setType(initial, "tag", colours().text)
    else
        icon = row.createChild("Poster")
        icon.uri = "pkg:/images/i-" + entry.icon + "-32.png"
        icon.width = 32
        icon.height = 32
        icon.translation = [88, 20]
    end if
    label = row.createChild("Label")
    label.translation = [141, 0]
    label.height = 72
    label.width = 250
    label.vertAlign = "center"
    label.text = entry.label
    setType(label, "nav")
    return {id: entry.id, node: row, bar: bar, icon: row.getChild(1), label: label}
end function

sub onEntries()
    m.list.removeChildrenIndex(m.list.getChildCount(), 0)
    m.foot.removeChildrenIndex(m.foot.getChildCount(), 0)
    m.rows = []
    entries = m.top.entries
    if entries = invalid then entries = []
    y = 0
    for each entry in entries
        m.rows.push(makeRow(m.list, y, entry))
        y = y + 72
    end for
    m.count = m.rows.count()
    m.rows.push(makeRow(m.foot, 0, {id: "settings", icon: "settings", label: "Settings"}))
    m.rows.push(makeRow(m.foot, 72, {id: "profile", icon: "profile", label: ""}))
    under = m.foot.createChild("Label")
    under.text = "Profile and server"
    under.translation = [141, 118]
    setType(under, "micro", colours().textTertiary)
    m.profileUnder = under
    m.rows[m.rows.count() - 1].label.height = 56
    m.rows[m.rows.count() - 1].label.translation = [141, 2]
    onProfile()
    onCurrent()
end sub

sub onProfile()
    if m.rows.count() = 0 then return
    row = m.rows[m.rows.count() - 1]
    name = m.top.profileName
    row.label.text = name
    row.node.getChild(1).visible = (name <> "")       ' no face until the profile is known
    if m.top.profileColour <> "" then row.node.getChild(1).blendColor = m.top.profileColour
    row.node.getChild(2).text = ucase(left(name, 1))
end sub

sub onCurrent()
    for i = 0 to m.rows.count() - 1
        if m.rows[i].id = m.top.current then m.focus = i
    end for
    paint()
end sub

sub onActive()
    if not m.top.active then return
    m.open = true
    onCurrent()
    m.top.setFocus(true)
    paint()
end sub

sub close()
    m.open = false
    m.scroll = 0
    onCurrent()
end sub

sub paint()
    c = colours()
    if m.top.isOpen <> m.open then m.top.isOpen = m.open
    m.dim.visible = m.open
    m.panel.visible = m.open
    m.wordmark.visible = m.open
    m.mark.visible = not m.open
    ' keep the row in focus (the current page's, when closed) inside the nine shown
    if m.focus < m.count
        if m.focus < m.scroll then m.scroll = m.focus
        if m.focus > m.scroll + 8 then m.scroll = m.focus - 8
    end if
    m.list.translation = [0, -72 * m.scroll]
    for i = 0 to m.rows.count() - 1
        row = m.rows[i]
        isCurrent = (row.id = m.top.current)
        hasFocus = (m.open and i = m.focus)
        row.bar.visible = isCurrent and not hasFocus
        row.label.visible = m.open
        if row.id = "profile" and m.profileUnder <> invalid then m.profileUnder.visible = m.open
        tint = c.textTertiary
        if isCurrent or hasFocus then tint = c.text
        row.label.color = tint
        if row.id <> "profile" then row.icon.blendColor = tint
    end for
    m.plate.visible = m.open
    m.ring.visible = m.open
    if m.open
        y = 884 + 72 * (m.focus - m.count)
        if m.focus < m.count then y = 158 + 72 * (m.focus - m.scroll)
        m.plate.translation = [60, y]
        m.ring.translation = [24, y - 36]
    end if
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not m.open then return false
    holdKey(key, press and (key = "up" or key = "down"))
    if not press then return true
    pressKey(key)
    return true   ' the rail holds focus until it is left
end function

function pressKey(key as string) as boolean
    if key = "up" and m.focus > 0
        m.focus = m.focus - 1
        paint()
    else if key = "down" and m.focus < m.rows.count() - 1
        m.focus = m.focus + 1
        paint()
    else if key = "OK"
        id = m.rows[m.focus].id
        close()
        m.top.chosen = id
    else if key = "right" or key = "back"
        close()
        m.top.left = true
    else
        return false
    end if
    return true
end function
