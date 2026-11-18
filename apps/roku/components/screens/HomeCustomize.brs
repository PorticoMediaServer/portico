' The layout is two of the profile's own settings, home.rowOrder and home.hiddenRowIds (with the
' saved views on Home in home.savedViews), saved with PATCH /v1/preferences as every app does. The
' server keeps no revision for them: the later of two devices' saves wins, so there is no conflict
' to show. Reset clears them, and Home goes back to the server's own order.
sub init()
    c = colours()
    f = frames().customize
    m.f = f
    m.top.visible = false
    m.top.findNode("surface").x = f.x
    m.textX = f.x + f.pad
    m.textWidth = 1920 - f.padRight - m.textX
    title = m.top.findNode("title")
    title.translation = [m.textX, f.top]
    title.width = m.textWidth
    setType(title, "title", c.text)
    m.intro = m.top.findNode("intro")
    m.intro.translation = [m.textX, f.introY]
    m.intro.width = m.textWidth
    setType(m.intro, "factLabel", c.textTertiary)
    ' the list's own group holds focus while a row has it (the panel cannot take focus back from
    ' its own buttons); its keys come up to the panel's onKeyEvent
    m.window = m.top.findNode("window")
    m.window.focusable = true
    m.rowsGroup = m.top.findNode("rows")
    m.held = m.top.findNode("held")
    m.held.rowWidth = m.textWidth
    m.held.look = "held"
    m.dropLine = m.top.findNode("dropLine")
    m.dropLine.color = c.accentStrong
    m.dropLine.width = m.textWidth
    m.dropLine.height = f.dropLine
    m.foot = m.top.findNode("foot")
    m.foot.translation = [m.textX, f.footY]
    m.foot.observeField("pressed", "onFoot")
    m.hints = m.top.findNode("hints")
    m.hints.translation = [frames().gutter, f.hintsY]
    m.hints.hints = [{keys: ["Up", "Down"], text: "Move"}, {keys: ["OK"], text: "Drop"}, {keys: ["Back"], text: "Put it back"}]
    m.confirm = m.top.findNode("confirm")
    m.confirm.observeField("chosen", "onConfirm")
    m.all = []          ' every row of the layout, in order: {id, title, required, view, hidden, shown}
    m.views = []        ' the profile's saved views (GET /v1/lists?kind=saved_view)
    m.mode = "rows"     ' "rows", or "views" while choosing a saved view to add
    m.zone = "rows"     ' "rows" or "foot"
    m.at = 0            ' the row in focus, as an index into the rows shown
    m.onSwitch = false  ' focus is on that row's switch
    m.picked = -1       ' the row picked up (an index into the rows shown), or -1
    m.target = -1       ' where it will land when dropped
end sub

sub onOpen()
    m.mode = "rows"
    m.zone = "rows"
    m.at = 0
    m.onSwitch = false
    m.picked = -1
    m.target = -1
    m.all = []
    m.views = []
    say("")
    draw()
    m.top.visible = true
    m.window.setFocus(true)
    api("GET", "/v1/home?hidden=include", invalid, "onLayout")
    api("GET", "/v1/lists?kind=saved_view&limit=50", invalid, "onViews")
end sub

' The intro line, or what went wrong in its place.
sub say(problem as string)
    c = colours()
    if m.mode = "views" then m.top.findNode("title").text = "Add a saved view" else m.top.findNode("title").text = "Customize Home"
    if problem <> ""
        m.intro.text = problem
        m.intro.color = c.danger
    else if m.mode = "views"
        m.intro.text = "Choose a saved view to show on Home."
        m.intro.color = c.textSecondary
    else
        m.intro.text = "OK picks a row up. Up and Down move it, OK drops it. Left and Right reach each row's switch."
        m.intro.color = c.textSecondary
    end if
end sub

' Every row Home has in this profile's layout, hidden ones in their place. A row this television
' never shows (reading is not on TV) keeps its place in the order but is not listed.
sub onLayout(event as object)
    result = event.getData()
    if not result.ok
        say("Home's rows couldn't be read. Press Back and try again.")
        return
    end if
    m.all = []
    for each row in result.data.rows
        hidden = (row.hidden = true)
        items = withoutReading(row.items)
        shown = row.id <> "continue_reading" and (hidden or row.deferred = true or items.count() > 0)
        view = (left(row.id, 5) = "view:")
        m.all.push({id: row.id, title: row.title, required: (left(row.id, 8) = "continue"), view: view, hidden: hidden, shown: shown})
    end for
    draw()
end sub

sub onViews(event as object)
    result = event.getData()
    if result.ok and result.data.items <> invalid then m.views = result.data.items
    draw()
end sub

' The indices into m.all of the rows that are listed.
function listed() as object
    out = []
    for i = 0 to m.all.count() - 1
        if m.all[i].shown then out.push(i)
    end for
    return out
end function

' The saved views not on Home yet, as rows of the second panel.
function viewsToAdd() as object
    out = []
    for each view in m.views
        onHome = false
        for each row in m.all
            if row.id = "view:" + view.id then onHome = true
        end for
        if not onHome then out.push(view)
    end for
    return out
end function

function viewsOnHome() as integer
    n = 0
    for each row in m.all
        if row.view then n = n + 1
    end for
    return n
end function

' Draw the list (Home's rows, or the saved views to add) and the foot, keeping the row in focus in
' view. A held row stays in its place in the list as an empty slot; the row itself floats where it
' will land, under the landing line (collections frame 28).
sub draw()
    f = m.f
    m.rowsGroup.removeChildrenIndex(m.rowsGroup.getChildCount(), 0)
    width = m.textWidth
    models = []
    if m.mode = "views"
        for each view in viewsToAdd()
            models.push({title: view.name, note: viewNote(view), right: "", view: true, hidden: false})
        end for
        if models.count() = 0 then say("No saved views to add. Save one from a library's filters.")
    else
        for each i in listed()
            row = m.all[i]
            right = "switch"
            if row.required then right = "lock"
            models.push({title: row.title, note: rowNote(row), right: right, view: row.view, hidden: row.hidden})
        end for
    end if
    inFocus = m.top.isInFocusChain() and m.zone = "rows"
    ' while a row is held the list opens a gap where it will land, and the held row floats in it, so
    ' it never covers another row; the place it came from stays as an empty slot
    gapAt = -1
    if m.picked >= 0 and m.target > m.picked then gapAt = m.target + 1
    if m.picked >= 0 and m.target < m.picked then gapAt = m.target
    for n = 0 to models.count() - 1
        r = m.rowsGroup.createChild("HomeCustomizeRow")
        r.rowWidth = width
        r.model = models[n]
        at = n
        if gapAt >= 0 and n >= gapAt then at = n + 1
        r.translation = [0, at * f.rowPitch]
        look = ""
        if n = m.picked
            look = "slot"
        else if inFocus and m.picked < 0 and n = m.at
            if m.onSwitch then look = "switch" else look = "focus"
        end if
        r.look = look
    end for
    ' the row in focus (or where the held row will land) is kept whole between rowsY and rowsBottom
    at = m.at
    heldAt = m.picked
    if gapAt >= 0 then heldAt = gapAt
    if m.picked >= 0 then at = heldAt
    bottom = (at + 1) * f.rowPitch
    offset = 0
    if bottom > f.rowsBottom - f.rowsY then offset = bottom - (f.rowsBottom - f.rowsY)
    m.rowsGroup.translation = [m.textX, f.rowsY - offset]
    top = f.rowsY
    if offset = 0 then top = f.rowsY - 40
    m.window.clippingRect = [f.x, top, f.width, f.rowsBottom + 36 - top]
    m.held.visible = (m.picked >= 0)
    m.dropLine.visible = (m.picked >= 0 and m.target <> m.picked)
    if m.held.visible
        m.held.model = models[m.picked]
        m.held.translation = [m.textX, f.rowsY - offset + heldAt * f.rowPitch]
        ' the landing line along the held row's lower edge, running out past its ring at both ends
        ' so it reads as a line, not as part of the ring (walk result pages-02, second walk)
        reach = metrics().focusSpread + 6 + f.endPad
        m.dropLine.width = m.textWidth + reach * 2
        m.dropLine.translation = [m.textX - reach, f.rowsY - offset + heldAt * f.rowPitch + f.rowHeight - f.dropLine / 2]
    end if
    m.hints.visible = (m.picked >= 0)
    m.top.holding = m.hints.visible
    ' the foot: Add a saved view only when the server lists saved views and Home has room for one
    m.foot.visible = (m.mode = "rows" and m.picked < 0)
    if m.mode = "rows"
        buttons = []
        if m.views.count() > 0 and viewsOnHome() < 20 then buttons.push({id: "add", text: "Add a saved view", icon: ""})
        buttons.push({id: "reset", text: "Reset to default", icon: ""})
        buttons.push({id: "save", text: "Save", icon: ""})
        if m.foot.buttons = invalid or m.foot.buttons.count() <> buttons.count() then m.foot.buttons = buttons
    end if
end sub

' Under a saved view to add (collections frame 29): its library, then what it finds.
function viewNote(view as object) as string
    parts = []
    if view.library <> invalid and m.global.libraryNames[view.library] <> invalid then parts.push(m.global.libraryNames[view.library])
    if view.description <> invalid and view.description <> "" then parts.push(view.description)
    return parts.join(", ")
end function

' The note under a row's name: Continue your books comes and goes with the books in progress.
function rowNote(row as object) as string
    if row.id = "continue_books" then return "Shown when a book is in progress"
    return ""
end function

function rowCount() as integer
    if m.mode = "views" then return viewsToAdd().count()
    return listed().count()
end function

' The held row lands where the landing line was (movedLayout); focus stays on it.
sub dropPicked()
    m.all = movedLayout(m.all, listed(), m.picked, m.target)
    m.at = m.target
    m.picked = -1
    m.target = -1
end sub

sub toggle()
    rows = listed()
    if m.at >= rows.count() then return
    row = m.all[rows[m.at]]
    if row.required then return
    row.hidden = not row.hidden
    m.all[rows[m.at]] = row
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not press then return true
    if m.confirm.visible then return false
    if key = "back"
        if m.picked >= 0
            ' Back puts the row back where it was picked up: nothing has moved yet
            m.at = m.picked
            m.picked = -1
            m.target = -1
        else if m.mode = "views"
            m.mode = "rows"
            m.at = 0
            say("")
        else
            close("")
            return true
        end if
        draw()
        return true
    end if
    if m.zone = "foot"
        if key = "up" and rowCount() > 0
            m.zone = "rows"
            m.window.setFocus(true)
            draw()
        end if
        return true     ' Left and Right move along the buttons; the panel's edges stop
    end if
    count = rowCount()
    if key = "OK"
        if m.mode = "views"
            addView()
        else if m.onSwitch
            toggle()
        else if m.picked >= 0
            dropPicked()
        else if count > 0
            m.picked = m.at
            m.target = m.at
        end if
    else if key = "up"
        if m.picked >= 0
            if m.target > 0 then m.target = m.target - 1
        else if m.at > 0
            m.at = m.at - 1
        end if
        m.onSwitch = false
    else if key = "down"
        if m.picked >= 0
            if m.target < count - 1 then m.target = m.target + 1
        else if m.at < count - 1
            m.at = m.at + 1
            m.onSwitch = false
        else if m.mode = "rows"
            m.zone = "foot"
            m.onSwitch = false
            m.foot.active = true
        end if
    else if (key = "right" or key = "left") and m.mode = "rows" and m.picked < 0 and count > 0
        ' Left and Right both go between a row and its switch (a row the server keeps has none)
        if m.onSwitch then m.onSwitch = false else m.onSwitch = not m.all[listed()[m.at]].required
    end if
    draw()
    return true         ' the panel holds focus until it closes
end function

' A saved view joins the end of the list, picked up so it can be moved at once.
sub addView()
    choices = viewsToAdd()
    if m.at >= choices.count() then return
    view = choices[m.at]
    m.all.push({id: "view:" + view.id, title: view.name, required: false, view: true, hidden: false, shown: true})
    m.mode = "rows"
    say("")
    m.at = listed().count() - 1
    m.picked = m.at
    m.target = m.at
end sub

sub onFoot(event as object)
    which = event.getData()
    if which = "add"
        m.mode = "views"
        m.zone = "rows"
        m.at = 0
        say("")
        m.window.setFocus(true)
        draw()
    else if which = "reset"
        m.confirm.model = {title: "Reset Home?", body: "Home goes back to the server's own rows and order on every device for this profile.", verb: "Reset"}
    else if which = "save"
        order = []
        hidden = []
        views = []
        for each row in m.all
            order.push(row.id)
            if row.hidden then hidden.push(row.id)
            if row.view then views.push(mid(row.id, 6))
        end for
        write({"home.rowOrder": order, "home.hiddenRowIds": hidden, "home.savedViews": views})
    end if
end sub

sub onConfirm(event as object)
    if event.getData() = "confirm"
        write({"home.rowOrder": invalid, "home.hiddenRowIds": invalid, "home.savedViews": invalid})
    else
        m.foot.active = true
    end if
end sub

sub write(values as object)
    api("PATCH", "/v1/preferences", {scope: "profile-server", deviceClass: "television", values: values}, "onWritten")
end sub

sub onWritten(event as object)
    if event.getData().ok
        close("saved")
    else
        say("Home couldn't be saved. Try again in a moment.")
        m.foot.active = true
    end if
end sub

sub close(how as string)
    m.top.visible = false
    m.top.closed = how
end sub
