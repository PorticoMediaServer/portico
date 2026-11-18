' Each page is a list of parts {c: a kit component, at: [x, y], f: its fields}. A frame's page is made
' from frames() itself, and the type roles' pages from typeRoles(), so the page always shows what the
' code uses. Nothing here draws an element of its own: only the frames' lines and the captions.
sub init()
    m.page = m.top.findNode("page")
    caption = m.top.findNode("caption")
    caption.translation = [40, 1036]
    setType(caption, "help", colours().textTertiary)
    m.pages = kitPages()
    m.at = 0
    show()
end sub

function kitPages() as object
    art = "pkg:/images/tint-2.jpg"     ' a picture that ships with the app, for "with art"
    pages = [{title: "Backdrop, on its own", parts: [{c: "Backdrop", at: [0, 0], f: {}}]}]
    for each name in sortedKeys(frames())
        if type(frames()[name]) = "roAssociativeArray" then pages.push({title: "Frame " + name, frame: name})
    end for
    for each page in typePages()
        pages.push(page)
    end for
    pages.append([
        {title: "Buttons in every state", parts: [
            {c: "Button", at: [200, 120], f: {text: "At rest"}}, {c: "Button", at: [520, 120], f: {text: "In focus", on: true}},
            {c: "Button", at: [860, 120], f: {text: "Switched on", switchedOn: true}}, {c: "Button", at: [200, 260], f: {text: "Sign out", danger: true}},
            {c: "Button", at: [520, 260], f: {text: "Sign out", danger: true, on: true}}, {c: "Button", at: [860, 260], f: {text: "My List", icon: "plus"}},
            {c: "Button", at: [1200, 260], f: {icon: "more"}}, {c: "Button", at: [200, 400], f: {text: "Resume", icon: "play", progress: 0.4, detail: "1 h 4 min left"}},
            {c: "Button", at: [200, 540], f: {text: "Resume", icon: "play", progress: 0.4, detail: "1 h 4 min left", on: true}},
            {c: "Button", at: [860, 540], f: {text: "Play", icon: "play", minWidth: 320}},
            {c: "ButtonRow", at: [200, 680], f: {buttons: [{id: "a", text: "A button row", icon: ""}, {id: "b", text: "Its second", icon: ""}]}}]},
        {title: "List rows of each kind", parts: [
            {c: "ListRow", at: [200, 100], f: {model: listRow({label: "A plain row"})}},
            {c: "ListRow", at: [200, 190], f: {model: listRow({label: "With help and scope", help: "Help under the label", scope: "This device"})}},
            {c: "ListRow", at: [200, 300], f: {model: listRow({label: "A choice", value: "Automatic", control: "choice"}), on: true}},
            {c: "ListRow", at: [200, 400], f: {model: listRow({label: "A switch, on", control: "switch", switchOn: true})}},
            {c: "ListRow", at: [200, 490], f: {model: listRow({label: "A switch, off", control: "switch"})}},
            {c: "ListRow", at: [200, 580], f: {model: listRow({label: "A group that opens", control: "expand"})}},
            {c: "ListRow", at: [200, 670], f: {model: listRow({label: "A group that closes", control: "collapse"})}},
            {c: "ListRow", at: [200, 760], f: {model: listRow({label: "With an icon", icon: "server"})}},
            {c: "ListRow", at: [200, 850], f: {model: listRow({label: "A destructive action", icon: "signOut", danger: true})}}]}
    ])
    for each pair in [["portrait", "landscape"], ["square", "episode"], ["gridPoster", "person"]]
        parts = []
        y = 80
        for each shape in pair
            parts.push({c: "Shelf", at: [0, y], f: {heading: "Cards: " + shape, shape: shape, cards: sampleCards(shape, art)}})
            y = y + shelfMetrics(shape).block + 40
        end for
        pages.push({title: "Cards: " + pair[0] + " and " + pair[1] + " (with art, without, in progress)", parts: parts})
    end for
    pages.append([
        {title: "The side sheet", parts: [{c: "Sheet", at: [0, 0], f: {visible: true, model: {eyebrow: "An eyebrow", title: "A sheet's title", status: "Its status line", rows: [
            {id: "a", text: "A choice", icon: "", current: true}, {id: "b", text: "Another", icon: "", detail: "Its detail"}, {id: "c", text: "After a gap", icon: "", gapBefore: true}]}}}]},
        {title: "The side sheet: headings with their values, a heading open, the key hints", parts: [{c: "Sheet", at: [0, 0], f: {visible: true, model: {eyebrow: "Playback", title: "Options", status: "", open: "quality", rows: [
            {id: "quality", text: "Quality", icon: "", heading: true, value: "Automatic"}, {id: "q1", text: "Automatic", icon: "", under: "quality", current: true},
            {id: "q2", text: "1080p High", icon: "", under: "quality"}, {id: "subtitles", text: "Subtitles", icon: "", heading: true, value: "Off"}],
            hints: [{keys: ["OK"], text: "Choose"}, {keys: ["Back"], text: "Close"}]}}}]},
        {title: "Track rows: a song, a chapter already heard, the chapter playing (in focus)", parts: [
            {c: "TrackRow", at: [200, 200], f: {model: {art: art, title: "A song", sub: "Its artist", duration: "3:45"}}},
            {c: "TrackRow", at: [200, 320], f: {model: {number: "2", title: "A chapter already heard", duration: "22:15", done: true}}},
            {c: "TrackRow", at: [200, 440], f: {model: {number: "3", title: "The chapter playing", duration: "35:19", playing: true, progress: 0.45}, on: true}}]},
        {title: "The level meter that marks what is playing", parts: [{c: "LevelMeter", at: [200, 200], f: {}}]},
        {title: "The now-playing card, at rest", parts: [{c: "NowPlaying", at: [0, 0], f: {now: {title: "A song", artist: "Its artist", art: art, position: 60, duration: 225, paused: false, hasNext: true}}}]},
        {title: "Channel badges (kit/ChannelMark): initials on the name's tint, 56 and 64; a channel with a logo shows the logo", parts: [{c: "ChannelMark", at: [200, 200], f: {channel: {name: "Summit Sports Extra", logo: ""}}}, {c: "ChannelMark", at: [320, 200], f: {size: 64, channel: {name: "Northwind News", logo: ""}}}]},
        {title: "In a section: error and empty rows (the kit has no loading row: a section waits in its own place)", parts: [
            {c: "StateRow", at: [200, 200], f: {kind: "error", title: "Couldn't load this row", reason: "The server didn't answer.", action: "Try again"}},
            {c: "StateRow", at: [200, 480], f: {kind: "empty", title: "Nothing here yet", reason: "What you add shows here."}}]},
        {title: "A person's face: their picture cut round, or their initials over a tint picked by their name", parts: [
            {c: "Face", at: [200, 200], f: {size: 280, name: "Steven Spielberg", photo: ""}}, {c: "Face", at: [560, 200], f: {size: 280, name: "Zendaya", photo: ""}},
            {c: "Face", at: [920, 200], f: {size: 280, name: "Timothée Chalamet", photo: ""}}]},
        {title: "The whole-page state", parts: [{c: "PageState", at: [0, 0], f: {icon: "warning", title: "Can't reach the server", body: "You're still signed in. Try again in a moment.", buttons: [{id: "a", text: "Try again now", icon: "refresh"}], footnote: "A footnote line"}}]},
        {title: "File info: a title's versions in a table (a sample with two)", parts: [
            {c: "FileInfo", at: [0, 0], f: {item: {kind: "movie", title: "A film", year: 2024, versions: [{edition: "Director's Cut", width: 3840, height: 2160, videoCodec: "hevc", size: 61400000000, durationMs: 9960000}, {width: 1920, height: 1080, videoCodec: "h264", audioCodec: "ac3", audioChannels: 6, size: 9800000000, durationMs: 9960000}]}}}]},
        {title: "Keys", parts: [
            {c: "Key", at: [200, 200], f: {text: "a", role: "keyLetter"}}, {c: "Key", at: [320, 200], f: {text: "b", role: "keyLetter", on: true}},
            {c: "Key", at: [440, 200], f: {glyph: "backspace", role: "button"}}, {c: "Key", at: [200, 340], f: {text: "7", role: "padKey", width: 148, height: 118}},
            {c: "Key", at: [380, 340], f: {text: "Forgot PIN?", glass: true, width: 484}}]},
        {title: "Book rows: a series' book under way, a book not started", parts: [{c: "BookRow", at: [200, 200], f: {model: {art: "pkg:/images/cover-3.jpg", number: "Book 1", title: "A book under way", sub: "Read by its narrator", place: "Chapter 6 of 17, 5 h 33 min left", length: "8 h 13 min", progress: 0.32}, on: true}}, {c: "BookRow", at: [200, 400], f: {model: {art: "pkg:/images/cover-5.jpg", title: "A book not started", sub: "Read by its narrator", length: "10 h 53 min"}}}]},
        {title: "The keyboard (Search's)", parts: [
            {c: "Keyboard", at: [200, 120], f: {}}]},
        {title: "The name panel (a new playlist named on the TV, tv-focus 41)", parts: [
            {c: "NamePanel", at: [0, 0], f: {model: {eyebrow: "Alien", title: "New playlist", value: "Road trip", verb: "Create"}}}]},
        {title: "Fields in a sheet: at rest with help, in focus, typed into, refused", parts: [
            {c: "Field", at: [200, 160], f: {label: "Your name in the group", text: "Demo", note: "Shown to the others"}}, {c: "Field", at: [760, 160], f: {label: "Name", text: "Movie night", on: true}},
            {c: "Field", at: [200, 400], f: {label: "Code from the host", text: "KQ7M 2X", typing: true}}, {c: "Field", at: [760, 400], f: {label: "Code from the host", placeholder: "ABCD EFGH", note: "That code didn’t work.", refused: true}}]},
        {title: "Number pads: a PIN with Forgot PIN?, and a two-step code", parts: [
            {c: "PinPad", at: [200, 220], f: {digits: 4, extra: "Forgot PIN?", boxesAt: [0, 700]}},
            {c: "PinPad", at: [1176, 220], f: {digits: 6, boxesAt: [-400, 700]}}]},
        {title: "Key hints: on a page, small, and with words ahead", parts: [
            {c: "KeyHints", at: [200, 200], f: {hints: [{keys: ["OK"], text: "Play"}, {keys: ["Up", "Down"], text: "Choose a page"}]}},
            {c: "KeyHints", at: [200, 320], f: {small: true, hints: [{keys: ["Play/Pause"], text: "Delete a digit"}, {keys: ["Back"], text: "Other profiles"}]}},
            {c: "KeyHints", at: [200, 440], f: {small: true, hints: [{before: "Press", keys: ["Back"], text: "to change the server address."}]}}]},
        {title: "Rate this: no rating, three stars", parts: [{c: "StarRow", at: [200, 200], f: {}}, {c: "StarRow", at: [200, 320], f: {stars: 3}}]},
        {title: "QR cards: with a code, and without", parts: [
            {c: "QrCard", at: [120, 212], f: {link: "https://web.getportico.tv/tv?code=7KQM-4TXP", address: "web.getportico.tv/tv", code: "7KQM-4TXP", note: "This code refreshes by itself."}},
            {c: "QrCard", at: [1060, 212], f: {link: "https://demo.getportico.tv/settings/profile", address: "demo.getportico.tv/settings/profile", note: "Scan the code with your phone, or go to this address on a computer."}}]},
        {title: "A long text read in full (a biography, Home's Details when the server can't be reached)", parts: [
            {c: "TextPanel", at: [0, 0], f: {model: {eyebrow: "An eyebrow", title: "A panel's title", text: "A long text, read in full. Up and Down scroll it when it runs past the panel."}}}]},
        {title: "The season list (five whole, the next peeking)", parts: [{c: "SeasonList", at: [200, 160], f: {seasons: sampleSeasons()}}]},
        {title: "The confirm dialog (focus starts on Cancel)", parts: [{c: "Confirm", at: [0, 0], f: {model: {title: "Sign out of Portico Demo on this TV?", body: "You'll need to sign this TV in again.", verb: "Sign out"}}}]},
        {title: "The side panel a panel or picker opens on (Customize Home, File info): the scrim and the panel from x", parts: [
            {c: "SidePanel", at: [0, 0], f: {x: 1060}}]},
        {title: "The rail, closed", parts: [{c: "Rail", at: [0, 0], f: sampleRail()}]},
        {title: "The rail, open (drawn open; the page keeps the focus)", parts: [{c: "Rail", at: [0, 0], f: sampleRail(), open: true}]}
    ])
    return pages
