sub init()
    m.f = frames().settings
    text = readAsciiFile("pkg:/source/licences.json")
    m.data = invalid
    if text <> "" then m.data = parseJson(text)
    m.list = m.top.findNode("list")
    m.licence = m.top.findNode("licence")
    m.paragraphs = m.top.findNode("paragraphs")
    m.stops = []          ' the list's rows: {node, component}
    m.row = 0
    m.text = []           ' the open licence's paragraphs
    m.at = 0
    c = colours()
    setType(m.top.findNode("licenceTitle"), "title", c.text)
    setType(m.top.findNode("licenceMeta"), "body", c.textSecondary)
    drawList()
end sub

' Portico's own licence and source, then the components in their groups, each with its licence.
sub drawList()
    c = colours()
    f = m.f
    title = m.list.createChild("Label")
    title.text = "Open-source licenses"
    title.translation = [f.pageX, f.top]
    setType(title, "title", c.text)
    intro = m.list.createChild("Label")
    intro.text = "Portico is free software. You can share it and change it under the GNU General Public License, version 3 or any later version. It comes with no warranty."
    intro.translation = [f.pageX, f.top + title.boundingRect().height + 14]
    intro.width = f.introWidth
    intro.wrap = true
    intro.maxLines = 2
    setType(intro, "body", c.textSecondary)
    y = intro.translation[1] + intro.boundingRect().height + f.headerGap
    if m.data = invalid then return
    own = []
    others = []
    for each part in m.data.components
        if part.group = "portico" then own.push(part) else others.push(part)
    end for
    y = section(y, "Portico", own, true)
    source = []
    for each place in m.data.sources
        source.push({name: "The Portico apps", url: place.url})
    end for
    y = section(y, "Source code", source, false)
    count = m.list.createChild("Label")
    count.text = others.count().toStr() + " components"
    count.translation = [f.pageX + 24, y]
    setType(count, "groupLabel", c.textSecondary)
    y = y + count.boundingRect().height + 14
    section(y, "Fonts and icons", others, true)
    paint()
end sub

' A section's label, then its rows on one raised card; returns where the next starts.
function section(y as integer, label as string, parts as object, opens as boolean) as integer
    c = colours()
    f = m.f
    heading = m.list.createChild("Label")
    heading.text = label
    heading.translation = [f.pageX + 24, y]
    setType(heading, "groupLabel", c.textSecondary)
    y = y + heading.boundingRect().height + 22
    card = m.list.createChild("Poster")
    card.uri = "pkg:/images/round-14.9.png"
    card.blendColor = c.raised
    card.translation = [f.pageX, y]
    card.width = f.pageWidth
    top = y
    first = true
    for each part in parts
        help = ""
        if part.url <> invalid and not opens then help = part.url.replace("https://", "")
        if part.copyright <> invalid then help = part.copyright.join(" ")
        control = ""
        value = ""
        if opens
            control = "open"
            value = part.licence
        end if
        item = m.list.createChild("ListRow")
        item.rowWidth = f.pageWidth
        item.model = {label: part.name, help: help, scope: "", value: value, control: control, switchOn: false, icon: ""}
        item.translation = [f.pageX, y]
        component = invalid
        if opens then component = part
        m.stops.push({node: item, component: component, first: first})
        first = false
        y = y + item.rowHeight
    end for
    card.height = y - top
    return y + f.sectionGap
end function

sub onActive()
    if not m.top.active then return
    m.top.setFocus(true)
    paint()
end sub

sub paint()
    has = m.top.hasFocus()
    for i = 0 to m.stops.count() - 1
        here = m.stops[i]
        here.node.on = (has and not m.licence.visible and i = m.row)
        here.node.separator = not here.first and not (has and i - 1 = m.row)
    end for
    ' the list moves only to keep the row in focus clear of the screen's foot
    scroll = 0
    if m.stops.count() > 0
        node = m.stops[m.row].node
        bottom = node.translation[1] + node.rowHeight
        if bottom > m.f.foot then scroll = bottom - m.f.foot
    end if
    m.list.translation = [0, -scroll]
end sub

' A licence: its name, its licence and copyright, then its text a paragraph at a time.
sub openLicence(part as object)
    c = colours()
    f = m.f
    m.list.visible = false
    m.licence.visible = true
    title = m.top.findNode("licenceTitle")
    title.text = part.name
    title.translation = [f.pageX, f.top]
    meta = m.top.findNode("licenceMeta")
    words = part.licence
    if part.copyright <> invalid then words = words + "   " + part.copyright.join(" ")
    meta.text = words
    meta.width = f.pageWidth
    meta.translation = [f.pageX, f.top + title.boundingRect().height + 14]
    m.textTop = meta.translation[1] + meta.boundingRect().height + f.headerGap
    m.text = []
    for each block in m.data.texts[part.text].split(chr(10) + chr(10))
        line = block.trim()
        if line <> "" then m.text.push(line)
    end for
    m.at = 0
    drawParagraphs()
end sub

' Only the paragraphs from the one in focus down to the screen's foot are drawn.
sub drawParagraphs()
    c = colours()
    f = m.f
    m.paragraphs.removeChildrenIndex(m.paragraphs.getChildCount(), 0)
    y = m.textTop
    for i = m.at to m.text.count() - 1
        if y > f.foot then exit for
        plate = m.paragraphs.createChild("Poster")
        plate.uri = "pkg:/images/round-14.9.png"
        label = m.paragraphs.createChild("Label")
        label.text = m.text[i]
        label.wrap = true
        label.width = f.pageWidth - 48
        label.translation = [f.pageX + 24, y + 18]
        setType(label, "body", c.textSecondary)
        height = label.boundingRect().height + 36
        plate.translation = [f.pageX, y]
        plate.width = f.pageWidth
        plate.height = height
        if i = m.at
            ' the paragraph in focus: the bright plate and the ring (rule 11)
            plate.blendColor = c.raisedBright
            label.color = c.text
            out = metrics().focusSpread + 6
            ring = m.paragraphs.createChild("Poster")
            ring.uri = "pkg:/images/focus-14.9.png"
            ring.translation = [f.pageX - out, y - out]
            ring.width = f.pageWidth + out * 2
            ring.height = height + out * 2
        else
            plate.blendColor = c.raised
        end if
        y = y + height + 12
    end for
end sub

function pressKey(key as string) as boolean
    by = 1
    if key = "up" then by = -1
    if m.licence.visible
        want = m.at + by
        if want < 0 or want >= m.text.count() then return false
        m.at = want
        drawParagraphs()
        return true
    end if
    want = m.row + by
    if want < 0 or want >= m.stops.count() then return false
    m.row = want
    paint()
    return true
end function

function onKeyEvent(key as string, press as boolean) as boolean
    holdKey(key, press and (key = "up" or key = "down"))
    if not press then return false
    if key = "up" or key = "down"
        pressKey(key)
        return true
    end if
    if key = "back" or key = "left"
        if m.licence.visible
            m.licence.visible = false
            m.list.visible = true
            paint()
        else
            m.top.done = true
        end if
        return true
    end if
    if key = "OK" and not m.licence.visible and m.stops.count() > 0
        part = m.stops[m.row].component
        if part <> invalid then openLicence(part)     ' the source rows only say where the code is
        return true
    end if
    return true
end function
