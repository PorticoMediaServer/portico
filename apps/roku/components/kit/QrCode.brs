sub onText()
    holder = m.top.findNode("modules")
    holder.removeChildrenIndex(holder.getChildCount(), 0)
    if m.top.text = "" then return
    rows = qrMatrix(m.top.text)
    if rows = invalid then return
    count = rows.count()
    cell = int(260 / count)            ' whole pixels, so every module is the same size
    origin = (300 - cell * count) / 2
    for y = 0 to count - 1
        row = rows[y]
        x = 0
        while x < count
            if mid(row, x + 1, 1) = "1"
                span = 1
                while x + span < count and mid(row, x + span + 1, 1) = "1"
                    span = span + 1
                end while
                r = holder.createChild("Rectangle")
                r.color = "0x071018FF"
                r.width = cell * span
                r.height = cell
                r.translation = [origin + x * cell, origin + y * cell]
                x = x + span
            else
                x = x + 1
            end if
        end while
    end for
end sub