end function

function listRow(fields as object) as object
    row = {label: "", help: "", scope: "", value: "", control: "", switchOn: false, icon: "", danger: false}
    row.append(fields)
    return row
end function

' Three cards of a shape: with its art, without (the made cover, or a person's initials), and in progress.
function sampleCards(shape as string, art as string) as object
    kind = "movie"
    if shape = "square" then kind = "album"
    if shape = "episode" then kind = "episode"
    if shape = "person" then kind = "person"
    whole = (kind = "album")
    return [
        {id: "1", kind: kind, art: art, whole: whole, title: "With art", sub: "2024", progress: -1, badge: "", overlay: shape = "landscape"},
        {id: "2", kind: kind, art: "", whole: whole, title: "No art at all", sub: "1957", progress: -1, badge: "", overlay: shape = "landscape"},
        {id: "3", kind: kind, art: art, whole: whole, title: "Part watched", sub: "S1 E3", progress: 0.4, badge: "", overlay: shape = "landscape"}]
end function

function sampleSeasons() as object
    seasons = []
    for i = 1 to 7
        seasons.push({title: "Season " + i.toStr(), text: "10 episodes", progress: (i - 1) / 7})
    end for
    return seasons
end function

function sampleRail() as object
    return {entries: [{id: "search", icon: "search", label: "Search"}, {id: "home", icon: "home", label: "Home"},
        {id: "a", icon: "film", label: "Films"}, {id: "b", icon: "tv", label: "TV Shows"}, {id: "c", icon: "music", label: "Music"},
        {id: "d", icon: "book", label: "Audiobooks"}, {id: "e", icon: "film", label: "Films from TMDB"}, {id: "f", icon: "folder", label: "Home videos"},
        {id: "live", icon: "live", label: "Live TV"}, {id: "saved", icon: "bookmark", label: "Saved"}],
        current: "home", profileName: "Demo Member", profileColour: faceColour("blue")}
