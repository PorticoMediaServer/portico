' Focus: Play first; Down reaches the songs (a list that keeps its row), and on an artist Right from
' the songs reaches its albums. Left from the buttons or the songs opens the rail.
sub init()
    c = colours()
    m.f = frames().music
    m.buttons = m.top.findNode("buttons")
    m.buttons.observeField("pressed", "onPressed")
    m.rows = m.top.findNode("rows")
    m.albums = m.top.findNode("albums")
    m.albums.observeField("selectedIndex", "onAlbum")
    m.state = m.top.findNode("state")
    m.state.observeField("pressed", "onRetry")
    m.top.findNode("coverBack").blendColor = c.raised
    setType(m.top.findNode("eyebrow"), "eyebrow", c.textSecondary)
    setType(m.top.findNode("title"), "lockup", c.text)
    setType(m.top.findNode("meta"), "meta", c.textSecondary)
    setType(m.top.findNode("songsHeading"), "rowHeading", c.text)
    m.songs = []
    m.focus = 0          ' 0 the buttons, 1 the songs, 2 the albums
    m.onMore = false     ' focus is on the song row's "..." rather than the row
    m.row = 0
    m.scroll = 0
end sub

sub onItem()
    item = m.top.item
    m.artist = (item.kind = "artist")
    place()
    api("GET", "/v1/items/" + item.id, invalid, "onDetail")
    if m.artist
        m.top.findNode("songsHeading").text = "Top songs"
        lib = ""
        if item.libraryId <> invalid then lib = "library=" + item.libraryId + "&"
        api("GET", "/v1/items?" + lib + "artist=" + item.id + "&kind=track&sort=popular&limit=5", invalid, "onSongs", {fallback: lib})
        api("GET", "/v1/items?" + lib + "parent=" + item.id + "&kind=album&sort=year&order=desc&limit=50", invalid, "onAlbums")
    else
        api("GET", "/v1/items?parent=" + item.id + "&sort=order&limit=200", invalid, "onSongs")
    end if
end sub

' Every part from the page's frame: an album keeps its cover on the left; an artist its circle on the right.
sub place()
    f = m.f
    a = m.artist
    m.top.findNode("coverBack").visible = not a
    m.top.findNode("cover").visible = not a
    m.top.findNode("circle").visible = a
    for each id in ["coverBack", "cover"]
        node = m.top.findNode(id)
        node.translation = [f.coverX, f.coverY]
        node.width = f.cover
        node.height = f.cover
    end for
    m.top.findNode("circle").translation = [f.circleX, f.circleY]
    x = f.wordsX
    if a then x = f.artistX
    eyebrow = m.top.findNode("eyebrow")
    if a then eyebrow.text = "Artist" else eyebrow.text = "Album"
    if a then eyebrow.translation = [x, f.artistEyebrowY] else eyebrow.translation = [x, f.eyebrowY]
    title = m.top.findNode("title")
    title.text = m.top.item.title
    title.width = f.listWidth
    if a then title.width = f.circleX - f.artistX - 40
    if a then title.translation = [x, f.artistTitleY] else title.translation = [x, f.titleY]
    meta = m.top.findNode("meta")
    if a then meta.translation = [x, f.artistMetaY] else meta.translation = [x, f.metaY]
    if a then m.buttons.translation = [x, f.artistButtonsY] else m.buttons.translation = [x, f.buttonsY]
    drawButtons()
    heading = m.top.findNode("songsHeading")
    heading.visible = a
    heading.translation = [f.artistX, f.songsY - 60]
    listY = f.listY
    if a then listY = f.songsY
    window = m.top.findNode("window")
    window.translation = [x, listY]
    window.clippingRect = [-50, -40, listWidth() + 100, 1080 - listY]
    m.albums.slotX = f.albumsX
    m.albums.translation = [0, f.albumsY]
end sub

