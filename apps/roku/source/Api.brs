' Calling the server from a component: api("GET", "/v1/home", invalid, "onHome").
' The callback gets an event whose data is {ok, status, data, error}; event.getRoSGNode().ctx
' is whatever was passed as ctx.
' Pass isPublic for the requests made before anyone is signed in (no sign-in is sent or renewed).
' Pass key to send a POST again as the same request (the server answers a repeat with what the first made).
' Pass origin while signing in: the request then goes to that server and no other, whatever server
' the app has turned to by the time it is sent.
' Pass ifMatch for a change the server asks to name what it changes (a queue's entries take "*").
function api(method as string, path as string, body as dynamic, callback as string, ctx = invalid as dynamic, isPublic = false as boolean, key = "" as string, origin = "" as string, ifMatch = "" as string) as object
    reply = createObject("roSGNode", "ApiReply")
    if ctx <> invalid then reply.ctx = ctx
    reply.observeField("result", callback)
    m.global.api.request = {method: method, path: path, body: body, reply: reply, public: isPublic, key: key, origin: origin, ifMatch: ifMatch}
    return reply
end function

' Where a picture lives, with the size it will be drawn at so the server can send its small copy.
function imageUrl(path as dynamic, w as integer, h as integer) as string
    if path = invalid or path = "" then return ""
    return m.global.origin + path + "?w=" + w.toStr() + "&h=" + h.toStr()
end function

' A cover's colour for the room behind it: the server's small preblurred copy (ambient-v2, the
' saturated wash the native apps use), drawn large.
function ambientUrl(path as dynamic) as string
    if path = invalid or path = "" then return ""
    return m.global.origin + path + "?size=ambient-v2"
end function
