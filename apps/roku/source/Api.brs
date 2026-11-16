' Calling the server from a component: api("GET", "/v1/home", invalid, "onHome").
' The callback gets an event whose data is {ok, status, data, error}; event.getRoSGNode().ctx
' is whatever was passed as ctx.
' Pass isPublic for the requests made before anyone is signed in (no sign-in is sent or renewed).
function api(method as string, path as string, body as dynamic, callback as string, ctx = invalid as dynamic, isPublic = false as boolean) as object
    reply = createObject("roSGNode", "ApiReply")
    if ctx <> invalid then reply.ctx = ctx
    reply.observeField("result", callback)
    m.global.api.request = {method: method, path: path, body: body, reply: reply, public: isPublic}
    return reply
end function

' Where a picture lives, with the size it will be drawn at so the server can send its small copy.
function imageUrl(path as dynamic, w as integer, h as integer) as string
    if path = invalid or path = "" then return ""
    return m.global.origin + path + "?w=" + w.toStr() + "&h=" + h.toStr()
end function