' Play and Shuffle, then the round buttons (tv-more frames 6 and 8): Favorite (the heart filled once it
' is one), and on an album Add to playlist and More.
sub drawButtons()
    heart = "heart"
    if m.detail <> invalid and m.detail.state <> invalid and m.detail.state.favorite = true then heart = "heartFilled"
    buttons = [{id: "play", text: "Play", icon: "play"}, {id: "shuffle", text: "Shuffle", icon: "shuffle"}, {id: "favorite", text: "", icon: heart}]
    if not m.artist
        buttons.push({id: "playlist", text: "", icon: "listPlus"})
        buttons.push({id: "more", text: "", icon: "more"})
    end if
    m.buttons.buttons = buttons
end sub

function listWidth() as integer
    if m.artist then return m.f.songsWidth
    return m.f.listWidth
end function

sub onDetail(event as object)
    result = event.getData()
    if not result.ok
        showState(result)
        return
    end if
    d = result.data
    parts = []
    if m.artist
        ' genre and country (frame 8); the server gives no active years
        for each kind in ["genre", "country"]
            if tagNames(d, kind, 1) <> "" then parts.push(tagNames(d, kind, 1))
        end for
        if d.counts <> invalid and d.counts.children <> invalid then parts.push(countText(d.counts.children, "album", "albums") + " and " + countText(d.counts.leaves, "song", "songs") + " on your server")
    else
        if d.rootTitle <> invalid then parts.push(d.rootTitle)
        if d.year <> invalid then parts.push(d.year.toStr())
        m.cover = pick(d.images, ["cover", "poster"])
        m.top.findNode("cover").uri = imageUrl(m.cover, m.f.cover, m.f.cover)
    end if
    m.detail = d
    m.metaParts = parts
    drawMeta()
    drawButtons()
end sub

sub drawMeta()
    if m.metaParts = invalid then return
    parts = []
    for each p in m.metaParts
        parts.push(p)
    end for
    ' an album says how much is in it
    if not m.artist and m.songs.count() > 0
        total = 0
        for each song in m.songs
            if song.durationMs <> invalid then total = total + song.durationMs
        end for
        parts.push(countText(m.songs.count(), "song", "songs") + ", " + durationText(total))
    end if
    m.top.findNode("meta").text = parts.join("   ")
end sub

sub onSongs(event as object)
    result = event.getData()
    ctx = event.getRoSGNode().ctx
    if not result.ok
        showState(result)
        return
    end if
    m.songs = result.data.items
    ' nobody here has played the artist yet: all of their songs by name instead
    if m.songs.count() = 0 and ctx <> invalid and ctx.fallback <> invalid
        api("GET", "/v1/items?" + ctx.fallback + "artist=" + m.top.item.id + "&kind=track&sort=title&order=asc&limit=5", invalid, "onSongs")
        return
    end if
    drawRows()
    drawMeta()
end sub

sub onAlbums(event as object)
    result = event.getData()
    if not result.ok then return
    m.albumItems = result.data.items
    cards = createObject("roSGNode", "ContentNode")
    for each album in m.albumItems
        card = cardFor(album, "square")
        card.art = imageUrl(card.art, 214, 214)
        cards.createChild("ContentNode").addFields(card)
    end for
    m.albums.heading = "Albums"
    m.albums.cards = cards
    m.albums.visible = (m.albumItems.count() > 0)
    ' the artist's circle is cut from their album art
    if m.albumItems.count() > 0
        art = pick(m.albumItems[0].images, ["cover", "poster"])
        m.top.findNode("circleArt").uri = imageUrl(art, 412, 412)
    end if
end sub

sub drawRows()
    m.rows.removeChildrenIndex(m.rows.getChildCount(), 0)
    w = listWidth()
    for i = 0 to m.songs.count() - 1
        song = m.songs[i]
        row = m.rows.createChild("TrackRow")
        row.rowWidth = w
        row.rowHeight = m.f.row
        row.translation = [0, i * m.f.row]
        model = {title: song.title, duration: clockText(song.durationMs), number: (i + 1).toStr(), menu: true, favorite: (song.state <> invalid and song.state.favorite = true)}
        if m.artist
            model.art = imageUrl(pick(song.images, ["cover", "poster"]), 72, 72)
            if song.parentTitle <> invalid then model.sub = song.parentTitle
        else if song.index <> invalid
            model.number = song.index.toStr()
        end if
        row.model = model
    end for
    paintRows()
end sub