end function

' The type roles in name order, as many to a page as fit: each in its own font, with its numbers.
function typePages() as object
    pages = []
    parts = []
    x = 80
    y = 40
    for each name in sortedKeys(typeRoles())
        r = typeRoles()[name]
        tall = r[1] * 1.4
        if y + tall > 1010
            x = x + 900
            y = 40
            if x > 1000
                pages.push({title: "Type roles", parts: parts})
                parts = []
                x = 80
            end if
        end if
        parts.push({role: name, at: [x, y]})
        y = y + tall
    end for
    pages.push({title: "Type roles", parts: parts})
    return pages
end function

function sortedKeys(table as object) as object
    keys = table.keys()
    keys.sort()
    return keys
end function

sub show()
    c = colours()
    m.page.removeChildrenIndex(m.page.getChildCount(), 0)
    page = m.pages[m.at]
    m.top.findNode("caption").text = "Shared elements " + (m.at + 1).toStr() + " of " + m.pages.count().toStr() + ": " + page.title + "      Left, Right: the pages"
    if page.frame <> invalid
        drawFrame(page.frame)
    else
        for each part in page.parts
            if part.role <> invalid
                label = m.page.createChild("Label")
                label.translation = part.at
                r = typeRoles()[part.role]
                label.text = part.role + "  " + r[0].toStr() + " / " + r[1].toStr()
                setType(label, part.role, c.text)
            else
                node = m.page.createChild(part.c)
                node.translation = part.at
                for each key in part.f
                    if key = "cards" then node.cards = contentOf(part.f.cards) else node.setField(key, part.f[key])
                end for
                if part.open = true then node.active = true      ' the rail opens when it takes focus...
            end if
        end for
    end if
    m.top.setFocus(true)      ' ...and the page keeps it, so Left and Right always step
