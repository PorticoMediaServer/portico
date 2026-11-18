' Add to playlist (tv-more frame 7, tv-07-f7), shared by the pages that offer it: the profile's
' playlists it may change (GET /v1/lists?kind=playlist) open in the side sheet, and the songs join the
' one chosen (POST /v1/lists/{id}/entries). A component including this has a `menu` field, and hands
' every id its sheet chooses to addToPlaylist first.

sub askPlaylists(itemIds as object, title as string)
    m.playlistItems = itemIds
    m.playlistTitle = title
    api("GET", "/v1/lists?kind=playlist&limit=100", invalid, "onPlaylists")
end sub

sub onPlaylists(event as object)
    result = event.getData()
    rows = []
    status = ""
    m.playlistNames = {}
    if result.ok
        rows = playlistRows(result.data.items)
        for each list in result.data.items
            m.playlistNames[list.id] = list.name
        end for
        ' a playlist is made on a phone or a computer: the TV has no keyboard worth naming one with
        if rows.count() = 0 then status = "Playlists made on your phone or computer appear here."
    else
        status = whyFailed(result)
    end if
    if rows.count() = 0 then rows.push({id: "pl:", text: "Close"})
    m.top.menu = {eyebrow: m.playlistTitle, title: "Add to playlist", status: status, rows: rows}
end sub

' True when the sheet's choice was this flow's: a playlist (the songs join it) or its Close.
function addToPlaylist(which as string) as boolean
    if left(which, 3) <> "pl:" then return false
    id = mid(which, 4)
    if id <> "" then api("POST", "/v1/lists/" + id + "/entries", {items: m.playlistItems}, "onPlaylistAdded", {name: m.playlistNames[id]})
    return true
end function

sub onPlaylistAdded(event as object)
    result = event.getData()
    name = event.getRoSGNode().ctx.name
    if not result.ok
        status = "Couldn't add to " + name + ". " + whyFailed(result)
    else if result.data.added > 0
        status = "Added to " + name + "."
    else
        status = "Already in " + name + "."
    end if
    m.top.menu = {eyebrow: m.playlistTitle, title: "Add to playlist", status: status, rows: [{id: "pl:", text: "Done"}]}
end sub
