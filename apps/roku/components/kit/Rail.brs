' Rows are 72 px apart. When the libraries are more than fit, the list shows whole rows, the next
' one peeks in half faded, and each end that has more says how many in words ("2 more below"), as
' the season list does: an entry is never faded, so none looks switched off.
sub init()
    c = colours()
    m.dim = m.top.findNode("dim")
    m.dim.color = c.ink
    m.panel = m.top.findNode("panel")
    m.panel.color = c.railPanel
    m.mark = m.top.findNode("mark")
    m.wordmark = m.top.findNode("wordmark")
    m.plate = m.top.findNode("plate")
    m.plate.blendColor = c.raisedHigh
    m.ring = m.top.findNode("ring")
    m.window = m.top.findNode("window")
    m.list = m.top.findNode("list")
    m.foot = m.top.findNode("foot")
    m.fixed = m.top.findNode("fixed")
    m.fadeBottom = m.top.findNode("fadeBottom")
    m.fadeBottom.blendColor = c.railPanel      ' it lies on the open rail's panel, the only place it shows
    m.moreAbove = m.top.findNode("moreAbove")
    m.moreBelow = m.top.findNode("moreBelow")
    setType(m.moreAbove, "micro", c.textTertiary)
    setType(m.moreBelow, "micro", c.textTertiary)
    m.rows = []       ' every focus stop in order: Search, Home, the libraries, Live TV, Saved, Settings, the profile
    m.count = 0       ' how many libraries there are
    m.fits = 0        ' how many of them show whole at once
    m.lift = 0        ' room above the list for its "more above" words, once it has moved
    m.listFirst = 0   ' where the libraries start in m.rows
    m.listTop = 302
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

' Search and Home are pinned at the top, Live TV and Saved above Settings (they are destinations,
' never something to scroll for); the libraries scroll between them when there are more than fit.
sub onEntries()
    m.list.removeChildrenIndex(m.list.getChildCount(), 0)
    m.foot.removeChildrenIndex(m.foot.getChildCount(), 0)
    m.fixed.removeChildrenIndex(m.fixed.getChildCount(), 0)
    entries = m.top.entries
    if entries = invalid then entries = []
    tops = []
    libraries = []
    bottoms = []
    for each entry in entries
        if entry.id = "search" or entry.id = "home"
            tops.push(entry)
        else if entry.id = "live" or entry.id = "saved"
            bottoms.push(entry)
        else
            libraries.push(entry)
        end if
    end for
    m.rows = []
    y = 158
    for each entry in tops
        row = makeRow(m.fixed, y, entry)
        row.y = y
        m.rows.push(row)
        y = y + 72
    end for
    m.listTop = y
    m.listFirst = m.rows.count()
    m.count = libraries.count()
    room = 868 - y - 72 * bottoms.count()     ' between Home and Live TV
    m.fits = m.count
    used = m.count * 72
    if used > room
        ' a 28 px line of words at each end and half a row peeking in below the whole rows
        m.fits = int((room - 28 * 2 - 36) / 72)
        used = 28 * 2 + m.fits * 72 + 36
    end if
    for i = 0 to libraries.count() - 1
        row = makeRow(m.list, i * 72, libraries[i])
        row.y = -1
        m.rows.push(row)
    end for
    m.window.translation = [0, m.listTop]
    y = m.listTop + used
    for each entry in bottoms
        row = makeRow(m.fixed, y, entry)
        row.y = y
        m.rows.push(row)
        y = y + 72
    end for
    row = makeRow(m.foot, 0, {id: "settings", icon: "settings", label: "Settings"})
    row.y = 884
    m.rows.push(row)
    row = makeRow(m.foot, 72, {id: "profile", icon: "profile", label: ""})
    row.y = 956
    m.rows.push(row)
    under = m.foot.createChild("Label")
    under.text = "Profile and server"
    under.translation = [141, 118]
    setType(under, "micro", colours().textTertiary)
    m.profileUnder = under
    row.label.height = 56
    row.label.translation = [141, 2]
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
    for i = 0 to m.rows.count() - 1
        if m.rows[i].id = m.top.enterOn then m.focus = i
    end for
    m.top.enterOn = ""
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
    ' keep the library in focus inside those shown whole; closed, the window is the first one that
    ' holds the current page's library, so it is the same on every page with the same current entry
    if not m.open then m.scroll = 0
    at = m.focus - m.listFirst
    if at >= 0 and at < m.count
        if at < m.scroll then m.scroll = at
        if at > m.scroll + m.fits - 1 then m.scroll = at - m.fits + 1
    end if
    if m.scroll > m.count - m.fits then m.scroll = m.count - m.fits
    if m.scroll < 0 then m.scroll = 0
    rest = m.count - m.scroll - m.fits
    ' the "more" cue is the open rail's, beside the names: closed, a half-shown icon would read as a
    ' dash, so the closed rail is whole icons only
    m.lift = 0
    if m.open and m.scroll > 0 then m.lift = 28
    peek = 0
    if m.open and rest > 0 then peek = 36
    m.list.translation = [0, m.lift - 72 * m.scroll]
    m.window.clippingRect = [0, m.lift, 440, m.fits * 72 + peek]
    m.fadeBottom.visible = (peek > 0)
    m.fadeBottom.translation = [0, m.listTop + m.lift + m.fits * 72]
    m.moreAbove.visible = (m.open and m.scroll > 0)
    m.moreAbove.text = m.scroll.toStr() + " more above"
    m.moreAbove.translation = [141, m.listTop]
    m.moreBelow.visible = (m.open and rest > 0)
    m.moreBelow.text = rest.toStr() + " more below"
    m.moreBelow.translation = [141, m.listTop + m.lift + m.fits * 72 + peek]
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
        y = m.rows[m.focus].y
        if y < 0 then y = m.listTop + m.lift + 72 * (m.focus - m.listFirst - m.scroll)
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
