' Rows are 90 px apart. The window shows five and a half; focus is kept inside the five whole ones.
sub init()
    m.window = m.top.findNode("window")
    m.rows = m.top.findNode("rows")
    m.plate = m.top.findNode("plate")
    m.ring = m.top.findNode("ring")
    m.window.clippingRect = [-60, 0, 460, 495]
    m.scroll = 0
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
    if m.scroll > 0 then lift = 45
    if m.scroll > 0 and m.scroll + 5 >= count then lift = 45
    m.rows.translation = [0, lift - 90 * m.scroll]
    y = lift + 90 * (at - m.scroll)
    m.plate.translation = [-10, y]
    has = m.top.hasFocus()
    m.plate.visible = (count > 0)
    if has then m.plate.opacity = 1 else m.plate.opacity = 0.55
    m.ring.visible = has
    m.ring.translation = [-46, y - 36]
    m.top.findNode("fadeTop").visible = (m.scroll > 0)
    m.top.findNode("fadeBottom").visible = (m.scroll + 5 < count) or (lift > 0 and m.scroll + 5 <= count and count > 5 and m.scroll + 5 < count)
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not press then return false
    if key = "down"
        if m.top.index < m.rows.getChildCount() - 1
            m.top.index = m.top.index + 1
            return true
        end if
        return false     ' past the last season: the page's next section
    else if key = "up"
        if m.top.index > 0 then m.top.index = m.top.index - 1
        return true
    end if
    return false
end function
