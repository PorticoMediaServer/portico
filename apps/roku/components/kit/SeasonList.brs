' Rows are 90 px apart. Five whole seasons show and the next peeks in below, half faded. Each end
' that has more says how many in words ("3 more above", "16 more below"), so the list is never
' mistaken for its end.
sub init()
    m.window = m.top.findNode("window")
    m.rows = m.top.findNode("rows")
    m.plate = m.top.findNode("plate")
    m.ring = m.top.findNode("ring")
    m.window.clippingRect = [-60, 0, 460, 540]
    m.scroll = 0
    setType(m.top.findNode("moreAbove"), "micro", colours().textTertiary)
    setType(m.top.findNode("moreBelow"), "micro", colours().textTertiary)
    m.top.observeField("focusedChild", "paint")
end sub

sub onSeasons()
    c = colours()
    m.rows.removeChildrenIndex(m.rows.getChildCount(), 0)
    y = 0
    for each season in m.top.seasons
        row = m.rows.createChild("Group")
        row.translation = [0, y]
        name = row.createChild("Label")
        name.translation = [22, 12]
        name.width = 300
        name.text = season.title
        setType(name, "seasonName", c.text)
        x = 22
        if season.progress >= 0
            track = row.createChild("Rectangle")
            track.color = c.track
            track.width = 72
            track.height = 5
            track.translation = [22, 62]
            bar = row.createChild("Rectangle")
            bar.color = c.accent
            bar.width = 72 * season.progress
            bar.height = 5
            bar.translation = [22, 62]
            x = 106
        end if
        line = row.createChild("Label")
        line.translation = [x, 50]
        line.text = season.text
        setType(line, "caption", c.textTertiary)
        y = y + 90
    end for
    paint()
end sub

sub onActive()
    if m.top.active then m.top.setFocus(true)
    paint()
end sub

sub paint()
    count = m.rows.getChildCount()
    at = m.top.index
    if at < m.scroll then m.scroll = at
    if at > m.scroll + 4 then m.scroll = at - 4
    if m.scroll > count - 5 then m.scroll = count - 5
    if m.scroll < 0 then m.scroll = 0
    ' once the list has moved, half a row shows above as well, so both ends say there is more
    lift = 0
    if m.scroll > 0 then lift = 36      ' room for the "more above" line
    m.rows.translation = [0, lift - 90 * m.scroll]
    y = lift + 90 * (at - m.scroll)
    m.plate.translation = [-10, y]
    has = m.top.hasFocus()
    m.plate.visible = (count > 0)
    if has then m.plate.opacity = 1 else m.plate.opacity = 0.55
    m.ring.visible = has
    m.ring.translation = [-46, y - 36]
    above = m.top.findNode("moreAbove")
    below = m.top.findNode("moreBelow")
    above.visible = (m.scroll > 0)
    above.text = m.scroll.toStr() + " more above"
    rest = count - m.scroll - 5
    below.visible = (rest > 0)
    below.text = rest.toStr() + " more below"
    m.top.findNode("fadeBottom").visible = (rest > 0)
    m.rows.clippingRect = [-60, 90 * m.scroll, 460, 540 - lift]
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    holdKey(key, press and (key = "up" or key = "down"))
    if not press then return false
    return pressKey(key)
end function

function pressKey(key as string) as boolean
    if key = "down"
        if m.top.index < m.rows.getChildCount() - 1
            m.top.index = m.top.index + 1
            return true
        end if
        return false     ' past the last season: the page's next section
    else if key = "up"
        if m.top.index > 0
            m.top.index = m.top.index - 1
            return true
        end if
        return m.heldKey = invalid   ' the top stops; a held key's repeat ends here
    end if
    return false
end function