end sub

function contentOf(cards as object) as object
    root = createObject("roSGNode", "ContentNode")
    for each card in cards
        root.createChild("ContentNode").addFields(card)
    end for
    return root
end function

' A frame from its own numbers: a line for each position across (…X, left, right, the gutter) and each
' line down (…Y, top, bottom, foot), each with its name and value; the sizes listed in the corner.
sub drawFrame(name as string)
    c = colours()
    numbers = {gutter: frames().gutter, rightEdge: frames().rightEdge}
    frame = frames()[name]
    for each key in frame
        if type(frame[key]) = "roAssociativeArray"
            for each inner in frame[key]
                numbers[key + "." + inner] = frame[key][inner]
            end for
        else
            numbers[key] = frame[key]
        end if
    end for
    sizes = ""
    across = 0
    down = 0
    for each key in sortedKeys(numbers)
        value = numbers[key]
        low = lcase(key)
        ' a number of any kind: a table's numbers come boxed (roInteger) on the TV, so ask what it can do
        if getInterface(value, "ifInt") = invalid and getInterface(value, "ifFloat") = invalid and getInterface(value, "ifDouble") = invalid
            ' not a number
        else if right(low, 1) = "x" or instr(1, low, "left") > 0 or instr(1, low, "right") > 0 or low = "gutter"
            frameLine(value, 0, 2, 1080, c.accent, key + " " + value.toStr(), [value + 6, 60 + (across mod 24) * 40])
            across = across + 1
        else if right(low, 1) = "y" or instr(1, low, "top") > 0 or instr(1, low, "bottom") > 0 or instr(1, low, "foot") > 0
            frameLine(0, value, 1920, 2, c.lineStrong, key + " " + value.toStr(), [1500 - (down mod 3) * 420, value + 4])
            down = down + 1
        else
            sizes = sizes + key + " " + value.toStr() + "   "
        end if
    end for
    list = m.page.createChild("Label")
    list.translation = [40, 990]
    list.width = 1840
    list.text = "Sizes: " + sizes
    setType(list, "help", c.textSecondary)
end sub

sub frameLine(x as float, y as float, w as float, h as float, colour as string, words as string, at as object)
    line = m.page.createChild("Rectangle")
    line.translation = [x, y]
    line.width = w
    line.height = h
    line.color = colour
    label = m.page.createChild("Label")
    label.translation = at
    label.text = words
    setType(label, "micro", colour)
end sub

sub onActive()
    if m.top.active then m.top.setFocus(true)
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not press then return false
    if key = "right" and m.at < m.pages.count() - 1
        m.at = m.at + 1
        show()
        return true
    else if key = "left" and m.at > 0
        m.at = m.at - 1
        show()
        return true
    end if
    return key <> "back"      ' Back leaves the app, as from Home
end function