function clockText(ms as dynamic) as string
    if ms = invalid then return ""
    total = int(ms / 1000)
    secs = total mod 60
    text = secs.toStr()
    if secs < 10 then text = "0" + text
    return int(total / 60).toStr() + ":" + text
end function

sub paintRows()
    if m.songs = invalid then return
    for i = 0 to m.rows.getChildCount() - 1
        row = m.rows.getChild(i)
        model = row.model
        model.playing = (m.songs[i].id = m.top.playingId)
        model.meter = model.playing      ' an album's numbered list marks it with the meter (a row with a cover keeps its cover)
        row.model = model
        row.on = (m.focus = 1 and i = m.row and m.top.isInFocusChain())
        row.moreOn = (row.on and m.onMore = true)
    end for
    room = 1080 - m.top.findNode("window").translation[1] - m.f.row - 40
    y = m.row * m.f.row
    if y - m.scroll > room then m.scroll = y - room
    if y < m.scroll then m.scroll = y
    m.rows.translation = [0, -m.scroll]
end sub

sub showState(result as object)
    m.state.icon = "warning"
    m.state.title = "This page could not be loaded"
    m.state.body = result.error.message
    m.state.buttons = [{id: "retry", text: "Try again", icon: "refresh"}]
    m.state.visible = true
    m.state.active = true
end sub

sub onRetry()
    m.state.visible = false
    onItem()
    m.focus = 0
    onActive()
end sub

sub onActive()
    if not m.top.active then return
    if m.state.visible
        m.state.active = true
    else if m.focus = 1 and m.songs.count() > 0
        m.rows.setFocus(true)
    else if m.focus = 2
        m.albums.active = true
    else
        m.focus = 0
        m.buttons.active = true
    end if
    paintRows()
end sub

' --- playing -------------------------------------------------------------------------------

' Play and Shuffle play the whole album or artist; a song plays on from itself.
sub onPressed(event as object)
    which = event.getData()
    if which = "favorite"
        if m.detail <> invalid then saveFavorite(m.detail, -1)
        return
    else if which = "playlist"
        if m.detail <> invalid then pickPlaylist(m.detail)      ' an album in a playlist puts in its songs
        return
    else if which = "more"
        ' the album's own menu: its artist (Favorite and Add to playlist have their buttons)
        if m.detail <> invalid and m.detail.rootId <> invalid then m.top.menu = {eyebrow: "", title: m.top.item.title, status: "", rows: [{id: "albumArtist", text: "Go to artist", icon: ""}]}
        return
    end if
    if m.songs.count() = 0 then return
    body = {source: {container: m.top.item.id}}
    if event.getData() = "shuffle" then body.shuffle = true
    m.top.open = {item: m.songs[0], play: true, queue: body}
end sub

' Add to playlist is the card menu's own picker (kit/CardMenu.brs): the profile's playlists it may
' change, and New playlist at their head. One picker for every place on the TV.
sub pickPlaylist(item as object)
    m.cardItem = item
    cardMenuChosen("card:playlist")
end sub

' The card menu calls this after a change to a title's state; these pages make theirs themselves.
sub cardMenuChanged(item as object)
end sub

' A favorite changes at once; the server is told (PUT state), and a refusal puts it back.
' `row` is the song's place in the list, or -1 for the album or artist itself.
sub saveFavorite(item as object, row as integer)
    if item.state = invalid then item.state = {}
    on = not (item.state.favorite = true)
    item.state.favorite = on
    if row < 0 then drawButtons() else drawRows()
    api("PUT", "/v1/items/" + item.id + "/state", {favorite: on, madeAgoMs: 0}, "onFavoriteSaved", {row: row, on: on})
end sub

sub onFavoriteSaved(event as object)
    result = event.getData()
    if result.ok then return
    ctx = event.getRoSGNode().ctx
    if ctx.row < 0 then item = m.detail else item = m.songs[ctx.row]
    if item = invalid then return
    item.state.favorite = not ctx.on
    if ctx.row < 0 then drawButtons() else drawRows()
end sub

