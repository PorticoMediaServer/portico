sub init()
    c = colours()
    f = frames().fileInfo
    m.f = f
    m.top.visible = false
    m.x = f.x + f.pad
    m.width = 1920 - f.x - f.pad * 2
    m.top.findNode("surface").x = f.x
    eyebrow = m.top.findNode("eyebrow")
    eyebrow.translation = [m.x, f.top]
    setType(eyebrow, "metaStrong", c.textSecondary)
    m.name = m.top.findNode("name")
    m.name.width = m.width
    setType(m.name, "title", c.text)
    m.meta = m.top.findNode("meta")
    m.meta.translation = [m.x, f.metaY]
    m.meta.width = m.width
    setType(m.meta, "meta", c.textSecondary)
    m.table = m.top.findNode("table")
    m.table.translation = [m.x, f.tableY]
    m.note = m.top.findNode("note")
    m.note.width = m.width
    setType(m.note, "caption", c.textSecondary)
    m.done = m.top.findNode("done")
    m.done.translation = [m.x, f.doneY]
    m.done.buttons = [{id: "done", text: "Done", icon: ""}]
    m.done.observeField("pressed", "close")
end sub

sub onItem()
    c = colours()
    item = m.top.item
    versions = []
    if item.versions <> invalid then versions = item.versions
    ' the name sits on the bottom of its two-line slot, so the meta line never moves
    m.name.text = item.title
    m.name.translation = [m.x, m.f.metaY - 8 - m.name.boundingRect().height]
    meta = []
    if item.kind = "episode"
        if item.rootTitle <> invalid then meta.push(item.rootTitle)
        if episodeCode(item) <> "" then meta.push(episodeCode(item))
    else if item.year <> invalid
        meta.push(item.year.toStr())
    end if
    meta.push(countText(versions.count(), "version", "versions"))
    m.meta.text = meta.join("      ")
    m.table.removeChildrenIndex(m.table.getChildCount(), 0)
    columns = m.f.columns
    rows = [["Version", "Resolution", "Video", "Audio", "Size", "Length"]]
    for i = 0 to versions.count() - 1
        v = versions[i]
        name = "Version " + (i + 1).toStr()
        if v.edition <> invalid and v.edition <> "" then name = v.edition
        resolution = ""
        if v.width <> invalid and v.height <> invalid then resolution = v.width.toStr() + " × " + v.height.toStr()
        rows.push([name, resolution, versionVideo(v), versionAudio(v), sizeText(v.size), durationText(v.durationMs)])
    end for
    y = 0
    for r = 0 to rows.count() - 1
        grows = 0
        for k = 0 to columns.count() - 1
            cell = m.table.createChild("Label")
            cell.text = rows[r][k]
            cell.translation = [columns[k], y]
            ' each cell ends where the next column starts; a version's name is never cut: it takes a
            ' second line and its row grows (two Dolby Vision editions must read apart)
            right = m.width
            if k < columns.count() - 1 then right = columns[k + 1] - 16
            cell.width = right - columns[k]
            if r = 0
                setType(cell, "help", c.textTertiary)
            else if k = 0
                setType(cell, "listLabel", c.text)
                oneLine = cell.boundingRect().height
                cell.wrap = true
                cell.maxLines = 2
                grows = cell.boundingRect().height - oneLine
            else
                setType(cell, "factLabel", c.text)
            end if
        end for
        if r > 0
            rule = m.table.createChild("Rectangle")
            rule.color = c.separator
            rule.width = m.width
            rule.height = 1
            rule.translation = [0, y - 14]
        end if
        if grows < 0 then grows = 0
        y = y + m.f.rowPitch + grows
    end for
    m.note.translation = [m.x, m.f.tableY + y + 4]
    m.top.visible = true
    m.done.active = true
end sub

sub close()
    m.top.visible = false
    m.top.closed = true
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if press and key = "back" then close()
    return true     ' the panel holds focus until it closes
end function