sub playSong(i as integer)
    song = m.songs[i]
    source = {container: m.top.item.id}
    if m.artist
        ' an artist's top songs play on in the order shown (the artist's own queue runs album by album)
        ids = []
        for each one in m.songs
            ids.push(one.id)
        end for
        source = {items: ids}
    end if
    m.top.open = {item: song, play: true, queue: {source: source, startAt: song.id}}
end sub

sub onAlbum(event as object)
    at = event.getData()
    if m.albumItems <> invalid and at >= 0 and at < m.albumItems.count() then m.top.open = {item: m.albumItems[at], play: false}
end sub

' The song's menu: after the song playing now, or at the end of what is playing; then its album or artist.
sub songMenu()
    song = m.songs[m.row]
    rows = []
    if m.top.playingId <> ""
        rows.push({id: "next", text: "Play next", icon: ""})
        rows.push({id: "end", text: "Add to queue", icon: ""})
    else
        rows.push({id: "play", text: "Play", icon: "play"})
    end if
    if song.state <> invalid and song.state.favorite = true then rows.push({id: "fav", text: "Remove from Favorites", icon: "", gapBefore: true}) else rows.push({id: "fav", text: "Add to Favorites", icon: "", gapBefore: true})
    rows.push({id: "pl", text: "Add to playlist…", icon: ""})
    if m.artist and song.parentId <> invalid then rows.push({id: "album", text: "Go to album", icon: "", gapBefore: true})
    if not m.artist and song.rootId <> invalid then rows.push({id: "artist", text: "Go to artist", icon: "", gapBefore: true})
    m.top.menu = {eyebrow: "", title: song.title, status: "", rows: rows}
end sub

sub onMenuChosen(event as object)
    which = event.getData()
    if cardMenuChosen(which) then return       ' a row of the playlist picker
    if which = "albumArtist"
        m.top.open = {item: {id: m.detail.rootId, kind: "artist", title: m.detail.rootTitle, libraryId: m.detail.libraryId}, play: false}
        return
    end if
    if m.songs.count() = 0 then return
    song = m.songs[m.row]
    if which = "fav"
        saveFavorite(song, m.row)
    else if which = "pl"
        pickPlaylist(song)
    else if which = "play"
        playSong(m.row)
    else if which = "next" or which = "end"
        m.top.open = {item: song, enqueue: which}
    else if which = "album"
        m.top.open = {item: {id: song.parentId, kind: "album", title: song.parentTitle, libraryId: song.libraryId}, play: false}
    else if which = "artist"
        m.top.open = {item: {id: song.rootId, kind: "artist", title: song.rootTitle, libraryId: song.libraryId}, play: false}
    end if
end sub

' --- keys ------------------------------------------------------------------------------------

function pressKey(key as string) as boolean
    if m.focus <> 1 then return false
    m.onMore = false
    if key = "up" and m.row > 0
        m.row = m.row - 1
    else if key = "down" and m.row < m.songs.count() - 1
        m.row = m.row + 1
    else
        return false
    end if
    paintRows()
    return true
end function

function onKeyEvent(key as string, press as boolean) as boolean
    holdKey(key, press and m.focus = 1 and (key = "up" or key = "down"))
    if not press then return false
    if m.state.visible then return false
    if m.focus = 0
        if key = "down" and m.songs.count() > 0
            m.focus = 1
            m.rows.setFocus(true)
            paintRows()
            return true
        end if
        return false
    else if m.focus = 1
        if key = "OK"
            ' OK on the row plays the song; on its "..." it opens the song's menu
            if m.onMore then songMenu() else playSong(m.row)
            return true
        else if key = "right" and not m.onMore
            m.onMore = true
            paintRows()
            return true
        else if key = "left" and m.onMore
            m.onMore = false
            paintRows()
            return true
        else if key = "options"
            songMenu()
            return true
        else if key = "up" and m.row = 0
            m.focus = 0
            m.buttons.active = true
            paintRows()
            return true
        else if key = "right" and m.artist and m.albums.visible
            m.onMore = false
            m.focus = 2
            m.albums.active = true
            paintRows()
            return true
        end if
        return pressKey(key)
    else if m.focus = 2
        if key = "left"
            m.focus = 1
            m.rows.setFocus(true)
            paintRows()
            return true
        end if
        return false
    end if
    return false
end function
